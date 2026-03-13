package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/cli"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/mux"
)

var version = "dev"

func main() {
	// Set restrictive umask so all file/directory creates honor their mode bits.
	// Without this, the default umask 0022 makes 0600 files world-readable (0644).
	syscall.Umask(0077)

	if len(os.Args) < 2 {
		printDashboard()
		return
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "init":
		runInit(args)
	case "mux":
		runMux(args)
	case "version", "--version":
		fmt.Printf("xnc %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		runCLI(cmd, args)
	}
}

// runMux handles all mux subcommands: start (foreground/daemon), stop, status, logs.
func runMux(args []string) {
	// Parse flags.
	home := agent.DefaultHome()
	image := agent.DefaultImage()
	foreground := false

	var remaining []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--home":
			if i+1 < len(args) {
				home = args[i+1]
				i++
			}
		case "--image":
			if i+1 < len(args) {
				image = args[i+1]
				i++
			}
		case "--foreground", "-f":
			foreground = true
		default:
			remaining = append(remaining, args[i])
		}
	}

	// Validate home directory.
	if err := agent.ValidateHome(home, false); err != nil {
		log.Fatalf("%v", err)
	}

	muxHome := filepath.Join(home, "mux")
	pidFile := filepath.Join(muxHome, "mux.pid")
	logFile := filepath.Join(muxHome, "mux.log")
	cfgPath := filepath.Join(muxHome, "config.json")

	// Subcommand dispatch.
	sub := ""
	if len(remaining) > 0 {
		sub = remaining[0]
	}

	switch sub {
	case "start":
		// Check if already running.
		if pid := readPID(pidFile); pid > 0 && processAlive(pid) {
			fmt.Fprintf(os.Stderr, "mux already running (PID %d)\n", pid)
			os.Exit(1)
		}

		if foreground {
			// Run in foreground — write PID file for status/stop.
			if err := os.MkdirAll(muxHome, 0700); err != nil {
				log.Fatalf("create mux home: %v", err)
			}
			lockFile, err := acquirePIDLock(pidFile)
			if err != nil {
				log.Fatalf("another mux instance may be running: %v", err)
			}
			defer func() {
				removeWarn(pidFile, "PID file")
				removeWarn(pidFile+".lock", "lock file")
				lockFile.Close()
			}()
			writePID(pidFile, os.Getpid())

			var lastChatID int64
			mc := mux.Config{
				Home:       home,
				CfgPath:    cfgPath,
				Image:      image,
				Version:    version,
				Foreground: true,
				LastChatID: &lastChatID,
			}
			reloadBackoff := time.Second
			for {
				err := mux.Run(mc)
				if errors.Is(err, mux.ErrReload) {
					log.Printf("mux reloading config (backoff %v)...", reloadBackoff)
					time.Sleep(reloadBackoff)
					if reloadBackoff < 30*time.Second {
						reloadBackoff *= 2
					}
					continue
				}
				if err != nil {
					log.Fatalf("mux: %v", err)
				}
				break
			}
		} else {
			// Daemon mode: re-exec self with --foreground, redirect output to log file.
			muxDaemon(home, image, muxHome, logFile)
		}
	case "stop":
		muxStop(pidFile)
	case "restart":
		// Stop if running, then start.
		if pid := readPID(pidFile); pid > 0 && processAlive(pid) {
			p, err := os.FindProcess(pid)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cannot find process %d: %v\n", pid, err)
				os.Exit(1)
			}
			if err := p.Signal(syscall.SIGTERM); err != nil {
				fmt.Fprintf(os.Stderr, "signal PID %d: %v\n", pid, err)
				os.Exit(1)
			}
			fmt.Printf("sent SIGTERM to mux (PID %d), waiting...\n", pid)
			// Wait for process to exit (up to 60s).
			stopped := false
			for i := 0; i < 300; i++ {
				if !processAlive(pid) {
					stopped = true
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if !stopped {
				// Force kill.
				fmt.Fprintln(os.Stderr, "mux did not stop gracefully, sending SIGKILL...")
				if kp, err := os.FindProcess(pid); err == nil {
					kp.Signal(syscall.SIGKILL)
				}
				time.Sleep(500 * time.Millisecond)
				removeWarn(filepath.Join(muxHome, "mux.pid"), "stale PID file")
			}
		}
		muxDaemon(home, image, muxHome, logFile)
	case "status", "":
		muxStatus(pidFile)
	case "logs":
		follow := false
		for _, a := range remaining[1:] {
			if a == "-f" || a == "--follow" {
				follow = true
			}
		}
		muxLogs(logFile, follow)
	case "config":
		muxConfig(cfgPath, remaining[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown mux command: %s\n", sub)
		fmt.Fprintln(os.Stderr, "usage: xnc mux [start|stop|restart|status|logs|config]")
		os.Exit(1)
	}
}

func muxDaemon(home, image, muxHome, logFile string) {
	if err := os.MkdirAll(muxHome, 0700); err != nil {
		log.Fatalf("create mux home: %v", err)
	}

	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.Fatalf("open log file: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("resolve executable: %v", err)
	}

	cmd := exec.Command(exe, "mux", "start", "--foreground", "--home", home, "--image", image)
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		lf.Close()
		log.Fatalf("start daemon: %v", err)
	}

	lf.Close()
	fmt.Printf("mux started (PID %d), logs: %s\n", cmd.Process.Pid, logFile)
}

func muxStop(pidFile string) {
	pid := readPID(pidFile)
	if pid <= 0 {
		fmt.Fprintln(os.Stderr, "mux is not running (no PID file)")
		os.Exit(1)
	}

	if !processAlive(pid) {
		fmt.Fprintf(os.Stderr, "mux process %d is not running (stale PID file)\n", pid)
		removeWarn(pidFile, "stale PID file")
		os.Exit(1)
	}

	p, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot find process %d: %v\n", pid, err)
		os.Exit(1)
	}

	if err := p.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "signal PID %d: %v\n", pid, err)
		os.Exit(1)
	}

	fmt.Printf("sent SIGTERM to mux (PID %d), waiting...\n", pid)

	// Wait up to 60s for graceful shutdown.
	for i := 0; i < 300; i++ {
		if !processAlive(pid) {
			fmt.Println("mux stopped")
			return
		}
		if i > 0 && i%25 == 0 {
			fmt.Printf("  still waiting (%ds)...\n", i/5)
		}
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Fprintln(os.Stderr, "mux did not stop gracefully, sending SIGKILL...")
	if kp, err := os.FindProcess(pid); err == nil {
		kp.Signal(syscall.SIGKILL)
	}
	time.Sleep(500 * time.Millisecond)
	removeWarn(pidFile, "PID file")
	fmt.Fprintln(os.Stderr, "mux force-killed")
	os.Exit(1)
}

func muxStatus(pidFile string) {
	pid := readPID(pidFile)
	if pid <= 0 {
		fmt.Println("mux: not running")
		return
	}
	if processAlive(pid) {
		fmt.Printf("mux: running (PID %d)\n", pid)
	} else {
		fmt.Printf("mux: stale PID file (PID %d not running)\n", pid)
		removeWarn(pidFile, "stale PID file")
	}
}

func muxLogs(logFile string, follow bool) {
	if !follow {
		f, err := os.Open(logFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open log: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		if _, err := io.Copy(os.Stdout, f); err != nil {
			return // stdout broken (pipe closed) — exit silently like cat
		}
		return
	}

	// Follow mode: tail -f equivalent.
	f, err := os.Open(logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open log: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	// Seek to end minus 4KB to show recent context.
	fi, err := f.Stat()
	if err == nil && fi.Size() > 4096 {
		f.Seek(-4096, io.SeekEnd)
		// Skip partial first line.
		r := bufio.NewReader(f)
		r.ReadString('\n')
		if _, err := io.Copy(os.Stdout, r); err != nil {
			return // stdout broken
		}
	} else {
		if _, err := io.Copy(os.Stdout, f); err != nil {
			return // stdout broken
		}
	}

	// Exit on Ctrl-C.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	buf := make([]byte, 4096)
	for {
		select {
		case <-sigCh:
			return
		default:
		}
		n, err := f.Read(buf)
		if n > 0 {
			if _, wErr := os.Stdout.Write(buf[:n]); wErr != nil {
				return // stdout broken (pipe closed)
			}
		}
		if n == 0 || err != nil {
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// PID file helpers.

// acquirePIDLock opens the PID file path with an exclusive advisory lock.
// Returns the open file (caller must keep it open for the process lifetime).
// Fails immediately (non-blocking) if another process holds the lock.
func acquirePIDLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	return f, nil
}

func writePID(path string, pid int) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0600); err != nil {
		log.Fatalf("write PID file: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		log.Fatalf("rename PID file: %v", err)
	}
}

func readPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

// removeWarn removes a file and logs a warning to stderr on failure.
// Ignores "not exist" errors (file already gone).
func removeWarn(path, desc string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", desc, err)
	}
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// runCLI dispatches CLI commands.
func runCLI(cmd string, args []string) {
	cli.Run(cmd, args)
}

func printDashboard() {
	home := agent.DefaultHome()

	fmt.Printf("xnc %s\n", version)
	fmt.Printf("home: %s\n", home)
	fmt.Println()

	if !agent.IsXNCHome(home) {
		fmt.Println("Not initialized. Run 'xnc init' to get started.")
		fmt.Println("Run 'xnc help' for full command reference.")
		return
	}

	// Agents.
	agents, _ := agent.ListAll(home)
	if len(agents) == 0 {
		fmt.Println("No agents configured.")
		fmt.Println()
		fmt.Println("  xnc init          Resume setup wizard")
		fmt.Println("  xnc setup <name>  Create an agent")
		fmt.Println("  xnc help          Full command reference")
		return
	}

	// Check provider keys.
	hasKeys := false
	for _, a := range agents {
		if agent.HasProviderKey(home, a.Name) {
			hasKeys = true
			break
		}
	}
	if !hasKeys {
		fmt.Printf("%d agent(s), but no API keys configured.\n", len(agents))
		fmt.Println()
		fmt.Println("  xnc init                              Resume setup wizard")
		fmt.Println("  xnc config set <agent> openai_key KEY  Set a key manually")
		fmt.Println("  xnc help                               Full command reference")
		return
	}

	// Count running containers.
	running := 0
	stopped := 0
	errored := 0

	// Try Docker — may not be available.
	stateMap := map[string]string{}
	dockerOK := false
	if dc, err := docker.NewClient(); err == nil {
		defer dc.Close()
		dockerOK = true
		prefix := agent.ContainerPrefix(home)
		containers, err := dc.ListContainers(context.Background(), prefix)
		if err == nil {
			for _, c := range containers {
				name := strings.TrimPrefix(c.Name, prefix)
				stateMap[name] = c.State
			}
		}
	}

	for _, a := range agents {
		canon := agent.CanonicalName(a.Name)
		state, hasContainer := stateMap[canon]
		if !hasContainer {
			stopped++
			continue
		}
		switch state {
		case "running":
			running++
		case "restarting", "dead":
			errored++
		case "exited":
			errored++ // could refine with exit code, but safe default
		default:
			stopped++
		}
	}

	// Show counts.
	fmt.Printf("agents: %d total", len(agents))
	if dockerOK {
		parts := []string{}
		if running > 0 {
			parts = append(parts, fmt.Sprintf("%d running", running))
		}
		if stopped > 0 {
			parts = append(parts, fmt.Sprintf("%d stopped", stopped))
		}
		if errored > 0 {
			parts = append(parts, fmt.Sprintf("%d error", errored))
		}
		if len(parts) > 0 {
			fmt.Printf(" (%s)", strings.Join(parts, ", "))
		}
	} else {
		fmt.Print(" (Docker not available)")
	}
	fmt.Println()

	// Mux status.
	muxHome := filepath.Join(home, "mux")
	pidFile := filepath.Join(muxHome, "mux.pid")
	pid := readPID(pidFile)
	if pid > 0 && processAlive(pid) {
		fmt.Printf("mux:    running (PID %d)\n", pid)
	} else {
		fmt.Println("mux:    not running")
	}

	fmt.Println()
	fmt.Println("  xnc status        Show all agents")
	fmt.Println("  xnc help          Full command reference")
}

func printUsage() {
	fmt.Printf("xnc %s — AI agent management\n", version)
	fmt.Print(`
BOOTSTRAP:
  init                                    Interactive setup wizard
    --openai-key KEY                        OpenAI API key
    --anthropic-key KEY                     Anthropic API key
    --openrouter-key KEY                    OpenRouter API key
    --brave-key KEY                         Brave Search API key
    --telegram-token TOKEN                  Telegram bot token
    --telegram-user USER                    Telegram username
    --model MODEL                           Default LLM model
    --agent-model MODEL                     Model for agents (if different)
    -n N, --agents N                        Number of agents to create
    --name NAME                             Agent name (repeatable)
    --mux                                   Enable Telegram bot setup
    --mux-name NAME                         Mux bot name (default: Mux)
    --owner NAME                            Your name (how agents address you)
    --group-id ID                           Telegram group chat ID
    --topic-id ID                           Forum topic ID (-1/0/1/N)
    --yes, -y                               Non-interactive mode

AGENT LIFECYCLE:
  setup    <names...>                      Create new agent(s)
    --openai-key KEY                        OpenAI API key
    --anthropic-key KEY                     Anthropic API key
    --openrouter-key KEY                    OpenRouter API key
    --brave-key KEY                         Brave Search API key
    --model MODEL                           LLM model
    --system-prompt TEXT                     Custom system prompt
  start    <agents...>                     Start agent containers
  stop     <agents...> [--all]             Stop agent containers
  restart  <agents...>                     Restart agent containers
  destroy  <agents...> [--yes]             Delete agents permanently
  clone    <source> <new> [--with-data]    Clone an agent
  rename   <old> <new>                     Rename an agent

AGENT INTERACTION:
  send     <agents...> [--all]             Pipe stdin message to agent(s)
  cli      <agent>                         Interactive chat session
  logs     <agent> [-f] [--tail N]         Container logs
  drain    <agent>                         Drain buffered output
  watch    <agent>                         Stream live output

FILE TRANSFER:
  cp-to    <agent> <host-file> [dest]      Copy file into agent container
  cp-from  <agent> <path> [host-dest]      Copy file out of agent container

AGENT CONFIG:
  config   get <agent> [key]               Read agent config
  config   set <agent> <key> <value>       Write agent config
  persona  <agent|mux> [--show] [--preset NAME] [--list-presets] [--reset] [--trait TEXT] [--warmth N] ...
  costs    <agent> [--today|--month|--json] Agent cost summary

SKILLS:
  skill    list [--agent NAME] [--all]     List installed skills
  skill    install <src> [--agent N] [--all] Install skill (dir/.zip/.md)
  skill    remove <name> [--agent N] [--all] Remove a skill
  skill    info <name> [--agent NAME]      Show skill details

FLEET:
  status   [agents...] [--running|--stopped|--error] [--json]
  list                                     Alias for: status
  running                                  Alias for: status --running

SNAPSHOTS:
  snapshot  <agent>                         Snapshot agent state to backup
  restore   <snapshot> [new-name]           Create agent from snapshot
  snapshots [--json]                        List all snapshots
  snapshot-delete <snapshot>                Delete a snapshot

MUX (Telegram bot):
  mux                                      Show mux status (same as mux status)
  mux      start [--foreground]            Start mux (default: daemon)
  mux      stop                            Stop mux daemon
  mux      restart                         Restart mux daemon
  mux      status                          Check mux status
  mux      logs [-f]                       Mux log output
  mux      config                          Show mux config (secrets redacted)
  mux      config get <key>                Get a config value
  mux      config set <key> <value>        Set a config value
  mux      config keys                     List all settable keys

IMAGE:
  image    build   [--from-source]         Pull image (or build from source)
  image    update  [--from-source]         Update image from registry/source
  image    status                          Show image info

OTHER:
  help                                     Show this help
  version                                  Show version

GLOBAL FLAGS:
  --home <path>     Override XNC_HOME (default: ~/.xnc)
  --image <name>    Override Docker image (default: nullclaw:latest)
  --json            JSON output where supported
  --quiet           Suppress informational output

ENVIRONMENT:
  XNC_HOME                  Data directory (default: ~/.xnc)
  XNC_IMAGE                 Docker image (default: nullclaw:latest)
  XNC_RUNTIME               Runtime mode: local (default), docker
  XNC_NETWORK               Docker network name for container mode
  XNC_TELEGRAM_BOT_TOKEN    Override Telegram bot token
  XNC_TELEGRAM_GROUP_ID     Override Telegram group ID
  XNC_TELEGRAM_TOPIC_ID     Override Telegram topic ID
  XNC_OPENAI_API_KEY        Override OpenAI API key
  XNC_OPENAI_MODEL          Override OpenAI model

DOCKER MODE:
  Run the mux inside a container with sibling agents on a shared network.
  Requires: nullclaw:latest image on the host (xnc image build).

    docker run -d --name xnc-mux \
      --user UID:DOCKER_GID \
      -v /var/run/docker.sock:/var/run/docker.sock \
      -v ~/.xnc:/xnc-home \
      -e XNC_HOME=/xnc-home \
      -e XNC_RUNTIME=docker \
      -e XNC_NETWORK=xnc-net \
      -e XNC_TELEGRAM_BOT_TOKEN=... \
      -e XNC_OPENAI_API_KEY=... \
      xnc-mux:latest
`)
}
