package main

import (
	"bufio"
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
	"github.com/jotavich/xnullclaw/internal/mux"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		// No args: if not initialized or incomplete, run init; otherwise show help.
		home := agent.DefaultHome()
		if !agent.SetupComplete(home) {
			if agent.IsXNCHome(home) {
				fmt.Println("Incomplete xnc setup detected. Resuming setup...")
			} else {
				fmt.Println("No xnc setup found. Starting guided setup...")
			}
			fmt.Println()
			runInit(nil)
			return
		}
		printUsage()
		os.Exit(1)
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
	image := defaultImage()
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

	muxHome := filepath.Join(home, ".mux")
	pidFile := filepath.Join(muxHome, "mux.pid")
	logFile := filepath.Join(muxHome, "mux.log")

	// Subcommand dispatch.
	sub := ""
	if len(remaining) > 0 {
		sub = remaining[0]
	}

	switch sub {
	case "stop":
		muxStop(pidFile)
	case "status":
		muxStatus(pidFile)
	case "logs":
		follow := false
		for _, a := range remaining[1:] {
			if a == "-f" || a == "--follow" {
				follow = true
			}
		}
		muxLogs(logFile, follow)
	default:
		// Start mux.
		if foreground || sub == "" {
			// Check if already running.
			if pid := readPID(pidFile); pid > 0 && processAlive(pid) {
				fmt.Fprintf(os.Stderr, "mux already running (PID %d)\n", pid)
				os.Exit(1)
			}
		}

		if foreground {
			// Run in foreground — write PID file for status/stop.
			if err := os.MkdirAll(muxHome, 0755); err != nil {
				log.Fatalf("create mux home: %v", err)
			}
			writePID(pidFile, os.Getpid())
			defer os.Remove(pidFile)

			cfgPath := filepath.Join(muxHome, "config.json")
			if err := mux.Run(mux.Config{
				Home:    home,
				CfgPath: cfgPath,
				Image:   image,
				Version: version,
			}); err != nil {
				log.Fatalf("mux: %v", err)
			}
		} else {
			// Daemon mode: re-exec self with --foreground, redirect output to log file.
			muxDaemon(home, image, muxHome, logFile)
		}
	}
}

func muxDaemon(home, image, muxHome, logFile string) {
	if err := os.MkdirAll(muxHome, 0755); err != nil {
		log.Fatalf("create mux home: %v", err)
	}

	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("open log file: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("resolve executable: %v", err)
	}

	cmd := exec.Command(exe, "mux", "--foreground", "--home", home, "--image", image)
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

	p, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot find process %d: %v\n", pid, err)
		os.Exit(1)
	}

	if err := p.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "signal PID %d: %v\n", pid, err)
		os.Exit(1)
	}

	fmt.Printf("sent SIGTERM to mux (PID %d)\n", pid)
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
		os.Remove(pidFile)
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
		io.Copy(os.Stdout, f)
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
	fi, _ := f.Stat()
	if fi.Size() > 4096 {
		f.Seek(-4096, io.SeekEnd)
		// Skip partial first line.
		r := bufio.NewReader(f)
		r.ReadString('\n')
		io.Copy(os.Stdout, r)
	} else {
		io.Copy(os.Stdout, f)
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
			os.Stdout.Write(buf[:n])
		}
		if err != nil {
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// PID file helpers.

func writePID(path string, pid int) {
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0644); err != nil {
		log.Fatalf("write PID file: %v", err)
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

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
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

// runCLI dispatches CLI commands.
func runCLI(cmd string, args []string) {
	cli.Run(cmd, args)
}

func printUsage() {
	fmt.Print(`xnc — agent management

BOOTSTRAP:
  init     [flags]                          Set up everything from scratch

AGENT LIFECYCLE:
  setup    <name>                          Create a new agent
  start    <agents...> [--port N]          Start agent containers
  stop     <agents...> [--all]             Stop agent containers
  restart  <agents...> [--port N]          Restart agent containers
  destroy  <agents...> [--yes]             Delete agents permanently
  clone    <source> <new> [--with-data]    Clone an agent

AGENT INTERACTION:
  send     <agents...> [--all]             Send stdin message to agent(s)
  cli      <agent> [args...]               Interactive chat session
  logs     <agent> [-f] [--tail N]         Container logs
  drain    <agent>                         Drain buffered output
  watch    <agent>                         Stream live output

FILE TRANSFER:
  cp-to    <agent> <host-file> [dest]      Copy file into agent container
  cp-from  <agent> <path> [host-dest]      Copy file out of agent container

AGENT CONFIG:
  config   get <agent> [key]               Read agent config
  config   set <agent> <key> <value>       Write agent config
  costs    <agent> [--today|--month|--json] Agent cost summary

FLEET:
  list     [--json]                        List all agents
  running  [--json]                        List running agents
  status   <agents...> [--json]            Show agent status

SNAPSHOTS:
  snapshot  <agent>                         Snapshot agent state to backup
  restore   <snapshot> [new-name]           Create agent from snapshot
  snapshots [--json]                        List all snapshots
  snapshot-delete <snapshot>                Delete a snapshot

MUX (Telegram bot):
  mux      [--foreground]                  Start mux (default: daemon)
  mux      stop                            Stop mux daemon
  mux      status                          Check mux status
  mux      logs [-f]                       Mux log output

IMAGE:
  image    build   [--from-source]         Pull image (or build from source)
  image    update  [--from-source]         Update image from registry/source
  image    status                          Show image info

OTHER:
  help                                     Show help
  version                                  Show version

GLOBAL FLAGS:
  --home <path>     Override XNC_HOME (default: ~/.xnc)
  --image <name>    Override Docker image (default: nullclaw:latest)
  --json            JSON output where supported
  --quiet           Suppress informational output
`)
}
