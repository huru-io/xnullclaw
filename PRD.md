# xnullclaw — Product Requirements Document

## 1. Overview

**xnullclaw** is a personal AI agent orchestration platform. It manages multiple independent AI agents (powered by [nullclaw](https://github.com/nullclaw/nullclaw)), each running in hardened Docker containers, orchestrated by an AI-driven multiplexer bot ("mux") accessible via Telegram.

The mux is not a dumb router — it is itself a GPT-5.4-class AI that understands intent, delegates to agents, synthesizes results, handles voice, and manages the agent fleet autonomously. It is the user's **personal intelligence layer**.

## 2. System Architecture

```
                         You (Telegram)
                              │
                         voice / text
                              │
                    ┌─────────▼──────────┐
                    │      xnc-mux       │
                    │  (Go + OpenAI)      │
                    │                     │
                    │  - AI orchestrator  │
                    │  - Whisper STT      │
                    │  - Intent routing   │
                    │  - Agent lifecycle  │
                    │  - Conversation mem │
                    └──┬──────┬───────┬──┘
                       │      │       │
                  Docker network: xnc-net
                  (no host ports exposed)
                       │      │       │
                 ┌─────┴┐ ┌──┴───┐ ┌─┴─────┐
                 │ alice │ │ bob  │ │ carol │
                 │ :3000 │ │:3000│ │ :3000 │
                 └───────┘ └─────┘ └───────┘
                 (nullclaw agents in Docker)
```

### Two Operating Modes

| Mode | Description |
|------|-------------|
| **Direct** | Each agent has its own Telegram bot. Simple, independent. Managed via `xnullclaw <agent> start`. |
| **Multiplexed (mux)** | Single Telegram bot controlled by the AI mux. Agents have no Telegram config. All communication routed through the mux over a Docker-internal network. |

Both modes coexist — some agents can run in direct mode while others are managed by the mux.

## 3. Components

### 3.1 xnullclaw (shell wrapper) — EXISTS

The CLI that manages agent lifecycle. Already implemented.

**Agent commands:**
- `xnullclaw <agent> setup` — create agent with config template
- `xnullclaw <agent> start [--port PORT]` — start as always-on daemon
- `xnullclaw <agent> stop` — stop agent
- `xnullclaw <agent> restart` — restart agent
- `xnullclaw <agent> status` — show agent status
- `xnullclaw <agent> logs [-f]` — view/follow container logs
- `xnullclaw <agent> cli [-m "msg"]` — interactive local chat or one-shot
- `xnullclaw <agent> clone <source> [--with-data]` — clone config/data
- `xnullclaw <agent> destroy` — permanently delete agent + data

**Global commands:**
- `xnullclaw list` — list all agents
- `xnullclaw image build` — build nullclaw Docker image from GitHub
- `xnullclaw image update` — pull latest + rebuild

**New commands for mux mode (to be added):**
- `xnullclaw mux start` — start the mux bot container
- `xnullclaw mux stop` — stop the mux bot
- `xnullclaw mux status` — show mux status + connected agents
- `xnullclaw mux logs [-f]` — mux container logs
- `xnullclaw <agent> start --mux` — start agent on the mux Docker network (no port exposed, no Telegram config)

**Docker hardening (all containers, automatic):**
- Read-only root filesystem
- All Linux capabilities dropped
- no-new-privileges
- No host ports by default
- /tmp as noexec tmpfs
- Config mounted read-only
- Runs as host user (non-root)

### 3.2 nullclaw agents — EXISTS (upstream)

Each agent is a nullclaw instance in its own Docker container with isolated config, data, memory, and workspace.

**Per-agent data** (`~/.xnullclaw/<agent>/`):
- `config.json` — agent configuration (provider, model, autonomy, tools, etc.)
- `data/.nullclaw/` — secrets, auth, cron (auto-managed)
- `data/workspace/` — memory (SQLite + markdown), files

**Agent capabilities** (via nullclaw tools):
- File read/write/append
- Shell command execution (sandbox-scoped)
- Git operations
- HTTP requests, web fetch, web search
- Memory store/recall/forget/list
- Cron scheduling (add/remove/list/run)
- Hardware info
- Browser automation
- Subagent delegation
- Composio integration

**Agent webhook API** (gateway `/webhook` endpoint):
- `POST /webhook` with `{"message": "..."}` — synchronous request/response
- Returns `{"status": "ok", "response": "agent reply text"}`
- Session keyed by Bearer token — supports per-user conversation isolation
- Rate limit: configurable (default 60 req/min)
- Auth: Bearer token via pairing, or disabled on trusted Docker network

**Agent configuration knobs:**
- Provider + model (50+ models across 23+ providers)
- Temperature (0.0–2.0)
- Autonomy level: `supervised` / `autonomous` / `restricted` / `full` / `yolo`
- Workspace scoping (on/off)
- Allowed commands (allowlist)
- Max actions per hour
- Memory backend (SQLite, markdown, etc.)
- System prompt (custom per agent)
- Tool iterations limit (default 1000)
- History length + auto-compaction
- Session idle timeout

### 3.3 xnc-mux (AI orchestrator bot) — TO BE BUILT

The core new component. A Go service that:
1. Connects to Telegram as a single bot (long-polling)
2. Runs an AI model (GPT-5.4 or configurable) as its brain
3. Routes messages to/from agents via their `/webhook` endpoints
4. Manages agent lifecycle via Docker API or xnullclaw CLI
5. Handles voice messages via Whisper transcription
6. Maintains its own conversation memory and agent status awareness

## 4. Mux Bot Requirements

### 4.1 AI-Driven Intent Understanding

The mux is not a command parser — it is an AI that understands natural language.

**The mux MUST:**
- Determine whether the user is talking to IT (the mux) or to an agent
- Detect when the user is addressing a specific agent by name, context, or topic
- Pass through messages transparently when clearly directed at an agent
- Handle ambiguous intent by asking for clarification or using context
- Understand follow-up messages belong to the same agent conversation
- Recognize when the user switches topics/agents mid-conversation

**Examples:**
```
User: "Ask alice to check the server logs"
→ mux routes to alice: "check the server logs"

User: "What did she find?"
→ mux routes to alice (follow-up, same context)

User: "Bob, summarize today's news"
→ mux routes to bob: "summarize today's news"

User: "Tell all agents to stop what they're doing"
→ mux sends stop signal to all running agents

User: "What's everyone working on?"
→ mux summarizes all agents' current tasks/status

User: "That last response from alice was wrong, she should try again with a different approach"
→ mux routes corrected instruction to alice
```

### 4.2 Agent Communication

**The mux MUST:**
- Send messages to individual agents via `POST http://xnc-{name}:3000/webhook`
- Receive synchronous responses and relay them to the user
- Support sending the same message to multiple agents in parallel
- Collect and synthesize responses from multiple agents
- Prefix agent responses with agent identifier: `[alice] response text...`
- Track which agent the user is currently "talking to" for follow-ups
- Handle agent errors/timeouts gracefully: `[alice] (offline)` or `[alice] (timeout — still thinking)`

**The mux MUST support these interaction patterns:**
- **One-to-one**: user talks to a single agent (most common)
- **Broadcast**: user sends a message to all agents simultaneously
- **Selective**: user sends to a subset of agents ("ask alice and bob to...")
- **Orchestrated**: mux decides which agent(s) should handle a request based on their roles/capabilities

### 4.3 Agent Lifecycle Management

**The mux MUST be able to:**
- Start an agent: execute `xnullclaw <agent> start --mux` (or Docker API equivalent)
- Stop an agent: execute `xnullclaw <agent> stop`
- Restart an agent
- Check agent health: `GET http://xnc-{name}:3000/health`
- List all agents and their status
- Report agent status to the user proactively (e.g., "alice just crashed, restarting...")

**The mux SHOULD be able to:**
- Create new agents (`xnullclaw <agent> setup`)
- Clone agents (`xnullclaw <agent> clone <source>`)
- Modify agent configuration (edit config.json: model, temperature, system prompt, autonomy level, etc.)
- Destroy agents (with user confirmation)

### 4.4 Voice Input (Whisper)

**The mux MUST:**
- Accept Telegram voice messages and audio files
- Transcribe audio using OpenAI Whisper API
- Process the transcription as if it were a text message
- Apply intelligent correction to transcription artifacts (names, technical terms, agent names the user commonly says)
- Show the transcription to the user before acting: `[heard] "ask alice to check the server logs"`
- Allow the user to correct: replying "no, I said BOB" re-routes accordingly

**The mux SHOULD:**
- Learn the user's speech patterns over time (common words, agent names, technical jargon)
- Maintain a custom vocabulary/corrections dictionary

### 4.5 Conversation Memory

**The mux MUST maintain:**
- Its own conversation history with the user (persistent across restarts)
- A summary of what each agent is currently doing / last did
- Knowledge of each agent's configuration (model, role, capabilities)
- Context about recent interactions (which agent was last addressed, ongoing tasks)

**The mux uses this memory to:**
- Route follow-up messages correctly without explicit agent naming
- Answer "what is alice doing?" without asking alice
- Provide situational awareness: "you asked bob to research X an hour ago, he's probably done"
- Avoid re-asking agents for information the mux already knows

### 4.6 Agent Configuration Management

**The mux MUST be able to modify agent configs when instructed:**
- Change an agent's model: "switch alice to claude-sonnet"
- Change temperature: "make bob more creative"
- Change autonomy level: "give carol full autonomy"
- Change system prompt: "tell alice she's a security researcher now"
- These changes persist (written to the agent's `config.json`)
- Agent restart required for config changes to take effect (mux handles this automatically)

### 4.7 Telegram Commands (fallback for explicit control)

In addition to natural language, the mux MUST support explicit commands:

| Command | Action |
|---------|--------|
| `/dm <agent> <message>` | Send message directly to a specific agent |
| `/switch <agent>` | Set default agent for subsequent messages |
| `/broadcast <message>` | Send to all running agents |
| `/list` | Show all agents and status |
| `/status [agent]` | Detailed status (agent or mux) |
| `/start <agent>` | Start an agent |
| `/stop <agent>` | Stop an agent |
| `/restart <agent>` | Restart an agent |
| `/config <agent> <key> <value>` | Modify agent config |
| `/mux` | Talk to the mux itself (force context) |
| `/history [agent]` | Show recent conversation summary |

These commands exist as a reliable fallback. In normal use, the AI handles intent from natural language.

### 4.8 OpenAI Function Calling (Tool Use)

The mux AI model uses OpenAI function calling to interact with the system. The mux does NOT use regex or command parsing — it uses structured tool calls.

**Mux tools (functions):**

```
send_to_agent(agent: str, message: str) -> str
    Send a message to a specific agent, return the response.

send_to_agents(agents: [str], message: str) -> [{agent: str, response: str}]
    Send a message to multiple agents in parallel, return all responses.

start_agent(agent: str) -> str
    Start an agent. Returns status.

stop_agent(agent: str) -> str
    Stop an agent. Returns status.

restart_agent(agent: str) -> str
    Restart an agent. Returns status.

list_agents() -> [{name: str, status: str, model: str, uptime: str}]
    List all agents and their current status.

agent_health(agent: str) -> {status: str, details: str}
    Check an agent's health endpoint.

update_agent_config(agent: str, key: str, value: any) -> str
    Modify an agent's configuration. Requires restart to take effect.

get_agent_config(agent: str) -> object
    Read an agent's current configuration.

transcribe_audio(file_id: str) -> str
    Transcribe a Telegram voice message/audio file via Whisper.

get_conversation_summary(agent: str) -> str
    Get summary of recent conversation with an agent.

remember(fact: str) -> str
    Store a fact in the mux's own memory.

recall(query: str) -> str
    Search the mux's own memory.
```

The AI model decides which tools to call based on the user's message. This is the core of the orchestration — the model IS the router.

## 5. Technical Specification

### 5.1 Tech Stack

| Component | Technology |
|-----------|-----------|
| Mux bot service | **Go** |
| AI inference | OpenAI API (GPT-5.4 or configurable, with function calling) |
| Speech-to-text | OpenAI Whisper API |
| Telegram integration | Go Telegram Bot API library |
| Agent communication | HTTP client → nullclaw `/webhook` endpoints |
| Agent lifecycle | Docker Engine API (Go SDK) or exec `xnullclaw` CLI |
| Mux persistent memory | SQLite (local file in mux data dir) |
| Container orchestration | Docker + shared network (`xnc-net`) |
| Version management | **asdf** (`.tool-versions` for Go version pinning) |

### 5.2 Directory Layout

```
xnullclaw/                          ← this repo
├── .tool-versions                  ← asdf: golang version
├── xnullclaw                       ← shell wrapper (bash)
├── mux/                            ← mux bot source (Go)
│   ├── go.mod
│   ├── go.sum
│   ├── main.go
│   ├── Dockerfile
│   ├── bot/                        ← Telegram bot handler
│   │   └── bot.go
│   ├── router/                     ← AI-driven intent routing
│   │   └── router.go
│   ├── agents/                     ← agent HTTP client + lifecycle
│   │   └── agents.go
│   ├── voice/                      ← Whisper transcription
│   │   └── voice.go
│   ├── memory/                     ← mux conversation memory (SQLite)
│   │   └── memory.go
│   ├── tools/                      ← OpenAI function definitions
│   │   └── tools.go
│   └── config/                     ← mux configuration
│       └── config.go
├── PRD.md                          ← this document
└── nullclaw/                       ← upstream clone (gitignored)
```

### 5.3 Mux Configuration

Lives at `~/.xnullclaw/.mux/config.json`:

```json
{
  "telegram": {
    "bot_token": "BOT_TOKEN_FROM_BOTFATHER",
    "allow_from": ["YOUR_TELEGRAM_USER_ID"]
  },

  "openai": {
    "api_key": "sk-...",
    "model": "gpt-5.4",
    "temperature": 0.7,
    "whisper_model": "whisper-1"
  },

  "agents": {
    "default": "alice",
    "auto_start": ["alice", "bob"]
  },

  "voice": {
    "enabled": true,
    "show_transcription": true,
    "correction_dictionary": {
      "alice": ["ellis", "allice", "alyss"],
      "bob": ["bop", "barb"]
    }
  },

  "memory": {
    "db_path": "memory.db",
    "summary_interval_messages": 20
  }
}
```

### 5.4 Docker Networking

```
# Created by xnullclaw mux start:
docker network create xnc-net

# Mux container:
docker run --network xnc-net --name xnc-mux ...

# Agent containers (started with --mux flag):
docker run --network xnc-net --name xnc-alice ... nullclaw gateway
docker run --network xnc-net --name xnc-bob   ... nullclaw gateway

# Mux reaches agents at:
#   http://xnc-alice:3000/webhook
#   http://xnc-bob:3000/webhook
#   http://xnc-carol:3000/health
```

No host ports exposed for any container. All traffic stays within the Docker network.

### 5.5 Agent Config in Mux Mode

When an agent runs in mux mode (`xnullclaw <agent> start --mux`), its config is simplified:

- `channels.telegram` — **removed** (mux handles Telegram)
- `gateway.require_pairing` — **false** (Docker network is the trust boundary)
- `gateway.host` — **0.0.0.0** (listen on container interface)
- Everything else unchanged (model, autonomy, tools, memory, etc.)

### 5.6 Authentication Between Mux and Agents

On the internal Docker network, agents run with `require_pairing: false`. The network boundary IS the auth boundary — only containers on `xnc-net` can reach agents.

The mux sends a per-user Bearer token (`Authorization: Bearer user_{telegram_user_id}`) to create isolated sessions per user. For single-user (the common case), a fixed token is fine.

### 5.7 Response Timeout Handling

Nullclaw agents can take time to think (especially with tool use chains). The mux handles this:

1. Send "thinking..." indicator to Telegram (typing action)
2. POST to agent with a generous HTTP timeout (120s+)
3. If agent responds within timeout → relay response
4. If timeout → inform user: `[alice] Still working... (timeout after 120s)`
5. Optionally: poll the agent or retry

Agent-side timeout is configurable via `agents.defaults.message_timeout_secs` (default: 600s = 10 minutes).

## 6. Message Flow Examples

### 6.1 Text to Specific Agent

```
User:  "alice, check if the API is responding"
Mux AI: → calls send_to_agent("alice", "check if the API is responding")
Agent:  ← {"response": "The API at api.example.com is returning 200 OK..."}
Mux AI: → formats and sends to Telegram
User sees: [alice] The API at api.example.com is returning 200 OK...
```

### 6.2 Voice Message

```
User:  🎤 (voice message)
Mux:   → calls transcribe_audio(file_id)
Whisper: ← "tell Ellis to check the server"
Mux AI: → recognizes "Ellis" → "alice" (from correction dictionary)
Mux:   → sends to Telegram: [heard] "tell alice to check the server"
Mux AI: → calls send_to_agent("alice", "check the server")
...
```

### 6.3 Broadcast

```
User:  "everyone, stop all background tasks"
Mux AI: → calls list_agents() to find running agents
       → calls send_to_agents(["alice","bob","carol"], "stop all background tasks")
Agents: ← [3 responses]
Mux AI: → synthesizes and sends:
User sees:
  [alice] All background tasks stopped. 2 cron jobs paused.
  [bob] Stopped web scraping job. No other tasks running.
  [carol] No background tasks were active.
```

### 6.4 Lifecycle Management

```
User:  "alice seems slow today, restart her and switch her to gpt-4o-mini"
Mux AI: → calls update_agent_config("alice", "agents.defaults.model.primary", "openai/gpt-4o-mini")
       → calls restart_agent("alice")
       → waits for health check
User sees:
  Done. Alice restarted with gpt-4o-mini. She's back online.
```

### 6.5 Mux as Coordinator

```
User:  "I need a competitive analysis of Acme Corp"
Mux AI: (decides this needs multiple agents)
       → calls send_to_agent("alice", "Find Acme Corp's recent news and press releases")
       → calls send_to_agent("bob", "Analyze Acme Corp's public financial data")
       → collects both responses
       → synthesizes into a unified report
User sees:
  Here's a competitive analysis of Acme Corp based on research from alice and bob:

  **Recent Developments** (via alice):
  ...

  **Financial Position** (via bob):
  ...

  **My Assessment:**
  ...
```

## 7. Implementation Phases

### Phase 0 — Foundation (DONE)
- [x] xnullclaw wrapper script
- [x] Hardened Docker setup
- [x] Multi-agent lifecycle (setup/start/stop/clone/destroy)
- [x] Security review of nullclaw
- [x] No-port-by-default mode (Telegram outbound polling)

### Phase 1 — Mux Core
- [ ] Go project scaffold (`mux/`, Dockerfile, `.tool-versions`)
- [ ] Telegram bot connection (long-polling, text messages)
- [ ] OpenAI integration with function calling
- [ ] Core tools: `send_to_agent`, `list_agents`, `agent_health`
- [ ] Docker network setup in xnullclaw (`--mux` flag, `xnc-net`)
- [ ] Agent webhook communication (HTTP POST, Bearer token)
- [ ] Basic intent routing (AI determines agent vs mux)
- [ ] Response formatting with agent prefixes

### Phase 2 — Voice + Memory
- [ ] Voice message handling (download from Telegram)
- [ ] Whisper transcription integration
- [ ] Transcription correction dictionary
- [ ] Show-before-act flow (display transcription, then process)
- [ ] Mux conversation memory (SQLite)
- [ ] Agent status tracking and summarization
- [ ] `remember` / `recall` tools

### Phase 3 — Agent Lifecycle via Mux
- [ ] Tools: `start_agent`, `stop_agent`, `restart_agent`
- [ ] Docker API integration (or xnullclaw CLI exec)
- [ ] `update_agent_config` + auto-restart
- [ ] `get_agent_config` tool
- [ ] Proactive health monitoring and alerting

### Phase 4 — Advanced Orchestration
- [ ] Multi-agent parallel dispatch (`send_to_agents`)
- [ ] Response synthesis (mux summarizes/combines agent outputs)
- [ ] Agent role awareness (mux knows what each agent is good at)
- [ ] Auto-routing based on agent specialization (no explicit agent naming needed)
- [ ] Conversation context handoff between agents
- [ ] Broadcast support

### Phase 5 — Polish
- [ ] Telegram command fallbacks (`/dm`, `/list`, `/status`, etc.)
- [ ] Error recovery (agent crash detection, auto-restart)
- [ ] Rate limiting awareness
- [ ] Timeout handling with progress indicators
- [ ] Mux self-update capability
- [ ] Voice vocabulary learning

## 8. Non-Goals (for now)

- Web UI or dashboard — Telegram is the interface
- Multi-user access control — this is a personal system
- Agent-to-agent direct communication — all routing goes through mux
- Custom agent images — all agents use the same nullclaw Docker image
- Cloud deployment — local Docker only
- End-to-end encryption — Docker network is the trust boundary

## 9. Success Criteria

1. User can talk to any agent by name via a single Telegram bot
2. Mux correctly routes >95% of messages without explicit commands
3. Voice messages work with <5s latency (transcribe + route + response display)
4. Agent lifecycle (start/stop/restart) manageable entirely from Telegram
5. Mux maintains useful context across conversations (no "who is alice?" amnesia)
6. Zero host ports exposed in mux mode
7. All containers maintain hardened security posture
