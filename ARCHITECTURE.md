# xnullclaw Unified Binary — Architecture Document

**Date:** 2026-03-10
**Status:** PLAN (pre-implementation)

---

## 0. Executive Summary

Replace the two-binary system (`xnc` bash wrapper + `xnc-mux` Go binary) with a
single Go binary named `xnullclaw`. The binary serves two modes: CLI mode
(replaces the bash wrapper) and mux mode (replaces xnc-mux). All Docker
operations use the Docker SDK instead of shelling out. The only external runtime
dependency is a running Docker daemon.

**CLI syntax change:** Commands come first, agents are arguments. Multiple agents
supported where it makes sense.

```
xnullclaw start alice bob     # start two agents
xnullclaw stop --all          # stop all running agents
xnullclaw list                # list all agents
xnullclaw mux                 # start mux (Telegram bot)
```

---

## A. Full Feature Catalog

### A.1 Bash Wrapper Features (xnc) → CLI Commands

| # | Current (old syntax) | New CLI Syntax | Go Package |
|---|---|---|---|
| 1 | `xnc <agent> setup` | `xnullclaw setup <name>` | `cli` + `agent` |
| 2 | `xnc <agent> start` | `xnullclaw start <agents...>` | `cli` + `docker` |
| 3 | `xnc <agent> start --port [N]` | `xnullclaw start <agent> --port [N]` | `cli` + `docker` |
| 4 | `xnc <agent> start --mux` | `xnullclaw start <agents...> --mux` | `cli` + `docker` |
| 5 | `xnc <agent> stop` | `xnullclaw stop <agents...>` or `--all` | `cli` + `docker` |
| 6 | `xnc <agent> restart` | `xnullclaw restart <agents...>` | `cli` + `docker` |
| 7 | `xnc <agent> status [--json]` | `xnullclaw status <agents...> [--json]` | `cli` + `docker` |
| 8 | `xnc <agent> logs [-f]` | `xnullclaw logs <agent> [-f] [--tail N]` | `cli` + `docker` |
| 9 | `xnc <agent> cli [args]` | `xnullclaw cli <agent> [args...]` | `cli` + `docker` |
| 10 | `xnc <agent> clone <src>` | `xnullclaw clone <src> <new> [--with-data]` | `cli` + `agent` |
| 11 | `xnc <agent> destroy [--yes]` | `xnullclaw destroy <agents...> [--yes]` | `cli` + `docker` + `agent` |
| 12 | `xnc <agent> send` (stdin) | `xnullclaw send <agent>` (stdin) | `cli` + `docker` |
| 13 | `xnc send-all` (stdin) | `xnullclaw send --all` (stdin) | `cli` + `docker` |
| 14 | `xnc send-some a,b` (stdin) | `xnullclaw send <agents...>` (stdin) | `cli` + `docker` |
| 15 | `xnc <agent> config get [key]` | `xnullclaw config get <agent> [key]` | `cli` + `agent` |
| 16 | `xnc <agent> config set <k> <v>` | `xnullclaw config set <agent> <key> <val>` | `cli` + `agent` |
| 17 | `xnc <agent> costs [--today]` | `xnullclaw costs <agent> [--today\|--month]` | `cli` + `agent` |
| 18 | `xnc <agent> drain` | `xnullclaw drain <agent>` | `cli` + `docker` |
| 19 | `xnc <agent> watch` | `xnullclaw watch <agent>` | `cli` + `docker` |
| 20 | `xnc list [--json]` | `xnullclaw list [--json]` | `cli` + `agent` |
| 21 | `xnc running [--json]` | `xnullclaw running [--json]` | `cli` + `docker` |
| 22 | `xnc mux start` | `xnullclaw mux start` | `cli` + `mux` |
| 23 | `xnc mux stop` | `xnullclaw mux stop` | `cli` |
| 24 | `xnc mux status` | `xnullclaw mux status` | `cli` |
| 25 | `xnc mux logs [-f]` | `xnullclaw mux logs [-f]` | `cli` |
| 26 | `xnc image build` | `xnullclaw image build` | `cli` + `docker` |
| 27 | `xnc image update` | `xnullclaw image update` | `cli` + `docker` |
| 28 | `xnc image status` | `xnullclaw image status` | `cli` + `docker` |
| 29 | `xnc help` | `xnullclaw help` or `--help` | `cli` |
| 30 | — | `xnullclaw version` | `cli` |
| 31 | Dependency check (docker,jq,curl,git) | Eliminated (Docker SDK, no jq/curl) | — |
| 32 | Config generation (heredoc template) | `agent/config` | `agent` |
| 33 | Emoji pool (40 emojis) | `agent/identity` | `agent` |
| 34 | Port allocation (scan .meta) | `agent/identity` | `agent` |
| 35 | .meta file management | `agent/meta` | `agent` |
| 36 | Agent name validation | `agent` | `agent` |
| 37 | Docker hardening args | `docker/hardening` | `docker` |
| 38 | macOS/Linux compat (BSD vs GNU) | Eliminated (Go stdlib) | — |
| 39 | XNC_HOME env override | `agent` | `agent` |
| 40 | XNC_IMAGE env override | `docker` | `docker` |
| 41 | Debug logging | `logging` | `logging` |

### A.2 Mux Bot Features (xnc-mux) → Mux Mode

| # | Feature | Current File | Target Package |
|---|---|---|---|
| 42 | OpenAI chat completions adapter | `main.go` | `internal/llm` |
| 43 | Agentic tool-calling loop | `loop/loop.go` | `internal/loop` |
| 44 | Telegram long-poll bot | `bot/bot.go` | `internal/telegram` |
| 45 | Telegram text handler | `main.go` | `internal/mux` |
| 46 | Telegram voice handler (Whisper) | `main.go` | `internal/mux` |
| 47 | Telegram media handler (photos/docs) | `main.go` | `internal/mux` |
| 48 | Telegram command handler | `main.go` | `internal/mux` |
| 49 | /help, /agents, /stats, /costs, /clear | `main.go` | `internal/mux` |
| 50 | Auto-start agents on boot | `main.go` | `internal/mux` |
| 51 | Auto-stop agents on shutdown | `main.go` | `internal/mux` |
| 52 | Media marker parsing | `media/media.go` | `internal/media` |
| 53 | Media attachment sending | `main.go` | `internal/mux` |
| 54 | Whisper transcription | `voice/voice.go` | `internal/voice` |
| 55 | TTS synthesis | `voice/voice.go` | `internal/voice` |
| 56 | SQLite memory store | `memory/store.go` | `internal/memory` |
| 57 | Context assembly | `memory/context.go` | `internal/memory` |
| 58 | System prompt builder | `prompt/prompt.go` | `internal/prompt` |
| 59 | Structured logging | `logging/logging.go` | `internal/logging` |
| 60 | Mux config (JSON load/save) | `config/config.go` | `internal/config` |

### A.3 Mux Tool Registry (33 tools)

| # | Tool Name | Target Package | Notes |
|---|---|---|---|
| 61 | `send_to_agent` | `internal/tools` | Direct `docker.ExecSync()` call |
| 62 | `send_to_all` | `internal/tools` | Parallel exec on all running |
| 63 | `send_to_some` | `internal/tools` | Parallel exec on named subset |
| 64 | `list_agents` | `internal/tools` | Direct `agent.ListAll()` call |
| 65 | `agent_status` | `internal/tools` | Direct `docker.Inspect()` call |
| 66 | `start_agent` | `internal/tools` | Direct `docker.StartContainer()` |
| 67 | `stop_agent` | `internal/tools` | Direct `docker.StopContainer()` |
| 68 | `restart_agent` | `internal/tools` | Stop + start |
| 69 | `destroy_agent` | `internal/tools` | Two-step confirm, full cleanup |
| 70 | `clone_agent` | `internal/tools` | Direct `agent.Clone()` |
| 71 | `provision_agent` | `internal/tools` | Setup+config+start+hello |
| 72 | `get_agent_config` | `internal/tools` | Direct `agent.ConfigGet()` |
| 73 | `update_agent_config` | `internal/tools` | Direct `agent.ConfigSet()` |
| 74 | `get_agent_persona` | `internal/tools` | SQLite read |
| 75 | `update_agent_persona` | `internal/tools` | SQLite write + regen prompt |
| 76 | `send_file_to_agent` | `internal/tools` | Direct `docker.CopyTo()` |
| 77 | `get_agent_file` | `internal/tools` | Direct `docker.CopyFrom()` |
| 78 | `list_agent_files` | `internal/tools` | Direct `docker.ExecSync(ls)` |
| 79 | `remember` | `internal/tools` | SQLite store |
| 80 | `recall` | `internal/tools` | SQLite search |
| 81 | `get_conversation_summary` | `internal/tools` | SQLite read |
| 82 | `set_persona` | `internal/tools` | Config mutate + save |
| 83 | `set_persona_dimension` | `internal/tools` | Config mutate + save |
| 84 | `get_persona` | `internal/tools` | Config read |
| 85 | `reset_persona` | `internal/tools` | Config reset + save |
| 86 | `apply_persona_preset` | `internal/tools` | Config preset + save |
| 87 | `list_voices` | `internal/tools` | Static list |
| 88 | `get_costs` | `internal/tools` | SQLite + wrapper costs |
| 89 | `get_agent_costs` | `internal/tools` | Agent costs.jsonl |
| 90 | `set_budget` | `internal/tools` | Config mutate + save |
| 91 | `set_passthrough_rule` | `internal/tools` | SQLite |
| 92 | `remove_passthrough_rule` | `internal/tools` | SQLite |
| 93 | `list_passthrough_rules` | `internal/tools` | SQLite |

**Total: 93 distinct features/commands/tools.** Every one has a home.

---

## B. Package Layout

```
xnullclaw/                          # Repository root
├── main.go                         # Entry point: CLI vs mux dispatch
├── go.mod                          # Module: github.com/jotavich/xnullclaw
├── go.sum
├── Makefile
│
├── internal/
│   ├── agent/                      # Agent directory management (pure Go, no Docker)
│   │   ├── agent.go                # Agent struct, Exists(), Dir(), ValidateName()
│   │   ├── setup.go                # Setup (create dirs, generate config, assign emoji)
│   │   ├── clone.go                # Clone logic (config copy, optional data copy)
│   │   ├── destroy.go              # Remove agent directory from disk
│   │   ├── meta.go                 # .meta file read/write
│   │   ├── identity.go             # Emoji pool, port allocation, name pool
│   │   ├── configkeys.go           # Friendly key → JSON path mapping, typed get/set
│   │   ├── costs.go                # Parse costs.jsonl from agent data dir
│   │   └── *_test.go
│   │
│   ├── docker/                     # Docker SDK facade (ONLY package that imports Docker SDK)
│   │   ├── ops.go                  # Ops interface (for mocking)
│   │   ├── client.go               # Client struct wrapping Docker SDK
│   │   ├── container.go            # Create, Start, Stop, Remove, Inspect, List
│   │   ├── exec.go                 # ExecSync (stdin/stdout pipe)
│   │   ├── copy.go                 # CopyToContainer, CopyFromContainer
│   │   ├── logs.go                 # ContainerLogs (stream, follow, tail)
│   │   ├── image.go                # ImageBuild, ImageInspect
│   │   ├── hardening.go            # HardenedContainerConfig() security flags
│   │   └── *_test.go
│   │
│   ├── cli/                        # CLI command dispatch
│   │   ├── root.go                 # Root dispatch, global flags (--home, --image, --json)
│   │   ├── agent_cmds.go           # setup, start, stop, restart, status, destroy
│   │   ├── comm_cmds.go            # send, cli, logs, drain, watch
│   │   ├── config_cmds.go          # config get, config set
│   │   ├── list_cmds.go            # list, running
│   │   ├── clone_cmds.go           # clone
│   │   ├── costs_cmds.go           # costs
│   │   ├── image_cmds.go           # image build/update/status
│   │   ├── mux_mgmt.go            # mux start/stop/status/logs
│   │   ├── help.go                 # Full help text
│   │   └── output.go               # Human vs JSON formatting helpers
│   │
│   ├── mux/                        # Mux mode: Telegram + LLM loop wiring
│   │   ├── mux.go                  # Mux struct, Run() entry point
│   │   ├── handlers.go             # onMessage, onVoice, onMedia, onCommand
│   │   ├── attachments.go          # sendAttachment logic
│   │   └── mux_test.go
│   │
│   ├── loop/                       # Agentic tool-calling loop
│   │   ├── loop.go
│   │   ├── types.go
│   │   ├── cost.go
│   │   └── loop_test.go
│   │
│   ├── tools/                      # Mux tool registry + implementations
│   │   ├── registry.go
│   │   ├── agents.go               # Agent lifecycle + persona tools
│   │   ├── files.go                # File transfer tools (uses docker.Ops)
│   │   ├── memory_tools.go         # remember, recall, summary
│   │   ├── persona.go              # Mux persona tools + presets + voices
│   │   ├── costs.go                # Cost + budget tools
│   │   ├── passthrough.go          # Passthrough rules
│   │   ├── helpers.go              # Arg extraction helpers
│   │   └── tools_test.go
│   │
│   ├── telegram/                   # Telegram bot
│   │   ├── bot.go                  # Bot struct, Start, Stop, long-poll
│   │   ├── send.go                 # Send, SendPhoto, SendDocument, SendVoice, etc.
│   │   ├── download.go             # DownloadFile
│   │   ├── split.go                # SplitMessage
│   │   ├── command.go              # ParseCommand
│   │   └── bot_test.go
│   │
│   ├── llm/                        # LLM client abstraction
│   │   ├── openai.go               # OpenAI HTTP adapter
│   │   └── openai_test.go
│   │
│   ├── memory/                     # SQLite persistent memory
│   │   ├── store.go
│   │   ├── context.go
│   │   ├── types.go
│   │   └── store_test.go
│   │
│   ├── prompt/                     # System prompt builder
│   │   ├── prompt.go
│   │   └── prompt_test.go
│   │
│   ├── voice/                      # Whisper + TTS
│   │   ├── transcribe.go
│   │   ├── synthesize.go
│   │   └── voice_test.go
│   │
│   ├── media/                      # Media marker parser
│   │   ├── media.go
│   │   └── media_test.go
│   │
│   ├── config/                     # Mux config types + load/save
│   │   └── config.go
│   │
│   └── logging/                    # Structured JSON logging
│       ├── logging.go
│       └── logging_test.go
│
├── mux/                            # OLD mux code (reference during migration, removed in Phase 7)
├── xnc                             # OLD bash wrapper (reference, removed in Phase 7)
└── build/                          # Build output
```

### B.1 Key Architectural Decisions

1. **`internal/` convention**: All packages under `internal/` — single-binary project.

2. **`docker` package is a facade**: Only package that imports Docker SDK. Exposes
   `Ops` interface for mocking. No other package touches Docker SDK directly.

3. **`agent` package is Docker-agnostic**: Handles disk operations only (dirs, config,
   meta). Docker operations composed at call sites (CLI commands, tools).

4. **Tools call Go functions directly**: No more `runWrapper()` subprocess spawning.
   Tools import `docker` and `agent` packages and call functions.

5. **No cobra**: Hand-rolled dispatcher. The CLI is simple enough — one command tree
   with a flat structure. Avoids a heavy dependency.

6. **Pure Go SQLite**: Use `modernc.org/sqlite` instead of `mattn/go-sqlite3` to
   eliminate CGO requirement. Enables trivial cross-compilation.

---

## C. Data Model

### C.1 Agent Directory Layout

```
~/.xnc/                             # XNC_HOME (configurable via env)
├── .xnc.log                        # Global debug log
├── .tmp/
│   └── nullclaw/                   # Git clone for image building
├── .mux/                           # Mux state directory
│   ├── config.json                 # Mux config
│   ├── memory.db                   # SQLite
│   ├── mux.pid                     # PID file (daemon mode)
│   ├── media_tmp/                  # Temp media downloads
│   └── logs/
│       ├── mux.log
│       ├── agents.log
│       └── costs.log
│
└── <agent>/                        # Per-agent directory
    ├── .meta                       # Key=value metadata
    ├── config.json                 # Nullclaw agent config
    └── data/                       # Mounted as /nullclaw-data in container
```

Zero data migration. Reads/writes identical files.

### C.2 .meta File Format

```
CREATED=2026-03-10T02:22:51Z
EMOJI=🍎
MUX_MODE=true
HOST_PORT=3001
AUTH_TOKEN=zc_<hex>
CLONED_FROM=alice
LAST_DRAIN=2026-03-10T12:00:00Z
```

### C.3 Config Key Aliases (agent config.json)

| Friendly Key | JSON Path | Type |
|---|---|---|
| `model` | `agents.defaults.model.primary` | string |
| `temperature` | `default_temperature` | float |
| `autonomy` | `autonomy.level` | string |
| `system_prompt` | `agents.defaults.system_prompt` | string |
| `max_actions_per_hour` | `autonomy.max_actions_per_hour` | int |
| `openai_key` | `models.providers.openai.api_key` | string |
| `anthropic_key` | `models.providers.anthropic.api_key` | string |
| `openrouter_key` | `models.providers.openrouter.api_key` | string |
| `telegram_token` | `channels.telegram.accounts.main.bot_token` | string |
| `telegram_allow_from` | `channels.telegram.accounts.main.allow_from` | string_array |
| `http_enabled` | `http_request.enabled` | bool |
| `http_timeout` | `http_request.timeout_secs` | int |
| `http_max_response` | `http_request.max_response_size` | int |
| `http_allowed_domains` | `http_request.allowed_domains` | string_array |
| `search_provider` | `http_request.search_provider` | string |

### C.4 SQLite Schema (unchanged)

Six tables: `messages`, `facts`, `agent_state`, `compactions`, `costs`, `agent_persona`.
Identical to current `mux/memory/store.go` schema.

---

## D. Docker Abstraction Layer

### D.1 Ops Interface

```go
type Ops interface {
    IsRunning(ctx context.Context, name string) (bool, error)
    StartContainer(ctx context.Context, name string, opts ContainerOpts) error
    StopContainer(ctx context.Context, name string) error
    RemoveContainer(ctx context.Context, name string, force bool) error
    InspectContainer(ctx context.Context, name string) (*ContainerInfo, error)
    ListContainers(ctx context.Context, prefix string) ([]ContainerInfo, error)
    ContainerLogs(ctx context.Context, name string, opts LogOpts) (io.ReadCloser, error)
    ExecSync(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error)
    CopyToContainer(ctx context.Context, name, destPath string, content io.Reader) error
    CopyFromContainer(ctx context.Context, name, srcPath string) (io.ReadCloser, error)
    ImageExists(ctx context.Context, image string) (bool, error)
    ImageInspect(ctx context.Context, image string) (*ImageInfo, error)
    ImageBuild(ctx context.Context, contextDir string, opts BuildOpts) error
    AttachInteractive(ctx context.Context, name string) error
}
```

### D.2 Operation Mapping

| Bash Operation | Docker CLI | Docker SDK Equivalent |
|---|---|---|
| Check running | `docker ps -q -f name=^xnc-X$` | `cli.ContainerList(ctx, opts)` |
| Check image | `docker image inspect <img>` | `cli.ImageInspectWithRaw(ctx, img)` |
| Start container | `docker run -d --name ... <flags>` | `ContainerCreate()` + `ContainerStart()` |
| Stop container | `docker stop <name>` | `ContainerStop(ctx, id, &timeout)` |
| Remove container | `docker rm [-f] <name>` | `ContainerRemove(ctx, id, opts)` |
| Inspect | `docker inspect <name>` | `ContainerInspect(ctx, id)` |
| Logs | `docker logs [--follow] [--tail]` | `ContainerLogs(ctx, id, opts)` |
| Exec (send msg) | `echo msg \| docker exec -i X nullclaw agent -s mux` | `ContainerExecCreate()` + `ExecAttach()` |
| Copy into | `docker cp host:file X:/path` | `CopyToContainer(ctx, id, path, tar)` |
| Copy out | `docker cp X:/path host:file` | `CopyFromContainer(ctx, id, path)` |
| Build image | `docker build -t name dir` | `ImageBuild(ctx, tar, opts)` |
| Interactive CLI | `docker run --rm -it ... agent` | `Create + Start + AttachRaw(stdin/stdout/tty)` |

### D.3 Hardened Container Config

```go
func HardenedConfig(agentDir, image string, cmd []string) (*container.Config, *container.HostConfig) {
    // read-only rootfs, cap-drop ALL, no-new-privileges
    // tmpfs /tmp:size=64M,noexec,nosuid
    // 128MB memory, 0.25 CPU, 64 PIDs
    // Binds: data:/nullclaw-data, config.json:ro
    // User: current uid:gid
    // RestartPolicy: unless-stopped
}
```

Port exposure, mux mode, and interactive mode modify the returned configs before
passing to `ContainerCreate`.

---

## E. CLI Command Tree

```
xnullclaw <command> [args...] [flags]

AGENT LIFECYCLE:
  setup    <name>                          Create a new agent
  start    <agents...> [--port N] [--mux]  Start agent containers
  stop     <agents...> [--all]             Stop agent containers
  restart  <agents...> [--port N] [--mux]  Restart agent containers
  destroy  <agents...> [--yes]             Delete agents permanently
  clone    <source> <new> [--with-data]    Clone an agent

AGENT INTERACTION:
  send     <agents...> [--all]             Send stdin message to agent(s)
  cli      <agent> [args...]               Interactive chat session
  logs     <agent> [-f] [--tail N]         Container logs
  drain    <agent>                         Drain buffered output
  watch    <agent>                         Stream live output

AGENT CONFIG:
  config   get <agent> [key]               Read agent config
  config   set <agent> <key> <value>       Write agent config
  costs    <agent> [--today|--month|--json] Agent cost summary

FLEET:
  list     [--json]                        List all agents
  running  [--json]                        List running agents
  status   <agents...> [--json]            Show agent status

MUX (Telegram bot):
  mux      [--foreground]                  Start mux (default: daemon)
  mux      stop                            Stop mux daemon
  mux      status                          Check mux status
  mux      logs [-f]                       Mux log output

IMAGE:
  image    build                           Build nullclaw Docker image
  image    update                          Rebuild from latest source
  image    status                          Show image info

OTHER:
  help                                     Show help
  version                                  Show version

GLOBAL FLAGS:
  --home <path>     Override XNC_HOME (default: ~/.xnc)
  --image <name>    Override Docker image (default: nullclaw:latest)
  --json            JSON output where supported
  --quiet           Suppress informational output
```

### E.1 Multi-Agent Support

Commands that accept `<agents...>` operate on multiple agents in parallel:

```bash
xnullclaw start alice bob charlie    # start 3 agents concurrently
xnullclaw stop alice bob             # stop 2 agents
xnullclaw stop --all                 # stop all running
xnullclaw send alice bob             # send stdin to 2 agents, merge responses
xnullclaw send --all                 # broadcast to all running
xnullclaw destroy alice bob --yes    # destroy 2 agents
xnullclaw status alice bob --json    # status of 2 agents
```

Commands that require exactly one agent: `setup`, `clone`, `cli`, `logs`,
`drain`, `watch`, `config`, `costs`.

---

## F. Mux Mode Architecture

### F.1 Startup Sequence

When `xnullclaw mux` is invoked:

1. Load mux config from `~/.xnc/.mux/config.json`
2. Open SQLite memory store
3. Create Docker client (same `internal/docker` used by CLI)
4. Create tool registry — tools call Go functions directly
5. Create OpenAI adapter
6. Create agentic loop
7. Create Telegram bot
8. Wire message/voice/media/command handlers
9. Auto-start agents (direct `docker.StartContainer()`)
10. Start Telegram long-polling
11. Wait for shutdown signal
12. Auto-stop managed agents, send goodbye, close DB

### F.2 No Wrapper Subprocess

| Current (shells out) | Unified (direct call) |
|---|---|
| `runWrapper(ctx, w, agent, "start", "--mux")` | `docker.StartContainer(ctx, ...)` |
| `runWrapper(ctx, w, agent, "stop")` | `docker.StopContainer(ctx, ...)` |
| `runWrapper(ctx, w, "list", "--json")` | `agent.ListAll(home)` |
| `runWrapper(ctx, w, agent, "config", "set", k, v)` | `agent.ConfigSet(dir, k, v)` |
| `runWrapperWithStdin(ctx, w, msg, agent, "send")` | `docker.ExecSync(ctx, ...)` |
| `exec.Command("docker", "cp", ...)` | `docker.CopyToContainer(ctx, ...)` |

---

## G. Migration Strategy

### G.1 Coexistence

Old `xnc` + `xnc-mux` remain functional throughout development. The new
`xnullclaw` binary is built alongside them. Same data formats = zero risk.

### G.2 Verification

| Step | How to Verify |
|---|---|
| CLI parity | Run same operations with both `xnc` and `xnullclaw`, compare results |
| Mux parity | Run `xnullclaw mux` with existing config, same Telegram behavior |
| Drop-in | Symlink `xnc` → `xnullclaw`, everything works |
| Cleanup | Remove old bash wrapper and `mux/` directory from repo |

---

## H. Implementation Phases

### Phase 1: Foundation

**Goal:** Binary compiles. Docker SDK + agent packages solid. No CLI yet.

**Deliverables:**
- `main.go` with CLI/mux dispatch skeleton
- `internal/docker/` — full Docker SDK wrapper with hardening, `Ops` interface
- `internal/agent/` — dir management, meta, identity, configkeys, name validation
- `go.mod` with Docker SDK + pure-Go SQLite (`modernc.org/sqlite`)

**Tests:** Hardening config, meta read/write, config key mapping, name validation.

**Milestone:** `go build` works, `go test ./internal/agent/... ./internal/docker/...` passes.

### Phase 2: Core CLI

**Goal:** Agent lifecycle from command line.

**Deliverables:**
- `internal/cli/` — root dispatch + global flags
- Commands: `setup`, `start`, `stop`, `restart`, `status`, `list`, `running`, `destroy`, `help`, `version`
- Multi-agent support for start/stop/restart/destroy/status

**Tests:** Integration tests with Docker (setup → start → status → stop → destroy).

**Milestone:** Can fully manage agent lifecycle. `xnullclaw list` matches `xnc list`.

### Phase 3: Agent Communication + Config

**Goal:** Full CLI parity with bash wrapper.

**Deliverables:**
- `send` (single, multi, --all), `cli` (interactive TTY), `logs`, `drain`, `watch`
- `config get`, `config set`
- `clone`, `costs`

**Tests:** Docker exec integration, config round-trip, parallel send.

**Milestone:** Complete CLI parity. Bash wrapper no longer needed for daily use.

### Phase 4: Image Management

**Goal:** Build/update Docker images.

**Deliverables:**
- `image build` (git clone + Docker SDK build)
- `image update` (git pull + no-cache build)
- `image status`

**Note:** `git` is the one external dependency, only for image management.

**Milestone:** Full CLI replacement.

### Phase 5: Mux Mode — Core

**Goal:** Mux starts, handles Telegram messages, calls tools via Go functions.

**Deliverables:**
- Move existing mux packages into `internal/` (loop, telegram, memory, prompt, voice, media, config, logging, llm, tools)
- `internal/mux/mux.go` — startup wiring
- Rewire all tools to use `docker.Ops` and `agent` packages directly
- `xnullclaw mux --foreground` runs the bot

**Tests:** Mux startup with mock Telegram + mock LLM + mock Docker.

**Milestone:** Mux runs, answers messages, manages agents. Old `xnc-mux` no longer needed.

### Phase 6: Mux Process Management

**Goal:** Daemon mode for mux.

**Deliverables:**
- `xnullclaw mux` (default: daemonize via fork+exec self with `--foreground`)
- `xnullclaw mux stop` (SIGTERM by PID)
- `xnullclaw mux status` (PID file check)
- `xnullclaw mux logs` (tail log file)

**Milestone:** Full replacement. Both old binaries can be deleted.

### Phase 7: Polish + Cleanup

**Deliverables:**
- Remove old `mux/` directory, `xnc` wrapper, `build/xnc`, `build/xnc-mux`
- Makefile with cross-compilation
- `make install` (symlinks `xnc` → `xnullclaw`)
- Update MEMORY.md

**Milestone:** Single binary in production.

---

## I. Build & Distribution

### I.1 Module

```
module github.com/jotavich/xnullclaw

go 1.24

require (
    github.com/docker/docker             v27.x
    github.com/docker/go-connections
    github.com/go-telegram-bot-api/telegram-bot-api/v5
    modernc.org/sqlite                              // pure Go, no CGO
)
```

### I.2 Cross-Compilation

With `modernc.org/sqlite` (pure Go), cross-compilation is trivial:

```bash
GOOS=linux  GOARCH=amd64 go build -o xnullclaw-linux-amd64
GOOS=linux  GOARCH=arm64 go build -o xnullclaw-linux-arm64
GOOS=darwin GOARCH=amd64 go build -o xnullclaw-darwin-amd64
GOOS=darwin GOARCH=arm64 go build -o xnullclaw-darwin-arm64
```

No CGO, no cross-compiler toolchains needed.

### I.3 Versioning

- Git tags: `v0.1.0`, `v0.2.0`, etc.
- `main.version` injected via `-ldflags -X main.version=$(VERSION)`
- `xnullclaw version` prints the version string

---

## J. Testing Strategy

### J.1 Unit Tests (no Docker)

| Package | What |
|---|---|
| `agent/meta` | Read/write .meta, key update, missing file |
| `agent/identity` | Emoji dedup, port allocation, name generation |
| `agent/configkeys` | Key alias → JSON path, typed get/set |
| `agent` | Name validation, Dir(), Exists() |
| `docker/hardening` | Security flags match expected config |
| `loop` | Agentic loop with mock ChatClient |
| `telegram/split` | Message splitting, code block awareness |
| `telegram/command` | ParseCommand variants |
| `memory` | SQLite CRUD with `:memory:` |
| `prompt` | Prompt builder with known inputs |
| `media` | Marker parsing |
| `config` | Load/Save/Defaults round-trip |
| `tools` | Arg extraction helpers |

### J.2 Integration Tests (`//go:build integration`)

| Test | What |
|---|---|
| `TestContainerLifecycle` | Create → start → inspect → stop → remove |
| `TestContainerExec` | Exec command, capture output |
| `TestContainerCopy` | Copy file in and out |
| `TestHardenedContainer` | Verify caps/readonly/limits |
| `TestAgentFullLifecycle` | Setup → start → send → stop → destroy |

### J.3 Mock Infrastructure

```go
// internal/docker/ops.go
type Ops interface { ... }

// internal/docker/mock.go
type MockOps struct {
    StartContainerFn func(ctx context.Context, ...) error
    StopContainerFn  func(ctx context.Context, ...) error
    ExecSyncFn       func(ctx context.Context, ...) (string, error)
    // ... all Ops methods
}
```

All packages depend on `Ops` interface, not concrete `Client`.

---

## K. Risk Assessment

| Risk | Mitigation |
|---|---|
| Docker SDK version churn | Pin to v27.x, stable API surface only |
| Pure-Go SQLite performance | `modernc.org/sqlite` is well-tested, adequate for our scale |
| Interactive TTY for `cli` | Docker SDK + `moby/term` handle raw terminal |
| Concurrent .meta access | Atomic write (temp + rename) |
| git dependency for image build | Accepted: only for `image build/update`, not daily ops |
| Migration risk | Zero: identical file formats and paths |
