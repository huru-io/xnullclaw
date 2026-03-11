package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/docker"
)

func cmdSend(g Globals, args []string) {
	all := hasFlag(&args, "--all")
	names := agentNames(args)

	if !all && len(names) == 0 {
		die("usage: xnc send <agents...> [--all]  (reads stdin)")
	}

	// Read message from stdin.
	msg, err := io.ReadAll(os.Stdin)
	if err != nil {
		die("read stdin: %v", err)
	}
	if len(msg) == 0 {
		die("no message on stdin")
	}

	g.ensureDocker()
	ctx := context.Background()

	if all {
		prefix := agent.ContainerPrefix(g.Home)
		containers, err := g.Docker.ListContainers(ctx, prefix)
		if err != nil {
			die("list containers: %v", err)
		}
		for _, c := range containers {
			if c.State == "running" {
				n := strings.TrimPrefix(c.Name, prefix)
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			die("no running agents")
		}
	}

	type result struct {
		Agent    string `json:"agent"`
		Response string `json:"response"`
		Error    string `json:"error,omitempty"`
	}

	var mu sync.Mutex
	var results []result
	var wg sync.WaitGroup

	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			cn := agent.ContainerName(g.Home, n)

			resp, err := g.Docker.ExecSync(ctx, cn,
				[]string{"flock", "/tmp/.send.lock", "nullclaw", "agent", "-s", "mux"},
				strings.NewReader(string(msg)),
			)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results = append(results, result{Agent: n, Error: err.Error()})
				if !g.Quiet {
					fmt.Fprintf(os.Stderr, "%s error: %v\n", n, err)
				}
			} else {
				results = append(results, result{Agent: n, Response: resp})
				if !g.JSON {
					if len(names) > 1 {
						fmt.Printf("── %s ──\n", n)
					}
					fmt.Print(resp)
					if !strings.HasSuffix(resp, "\n") {
						fmt.Println()
					}
				}
			}
		}(name)
	}

	wg.Wait()

	if g.JSON {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
	}
}

func cmdCLI(g Globals, args []string) {
	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc cli <agent> [args...]")
	}

	name := names[0]
	if !agent.Exists(g.Home, name) {
		die("agent %q does not exist", name)
	}

	g.ensureDocker()
	ctx := context.Background()

	cn := agent.ContainerName(g.Home, name)
	running, err := g.Docker.IsRunning(ctx, cn)
	if err != nil {
		die("check %s: %v", name, err)
	}
	if !running {
		die("agent %s is not running", name)
	}

	// Build command: nullclaw agent [extra args...]
	cmd := []string{"nullclaw", "agent"}
	if len(names) > 1 {
		cmd = append(cmd, names[1:]...)
	}

	if err := g.Docker.AttachInteractive(ctx, cn, cmd); err != nil {
		die("cli %s: %v", name, err)
	}
}

func cmdLogs(g Globals, args []string) {
	follow := hasFlag(&args, "-f") || hasFlag(&args, "--follow")
	tailStr, _ := flagValue(&args, "--tail")
	names := agentNames(args)

	if len(names) == 0 {
		die("usage: xnc logs <agent> [-f] [--tail N]")
	}

	name := names[0]
	g.ensureDocker()
	ctx := context.Background()

	cn := agent.ContainerName(g.Home, name)
	opts := docker.LogOpts{
		Follow: follow,
		Tail:   tailStr,
	}
	if opts.Tail == "" {
		opts.Tail = "100"
	}

	rc, err := g.Docker.ContainerLogs(ctx, cn, opts)
	if err != nil {
		die("logs %s: %v", name, err)
	}
	defer rc.Close()

	io.Copy(os.Stdout, rc)
}

func cmdDrain(g Globals, args []string) {
	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc drain <agent>")
	}

	name := names[0]
	if !agent.Exists(g.Home, name) {
		die("agent %q does not exist", name)
	}

	g.ensureDocker()
	ctx := context.Background()

	cn := agent.ContainerName(g.Home, name)
	dir := agent.Dir(g.Home, name)

	// Get last drain time.
	lastDrain := agent.ReadMetaKey(dir, "LAST_DRAIN", "")

	opts := docker.LogOpts{Tail: "all"}
	if lastDrain != "" {
		opts.Since = lastDrain
	}

	rc, err := g.Docker.ContainerLogs(ctx, cn, opts)
	if err != nil {
		die("drain %s: %v", name, err)
	}
	defer rc.Close()

	io.Copy(os.Stdout, rc)

	// Update drain timestamp.
	now := time.Now().UTC().Format(time.RFC3339)
	agent.WriteMeta(dir, "LAST_DRAIN", now)
}

func cmdWatch(g Globals, args []string) {
	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc watch <agent>")
	}

	// Watch = logs --follow --tail 0 (live only).
	name := names[0]
	g.ensureDocker()
	ctx := context.Background()

	cn := agent.ContainerName(g.Home, name)
	rc, err := g.Docker.ContainerLogs(ctx, cn, docker.LogOpts{
		Follow: true,
		Tail:   "0",
	})
	if err != nil {
		die("watch %s: %v", name, err)
	}
	defer rc.Close()

	io.Copy(os.Stdout, rc)
}

func cmdCosts(g Globals, args []string) {
	today := hasFlag(&args, "--today")
	month := hasFlag(&args, "--month")
	names := agentNames(args)

	if len(names) == 0 {
		die("usage: xnc costs <agent> [--today|--month|--json]")
	}

	name := names[0]
	if !agent.Exists(g.Home, name) {
		die("agent %q does not exist", name)
	}

	dir := agent.Dir(g.Home, name)

	var since time.Time
	label := "all time"
	now := time.Now().UTC()
	if today {
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		label = "today"
	} else if month {
		since = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		label = "this month"
	}

	entries, err := agent.ReadCosts(dir, since)
	if err != nil {
		die("costs: %v", err)
	}

	summary := agent.SummarizeCosts(entries)

	if g.JSON {
		data, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("Agent: %s (%s)\n", name, label)
	fmt.Printf("Total: $%.4f (%d calls)\n", summary.TotalUSD, summary.Count)
	if len(summary.ByModel) > 0 {
		fmt.Println("By model:")
		for model, cost := range summary.ByModel {
			fmt.Printf("  %-30s $%.4f\n", model, cost)
		}
	}
}

