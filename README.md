# xnc

Manage fleets of AI agents from your phone. One binary, full control.

`xnc` orchestrates [nullclaw](https://github.com/huru-io/nullclaw) AI agents — each running isolated in hardened Docker containers with persistent memory. Talk to them from the terminal, pipe messages through scripts, or run your entire fleet from Telegram.

## Telegram-native agent orchestration

The killer feature. Run `xnc mux` and your Telegram bot becomes a full agent control plane:

```bash
xnc mux start              # start the mux daemon
```

From Telegram you can:

- **Talk to any agent** — the mux AI routes your messages, starts agents on demand, and manages conversations
- **Send voice messages** — transcribed via Whisper, agents can respond with TTS
- **Manage your fleet** — start, stop, clone, rename, snapshot agents — all through natural conversation
- **Install skills** — drop a `.md` or `.zip` file in the chat, the mux installs it to any agent or all of them
- **Track costs** — ask "how much did alice spend today?" and get real answers
- **Send files** — upload documents, the mux delivers them to the right agent's workspace

Everything the CLI can do, the mux can do from your phone. No SSH, no terminal, no VPN.

```bash
xnc mux stop               # stop the daemon
xnc mux logs -f            # follow logs
xnc mux status             # check if running
```

## Install

### asdf (recommended)

```bash
asdf plugin add xnc https://github.com/huru-io/asdf-xnc.git
asdf install xnc latest
asdf set --home xnc latest
```

Manages versions automatically. Update with `asdf install xnc latest`.

### Download binary

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/huru-io/xnullclaw/releases/latest/download/xnc_$(curl -fsSL https://api.github.com/repos/huru-io/xnullclaw/releases/latest | grep tag_name | cut -d'"' -f4)_darwin_arm64.tar.gz | tar xz -C /usr/local/bin

# macOS (Intel)
curl -fsSL https://github.com/huru-io/xnullclaw/releases/latest/download/xnc_$(curl -fsSL https://api.github.com/repos/huru-io/xnullclaw/releases/latest | grep tag_name | cut -d'"' -f4)_darwin_amd64.tar.gz | tar xz -C /usr/local/bin

# Linux (x86_64)
curl -fsSL https://github.com/huru-io/xnullclaw/releases/latest/download/xnc_$(curl -fsSL https://api.github.com/repos/huru-io/xnullclaw/releases/latest | grep tag_name | cut -d'"' -f4)_linux_amd64.tar.gz | tar xz -C /usr/local/bin

# Linux (arm64)
curl -fsSL https://github.com/huru-io/xnullclaw/releases/latest/download/xnc_$(curl -fsSL https://api.github.com/repos/huru-io/xnullclaw/releases/latest | grep tag_name | cut -d'"' -f4)_linux_arm64.tar.gz | tar xz -C /usr/local/bin
```

Or grab the right archive from the [Releases](https://github.com/huru-io/xnullclaw/releases) page, extract, and put `xnc` somewhere on your `PATH`.

### Build from source

```bash
git clone https://github.com/huru-io/xnullclaw.git
cd xnullclaw
make build        # -> ./build/xnc
make install      # -> /usr/local/bin/xnc
```

### Requirements

- Docker (running)
- nullclaw container image

## Quick start

```bash
# Interactive wizard — sets up API keys, agents, optional Telegram bot
xnc init

# Pull the nullclaw Docker image
xnc image build

# Start an agent
xnc start alice

# Talk to it
echo "Summarize the Go 1.24 release notes" | xnc send alice

# Interactive session
xnc cli alice
```

Non-interactive setup for automation:

```bash
xnc init --openai-key sk-... -n 3 --name alice --name bob --name carol
```

## What xnc does

**nullclaw** is the AI agent runtime — it handles LLM calls, tool execution, memory, and skills inside a container.

**xnc** is the control plane on top — it creates, configures, starts, stops, clones, snapshots, and orchestrates those agents from the outside. Think `docker` to nullclaw's application.

### Container hardening

Every agent runs locked down:

- Read-only root filesystem
- All Linux capabilities dropped
- No privilege escalation
- `/tmp` mounted noexec (64 MB)
- 128 MB memory, 0.25 CPU, 64 PIDs
- Runs as host user, never root
- Restart policy: unless-stopped

### Multi-provider LLM

Configure any combination of providers per agent:

- OpenAI (`openai/gpt-5-mini`)
- Anthropic (`anthropic/claude-sonnet-4`)
- OpenRouter (`openrouter/...`)

Keys validated at setup time with a lightweight `/models` probe.

### Skill system

Skills are instruction sets that teach agents new capabilities. Install from directories, zip archives, or plain markdown files:

```bash
xnc skill install ./code-review/          # directory with SKILL.toml + SKILL.md
xnc skill install coding-standards.md     # single markdown (name from # heading)
xnc skill install skills.zip              # zip archive

xnc skill install ./my-skill --agent bob  # one agent
xnc skill install ./my-skill --all        # shared + sync to all agents
xnc skill list --all                      # see what's installed
```

Shared skills (`~/.xnc/skills/`) are auto-installed to new agents. Agent-local skills override shared ones.

### Snapshots and cloning

```bash
xnc snapshot alice                    # backup full state
xnc restore alice-20240315 alice-v2   # restore as new agent
xnc clone alice bob --with-data       # duplicate with conversation history
```

### Cost tracking

Per-agent LLM cost tracking with budget enforcement:

```bash
xnc costs alice --today
xnc costs alice --month --json
```

### Agent rename

Rename with full identity propagation — filesystem, config, system prompt, and an identity-change message sent to the agent:

```bash
xnc rename old-name new-name
```

## CLI reference

### Bootstrap

```
xnc init [flags]                          Interactive setup wizard
```

### Lifecycle

```
xnc setup    <names...> [flags]           Create agent(s)
xnc start    <agents...> [--port N]       Start containers
xnc stop     <agents...> [--all]          Stop containers
xnc restart  <agents...> [--port N]       Restart containers
xnc destroy  <agents...> [--yes]          Delete permanently
xnc clone    <source> <new> [--with-data] Clone an agent
xnc rename   <old> <new>                  Rename an agent
```

### Interaction

```
xnc send     <agents...> [--all]          Pipe stdin to agent(s)
xnc cli      <agent>                      Interactive chat
xnc logs     <agent> [-f] [--tail N]      Container logs
xnc drain    <agent>                      Drain buffered output
xnc watch    <agent>                      Stream live output
```

### Files

```
xnc cp-to    <agent> <file> [dest]        Copy file into container
xnc cp-from  <agent> <path> [dest]        Copy file out
```

### Config

```
xnc config   get <agent> [key]            Read config
xnc config   set <agent> <key> <value>    Write config
xnc persona  <agent|mux> [--show] [--reset] [--preset NAME] [--list-presets] [--trait TEXT] [--warmth N] ...  Personality editor
xnc costs    <agent> [--today|--month]    Cost summary
```

### Skills

```
xnc skill    list [--agent N] [--all]     List skills
xnc skill    install <src> [--agent N]    Install skill
xnc skill    remove <name> [--agent N]    Remove skill
xnc skill    info <name> [--agent N]      Skill details
```

### Fleet

```
xnc status   [agents...] [flags] [--json] Agent status (default: all)
xnc list                                  Alias for: status
xnc running                               Alias for: status --running
```

Status flags: `--running`, `--stopped`, `--error`. Combine with agent names to filter further.

### Snapshots

```
xnc snapshot  <agent>                     Create snapshot
xnc restore   <snapshot> [name]           Restore from snapshot
xnc snapshots [--json]                    List snapshots
xnc snapshot-delete <snapshot>            Delete snapshot
```

### Mux

```
xnc mux                                  Check mux status (same as mux status)
xnc mux      start [--foreground]        Start Telegram bot daemon
xnc mux      stop                        Stop daemon
xnc mux      status                      Check status
xnc mux      logs [-f]                   View logs
```

### Image

```
xnc image    build [--from-source]       Pull or build image
xnc image    update [--from-source]      Update image
xnc image    status                      Image info
```

### Global flags

```
--home <path>     Override XNC_HOME (default: ~/.xnc)
--image <name>    Override Docker image
--json            JSON output
--quiet           Suppress output
```

## Architecture

- Single static binary (~12 MB). Pure Go, `CGO_ENABLED=0`, SQLite via `modernc.org/sqlite`.
- Docker SDK behind an `Ops` interface — only `internal/docker/` imports it.
- 14 internal packages: agent, cli, config, docker, llm, logging, loop, media, memory, mux, prompt, telegram, tools, voice.
- Concurrency: `flock` serialization inside containers for safe concurrent access.
- Data: `~/.xnc/` (override with `XNC_HOME` or `--home`).

## Environment variables

| Variable | Description |
|----------|-------------|
| `XNC_HOME` | Home directory (default: `~/.xnc`) |
| `XNC_IMAGE` | Docker image (default: `nullclaw:latest`) |
| `OPENAI_API_KEY` | OpenAI key (read during init/setup) |
| `ANTHROPIC_API_KEY` | Anthropic key (read during init/setup) |
| `OPENROUTER_API_KEY` | OpenRouter key (read during init/setup) |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token (read during init) |

## License

MIT License. See [LICENSE](LICENSE) for details.
