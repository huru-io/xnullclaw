package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/docker"
)

func cmdSetup(g Globals, args []string) {
	openaiKey, _ := flagValue(&args, "--openai-key")
	anthropicKey, _ := flagValue(&args, "--anthropic-key")
	openrouterKey, _ := flagValue(&args, "--openrouter-key")
	model, _ := flagValue(&args, "--model")
	systemPrompt, _ := flagValue(&args, "--system-prompt")

	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc setup <name> [--openai-key KEY] [--anthropic-key KEY] [--openrouter-key KEY] [--model MODEL] [--system-prompt TEXT]")
	}

	// Fall back to environment variables.
	if openaiKey == "" {
		openaiKey = os.Getenv("OPENAI_API_KEY")
	}
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if openrouterKey == "" {
		openrouterKey = os.Getenv("OPENROUTER_API_KEY")
	}

	opts := agent.SetupOpts{
		OpenAIKey:     openaiKey,
		AnthropicKey:  anthropicKey,
		OpenRouterKey: openrouterKey,
		Model:         model,
		SystemPrompt:  systemPrompt,
	}

	// Test keys once before creating agents.
	testKeys(openaiKey, anthropicKey, openrouterKey)

	for _, name := range names {
		if err := agent.Setup(g.Home, name, opts); err != nil {
			die("%v", err)
		}

		dir := agent.Dir(g.Home, name)
		meta, _ := agent.ReadMeta(dir)
		ok("agent %s %s created at %s", meta["EMOJI"], name, dir)
	}
}

// testKeys validates provider keys and prints results.
// Dies if all provided keys are invalid.
func testKeys(openaiKey, anthropicKey, openrouterKey string) {
	type keyTest struct {
		provider string
		key      string
	}
	var tests []keyTest
	if openaiKey != "" {
		tests = append(tests, keyTest{"openai", openaiKey})
	}
	if anthropicKey != "" {
		tests = append(tests, keyTest{"anthropic", anthropicKey})
	}
	if openrouterKey != "" {
		tests = append(tests, keyTest{"openrouter", openrouterKey})
	}

	if len(tests) == 0 {
		return
	}

	var anyValid bool
	for _, t := range tests {
		if err := agent.TestProviderKey(t.provider, t.key); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s key: %v\n", t.provider, err)
		} else {
			ok("%s key verified", t.provider)
			anyValid = true
		}
	}
	if !anyValid {
		die("all provided API keys failed validation")
	}
}

func cmdStart(g Globals, args []string) {
	portStr, _ := flagValue(&args, "--port")
	names := agentNames(args)

	if len(names) == 0 {
		die("usage: xnc start <agents...> [--port N]")
	}

	g.ensureDocker()
	ctx := context.Background()

	port := 0
	if portStr != "" {
		var err error
		port, err = strconv.Atoi(portStr)
		if err != nil {
			die("invalid port: %s", portStr)
		}
	}

	// For multi-agent start, only first agent gets the explicit port.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string

	for i, name := range names {
		if err := agent.ValidateName(name); err != nil {
			die("%v", err)
		}
		if !agent.Exists(g.Home, name) {
			die("agent %q does not exist", name)
		}
		if !agent.HasProviderKey(g.Home, name) {
			die("agent %q has no API key configured — set one with: xnc config set %s openai_key <key>", name, name)
		}

		containerName := agent.ContainerName(g.Home, name)

		// Check if already running.
		running, err := g.Docker.IsRunning(ctx, containerName)
		if err != nil {
			die("check %s: %v", name, err)
		}
		if running {
			info("%s is already running", name)
			continue
		}

		agentPort := 0
		if i == 0 {
			agentPort = port
		}
		agentDir := agent.Dir(g.Home, name)
		opts := docker.ContainerOpts{
			Image:    g.Image,
			Cmd:      []string{"gateway"},
			AgentDir: agentDir,
			Port:     agentPort,
		}

		wg.Add(1)
		go func(n, cn string, o docker.ContainerOpts) {
			defer wg.Done()
			if err := g.Docker.StartContainer(ctx, cn, o); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("%s: %v", n, err))
				mu.Unlock()
				return
			}

			// Update meta.
			if o.Port > 0 {
				agent.WriteMeta(agent.Dir(g.Home, n), "HOST_PORT", strconv.Itoa(o.Port))
			}
			meta, _ := agent.ReadMeta(agent.Dir(g.Home, n))
			mu.Lock()
			ok("started %s %s", meta["EMOJI"], n)
			mu.Unlock()
		}(name, containerName, opts)
	}

	wg.Wait()
	if len(errs) > 0 {
		die("failed to start: %s", strings.Join(errs, "; "))
	}
}

func cmdStop(g Globals, args []string) {
	all := hasFlag(&args, "--all")
	names := agentNames(args)

	if !all && len(names) == 0 {
		die("usage: xnc stop <agents...> [--all]")
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
			info("no running agents")
			return
		}
	}

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			cn := agent.ContainerName(g.Home, n)
			if err := g.Docker.StopContainer(ctx, cn); err != nil {
				fmt.Fprintf(os.Stderr, "warning: stop %s: %v\n", n, err)
				return
			}
			if err := g.Docker.RemoveContainer(ctx, cn, false); err != nil {
				// Not fatal — container may auto-remove.
			}
			// Clear runtime meta.
			dir := agent.Dir(g.Home, n)
			agent.DeleteMetaKey(dir, "HOST_PORT")
			ok("stopped %s", n)
		}(name)
	}
	wg.Wait()
}

func cmdRestart(g Globals, args []string) {
	// Stop then start with same flags.
	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc restart <agents...>")
	}

	// Preserve flags for start.
	stopArgs := make([]string, len(names))
	copy(stopArgs, names)

	cmdStop(g, stopArgs)
	cmdStart(g, args)
}

func cmdDestroy(g Globals, args []string) {
	yes := hasFlag(&args, "--yes")
	names := agentNames(args)

	if len(names) == 0 {
		die("usage: xnc destroy <agents...> [--yes]")
	}

	if !yes {
		fmt.Fprintf(os.Stderr, "This will permanently delete: %s\n", strings.Join(names, ", "))
		fmt.Fprintf(os.Stderr, "Run with --yes to confirm.\n")
		os.Exit(1)
	}

	g.ensureDocker()
	ctx := context.Background()

	for _, name := range names {
		if !agent.Exists(g.Home, name) {
			fmt.Fprintf(os.Stderr, "warning: agent %q does not exist, skipping\n", name)
			continue
		}

		// Stop container if running.
		cn := agent.ContainerName(g.Home, name)
		running, _ := g.Docker.IsRunning(ctx, cn)
		if running {
			g.Docker.StopContainer(ctx, cn)
			g.Docker.RemoveContainer(ctx, cn, true)
		}

		if err := agent.Destroy(g.Home, name); err != nil {
			fmt.Fprintf(os.Stderr, "warning: destroy %s: %v\n", name, err)
			continue
		}
		ok("destroyed %s", name)
	}
}

func cmdList(g Globals, args []string) {
	agents, err := agent.ListAll(g.Home)
	if err != nil {
		die("list: %v", err)
	}

	if len(agents) == 0 {
		if g.JSON {
			fmt.Println("[]")
		} else {
			info("no agents configured")
		}
		return
	}

	// Build container state map if Docker is available.
	stateMap := map[string]docker.ContainerInfo{}
	if g.Docker != nil {
		prefix := agent.ContainerPrefix(g.Home)
		containers, err := g.Docker.ListContainers(context.Background(), prefix)
		if err == nil {
			for _, c := range containers {
				name := strings.TrimPrefix(c.Name, prefix)
				stateMap[name] = c
			}
		}
	}

	type listEntry struct {
		Name    string `json:"name"`
		Emoji   string `json:"emoji,omitempty"`
		Created string `json:"created,omitempty"`
		Status  string `json:"status"`
	}

	var entries []listEntry
	for _, a := range agents {
		e := listEntry{
			Name:    a.Name,
			Emoji:   a.Emoji,
			Created: a.Created,
			Status:  "stopped",
		}
		canon := agent.CanonicalName(a.Name)
		if c, ok := stateMap[canon]; ok {
			e.Status = c.Status
		}
		entries = append(entries, e)
	}

	if g.JSON {
		data, _ := json.MarshalIndent(entries, "", "  ")
		fmt.Println(string(data))
		return
	}

	for _, e := range entries {
		emoji := e.Emoji
		if emoji == "" {
			emoji = " "
		}
		fmt.Printf("  %s %-15s %s\n", emoji, e.Name, e.Status)
	}
	fmt.Printf("\n%d agent(s)\n", len(entries))
}

func cmdRunning(g Globals, args []string) {
	g.ensureDocker()
	ctx := context.Background()

	prefix := agent.ContainerPrefix(g.Home)
	containers, err := g.Docker.ListContainers(ctx, prefix)
	if err != nil {
		die("list containers: %v", err)
	}

	// Filter to running only.
	var running []docker.ContainerInfo
	for _, c := range containers {
		if c.State == "running" {
			running = append(running, c)
		}
	}

	if g.JSON {
		data, _ := json.MarshalIndent(running, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(running) == 0 {
		info("no running agents")
		return
	}

	for _, c := range running {
		name := strings.TrimPrefix(c.Name, prefix)
		dir := agent.Dir(g.Home, name)
		meta, _ := agent.ReadMeta(dir)
		emoji := meta["EMOJI"]
		if emoji == "" {
			emoji = " "
		}

		ports := ""
		if len(c.Ports) > 0 {
			ports = " [" + strings.Join(c.Ports, ", ") + "]"
		}
		fmt.Printf("  %s %-15s %s%s\n", emoji, name, c.Status, ports)
	}
	fmt.Printf("\n%d running\n", len(running))
}

func cmdStatus(g Globals, args []string) {
	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc status <agents...> [--json]")
	}

	g.ensureDocker()
	ctx := context.Background()

	type statusInfo struct {
		Name      string `json:"name"`
		Emoji     string `json:"emoji"`
		Exists    bool   `json:"exists"`
		Running   bool   `json:"running"`
		Container string `json:"container,omitempty"`
		Status    string `json:"status"`
		Port      string `json:"port,omitempty"`
	}

	var results []statusInfo
	for _, name := range names {
		si := statusInfo{Name: name}

		if !agent.Exists(g.Home, name) {
			si.Status = "not found"
			results = append(results, si)
			continue
		}

		si.Exists = true
		dir := agent.Dir(g.Home, name)
		meta, _ := agent.ReadMeta(dir)
		si.Emoji = meta["EMOJI"]
		si.Port = meta["HOST_PORT"]

		cn := agent.ContainerName(g.Home, name)
		info, err := g.Docker.InspectContainer(ctx, cn)
		if err != nil {
			si.Status = "stopped"
		} else {
			si.Running = info.State == "running"
			si.Container = info.ID
			si.Status = info.Status
		}
		results = append(results, si)
	}

	if g.JSON {
		data, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(data))
		return
	}

	for _, s := range results {
		emoji := s.Emoji
		if emoji == "" {
			emoji = " "
		}
		state := "stopped"
		if s.Running {
			state = "running"
		}
		if !s.Exists {
			state = "not found"
		}
		port := ""
		if s.Port != "" {
			port = fmt.Sprintf(" (port %s)", s.Port)
		}
		fmt.Printf("  %s %-15s %s%s\n", emoji, s.Name, state, port)
	}
}
