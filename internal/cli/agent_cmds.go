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
	braveKey, _ := flagValue(&args, "--brave-key")
	model, _ := flagValue(&args, "--model")
	systemPrompt, _ := flagValue(&args, "--system-prompt")

	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc setup <name> [--openai-key KEY] [--anthropic-key KEY] [--openrouter-key KEY] [--brave-key KEY] [--model MODEL] [--system-prompt TEXT]")
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
	if braveKey == "" {
		braveKey = os.Getenv("BRAVE_API_KEY")
	}

	opts := agent.SetupOpts{
		OpenAIKey:     openaiKey,
		AnthropicKey:  anthropicKey,
		OpenRouterKey: openrouterKey,
		BraveKey:      braveKey,
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
		opts := agent.StartOpts(g.Image, g.Home, name, agentPort)

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

// statusCategory maps Docker container state to a filter category.
func statusCategory(state string) string {
	switch state {
	case "running":
		return "running"
	case "restarting", "dead":
		return "error"
	case "exited":
		// Exited containers with non-zero exit codes are errors,
		// but we can't distinguish from ListContainers alone.
		// The Status string contains the exit code, e.g. "Exited (1) ...".
		return "stopped"
	default:
		return "stopped"
	}
}

// statusCategoryFromStatus refines the category using the human-readable status
// string which contains exit codes, e.g. "Exited (1) 5 minutes ago".
func statusCategoryFromStatus(state, status string) string {
	cat := statusCategory(state)
	if state == "exited" && strings.Contains(status, "Exited (") && !strings.Contains(status, "Exited (0)") {
		cat = "error"
	}
	return cat
}

func cmdList(g Globals, args []string) {
	// list is an alias for status with no filters.
	cmdStatus(g, args)
}

func cmdRunning(g Globals, args []string) {
	// running is an alias for status --running.
	args = append(args, "--running")
	cmdStatus(g, args)
}

func cmdStatus(g Globals, args []string) {
	filterRunning := hasFlag(&args, "--running")
	filterStopped := hasFlag(&args, "--stopped")
	filterError := hasFlag(&args, "--error")
	hasFilter := filterRunning || filterStopped || filterError

	names := agentNames(args)

	allAgents, err := agent.ListAll(g.Home)
	if err != nil {
		die("status: %v", err)
	}

	// If specific names given, validate they exist.
	if len(names) > 0 {
		nameSet := map[string]bool{}
		for _, n := range names {
			nameSet[agent.CanonicalName(n)] = true
		}
		var filtered []agent.Info
		for _, a := range allAgents {
			if nameSet[agent.CanonicalName(a.Name)] {
				filtered = append(filtered, a)
			}
		}
		// Report names not found.
		foundSet := map[string]bool{}
		for _, a := range filtered {
			foundSet[agent.CanonicalName(a.Name)] = true
		}
		for _, n := range names {
			if !foundSet[agent.CanonicalName(n)] {
				fmt.Fprintf(os.Stderr, "warning: agent %q not found\n", n)
			}
		}
		allAgents = filtered
	}

	if len(allAgents) == 0 {
		if g.JSON {
			fmt.Println("[]")
		} else {
			info("no agents configured")
		}
		return
	}

	// Build container state map — try Docker, skip gracefully if unavailable.
	stateMap := map[string]docker.ContainerInfo{}
	if g.Docker == nil {
		if cli, err := docker.NewClient(); err == nil {
			g.Docker = cli
		}
	}
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

	type statusEntry struct {
		Name     string `json:"name"`
		Emoji    string `json:"emoji,omitempty"`
		State    string `json:"state"`
		Status   string `json:"status"`
		Category string `json:"category"` // running, stopped, error
		Ports    string `json:"ports,omitempty"`
	}

	var entries []statusEntry
	for _, a := range allAgents {
		e := statusEntry{
			Name:     a.Name,
			Emoji:    a.Emoji,
			State:    "stopped",
			Status:   "stopped",
			Category: "stopped",
		}
		canon := agent.CanonicalName(a.Name)
		if c, ok := stateMap[canon]; ok {
			e.State = c.State
			e.Status = c.Status
			e.Category = statusCategoryFromStatus(c.State, c.Status)
			if len(c.Ports) > 0 {
				e.Ports = strings.Join(c.Ports, ", ")
			}
		}

		// Apply filters.
		if hasFilter {
			match := false
			if filterRunning && e.Category == "running" {
				match = true
			}
			if filterStopped && e.Category == "stopped" {
				match = true
			}
			if filterError && e.Category == "error" {
				match = true
			}
			if !match {
				continue
			}
		}

		entries = append(entries, e)
	}

	if g.JSON {
		data, _ := json.MarshalIndent(entries, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(entries) == 0 {
		info("no matching agents")
		return
	}

	for _, e := range entries {
		emoji := e.Emoji
		if emoji == "" {
			emoji = " "
		}
		ports := ""
		if e.Ports != "" {
			ports = " [" + e.Ports + "]"
		}
		fmt.Printf("  %s %-15s %s%s\n", emoji, e.Name, e.Status, ports)
	}
	fmt.Printf("\n%d agent(s)\n", len(entries))
}
