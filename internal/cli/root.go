// Package cli implements the command-line interface for xnc.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/docker"
)

// Globals holds parsed global flags.
type Globals struct {
	Home    string
	Image   string
	JSON    bool
	Quiet   bool
	Docker  docker.Ops
}

// Run dispatches a CLI command.
func Run(cmd string, args []string) {
	g := parseGlobals(&args)

	// Validate home directory before dispatching.
	if err := agent.ValidateHome(g.Home, false); err != nil {
		die("%v", err)
	}

	switch cmd {
	case "setup":
		cmdSetup(g, args)
	case "start":
		cmdStart(g, args)
	case "stop":
		cmdStop(g, args)
	case "restart":
		cmdRestart(g, args)
	case "destroy":
		cmdDestroy(g, args)
	case "clone":
		cmdClone(g, args)
	case "rename":
		cmdRename(g, args)
	case "list":
		cmdList(g, args)
	case "running":
		cmdRunning(g, args)
	case "status":
		cmdStatus(g, args)
	case "config":
		cmdConfig(g, args)
	case "send":
		cmdSend(g, args)
	case "cli":
		cmdCLI(g, args)
	case "logs":
		cmdLogs(g, args)
	case "drain":
		cmdDrain(g, args)
	case "watch":
		cmdWatch(g, args)
	case "costs":
		cmdCosts(g, args)
	case "snapshot":
		cmdSnapshot(g, args)
	case "restore":
		cmdRestore(g, args)
	case "snapshots":
		cmdSnapshots(g, args)
	case "snapshot-delete":
		cmdSnapshotDelete(g, args)
	case "image":
		cmdImage(g, args)
	case "cp-to":
		cmdCpTo(g, args)
	case "cp-from":
		cmdCpFrom(g, args)
	case "skill":
		cmdSkill(g, args)
	case "persona":
		cmdPersona(g, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\nRun 'xnc help' for usage.\n", cmd)
		os.Exit(1)
	}
}

// parseGlobals extracts global flags from args, returning the remaining args.
func parseGlobals(args *[]string) Globals {
	g := Globals{
		Home:  agent.DefaultHome(),
		Image: defaultImage(),
	}

	var remaining []string
	a := *args
	for i := 0; i < len(a); i++ {
		switch a[i] {
		case "--home":
			if i+1 < len(a) {
				g.Home = a[i+1]
				i++
			}
		case "--image":
			if i+1 < len(a) {
				g.Image = a[i+1]
				i++
			}
		case "--json":
			g.JSON = true
		case "--quiet", "-q":
			g.Quiet = true
		default:
			remaining = append(remaining, a[i])
		}
	}
	*args = remaining
	return g
}

func defaultImage() string {
	if img := os.Getenv("XNC_IMAGE"); img != "" {
		return img
	}
	if img := os.Getenv("XNULLCLAW_IMAGE"); img != "" {
		return img
	}
	return "nullclaw:latest"
}

// ensureDocker lazily initializes the Docker client.
func (g *Globals) ensureDocker() {
	if g.Docker != nil {
		return
	}
	cli, err := docker.NewClient()
	if err != nil {
		die("Docker not available: %v", err)
	}
	g.Docker = cli
}

// hasFlag checks if a flag is present in args and removes it.
func hasFlag(args *[]string, flag string) bool {
	var remaining []string
	found := false
	for _, a := range *args {
		if a == flag {
			found = true
		} else {
			remaining = append(remaining, a)
		}
	}
	*args = remaining
	return found
}

// flagValue extracts a --key value pair from args.
func flagValue(args *[]string, flag string) (string, bool) {
	a := *args
	var remaining []string
	var val string
	found := false
	for i := 0; i < len(a); i++ {
		if a[i] == flag && i+1 < len(a) {
			val = a[i+1]
			found = true
			i++
		} else {
			remaining = append(remaining, a[i])
		}
	}
	*args = remaining
	return val, found
}

// agentNames returns non-flag arguments (agent names).
func agentNames(args []string) []string {
	var names []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			names = append(names, a)
		}
	}
	return names
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", a...)
	os.Exit(1)
}

func info(format string, a ...any) {
	fmt.Printf(":: "+format+"\n", a...)
}

func ok(format string, a ...any) {
	fmt.Printf("ok: "+format+"\n", a...)
}
