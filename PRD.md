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
                  xnullclaw wrapper (CLI)
               (agent discovery, lifecycle,
                message relay, config mgmt)
                       │      │       │
                 ┌─────┴┐ ┌──┴───┐ ┌─┴─────┐
                 │ alice │ │ bob  │ │ carol │
                 │ :3000 │ │:3000│ │ :3000 │
                 └───────┘ └─────┘ └───────┘
                 (nullclaw agents in Docker)
```

The mux runs as a **host-level process** (not in Docker) and interacts with agents
exclusively through the `xnullclaw` wrapper. The wrapper is the single source of truth
for agent discovery, lifecycle, networking, and message relay. The mux never talks
to Docker directly or hardcodes container names/ports.

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
- `xnullclaw mux start` — start the mux bot (host-level Go process)
- `xnullclaw mux stop` — stop the mux bot
- `xnullclaw mux status` — show mux status + connected agents
- `xnullclaw mux logs [-f]` — mux process logs
- `xnullclaw <agent> start --mux` — start agent with internal port for mux communication (no Telegram config)
- `xnullclaw <agent> send "message"` — send a message to an agent's webhook, return the response (used by mux)
- `xnullclaw <agent> endpoint` — print the agent's HTTP endpoint URL (for mux discovery)
- `xnullclaw running` — list only running agents with their endpoints (machine-readable, for mux)

The mux uses `xnullclaw` for ALL agent interactions — it never calls Docker directly.
This keeps the wrapper as the single abstraction layer over container details.

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

The core new component. A Go binary that runs as a **host-level process** (not in Docker):
1. Connects to Telegram as a single bot (long-polling)
2. Runs an AI model (GPT-5.4 or configurable) as its brain
3. Routes messages to/from agents via `xnullclaw <agent> send` (wrapper handles HTTP details)
4. Manages agent lifecycle via `xnullclaw` CLI (start/stop/restart/setup/clone/destroy)
5. Discovers agents via `xnullclaw list` and `xnullclaw running`
6. Handles voice messages via Whisper transcription
7. Maintains its own conversation memory and agent status awareness

**Why host-level, not Docker?**
- The mux needs to call `xnullclaw` (a host CLI tool) for lifecycle management
- The mux has no untrusted workloads — it's your personal orchestrator
- No need for filesystem isolation — it reads agent configs from `~/.xnullclaw/`
- Simpler: no Docker socket mounting, no network gymnastics

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

All agent communication goes through the `xnullclaw` wrapper. The mux never constructs
URLs, manages ports, or talks to Docker directly.

**The mux MUST:**
- Send messages via `xnullclaw <agent> send "message"` (wrapper handles HTTP internally)
- Receive synchronous responses (wrapper returns the agent's reply on stdout)
- Support sending to all running agents at once via `xnullclaw send-all "message"` (wrapper fans out in parallel)
- Support sending to a named subset via `xnullclaw send-some "alice,bob" "message"`
- Collect and synthesize responses from multiple agents
- Prefix agent responses with agent identifier: `[alice] response text...`
- Track which agent the user is currently "talking to" for follow-ups
- Handle agent errors/timeouts gracefully: `[alice] (offline)` or `[alice] (timeout — still thinking)`

**The mux MUST support these interaction patterns:**
- **One-to-one**: user talks to a single agent (most common)
- **Broadcast**: user sends a message to all running agents — no enumeration required
- **Selective**: user sends to a subset of agents ("ask alice and bob to...")
- **Orchestrated**: mux decides which agent(s) should handle a request based on their roles/capabilities

### 4.3 Agent Lifecycle Management

All lifecycle operations go through the `xnullclaw` CLI.

**The mux MUST be able to:**
- Start an agent: `xnullclaw <agent> start --mux`
- Stop an agent: `xnullclaw <agent> stop`
- Restart an agent: `xnullclaw <agent> restart`
- Check agent health: `xnullclaw <agent> status` (wrapper checks Docker + health endpoint)
- List all agents and their status: `xnullclaw list`
- List running agents with endpoints: `xnullclaw running`
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

### 4.5 Conversation Memory and Long-Term Stability

The mux is an always-on 24/7 orchestrator. Its memory system must be designed for
**indefinite operation** — months or years of continuous use without degradation.

**Memory tiers:**

| Tier | What | Storage | Lifecycle |
|------|------|---------|-----------|
| **Working context** | Current conversation turn, recent messages | In-memory (Go structs) | Sent to the model as context window |
| **Short-term history** | Recent conversation messages (last N hours/messages) | SQLite `messages` table | Auto-compacted: older messages are summarized and promoted to long-term |
| **Long-term facts** | Important facts, decisions, user preferences, agent roles | SQLite `facts` table | Persistent, searchable, manually or auto-curated |
| **Agent summaries** | What each agent is doing, last interaction, current task | SQLite `agent_state` table | Updated after every agent interaction |
| **Compaction log** | Summaries of compacted conversation blocks | SQLite `compactions` table | Append-only archive, queryable for "what happened last week" |

**Compaction strategy (critical for 24/7 stability):**

The mux's context window is finite. As conversation history grows, the mux MUST compact
it without losing important information:

1. **Rolling window**: Keep the last N messages (e.g., 50) in full detail
2. **Auto-summarize**: When messages exceed the window, the mux summarizes the oldest
   block into a concise paragraph and stores it in the compaction log
3. **Fact extraction**: Before compacting, the mux extracts important facts
   (decisions, preferences, agent assignments) and stores them in `facts`
4. **Agent state refresh**: After each agent interaction, update the agent's summary
   in `agent_state` — the mux always knows what each agent is working on
5. **Context assembly**: Each new turn, the model receives:
   - System prompt (role, tools, instructions)
   - Long-term facts (retrieved by relevance to current message)
   - Agent summaries (all active agents)
   - Recent compaction summaries (last 2-3 blocks)
   - Full recent messages (the rolling window)

**The mux MUST:**
- Never lose important facts during compaction
- Survive restarts without amnesia (all tiers persist in SQLite)
- Be able to answer "what happened yesterday?" from compaction logs
- Track user preferences learned over time ("I prefer alice for code tasks")
- Self-maintain: periodically review and prune stale facts
- Handle context overflow gracefully — degrade by dropping old summaries, not crashing

**The mux MUST maintain:**
- Its own conversation history with the user (persistent, compacted, across restarts)
- A summary of what each agent is currently doing / last did
- Knowledge of each agent's configuration (model, role, capabilities)
- Context about recent interactions (which agent was last addressed, ongoing tasks)
- User preferences and patterns learned over time

**The mux uses this memory to:**
- Route follow-up messages correctly without explicit agent naming
- Answer "what is alice doing?" without asking alice
- Provide situational awareness: "you asked bob to research X an hour ago, he's probably done"
- Avoid re-asking agents for information the mux already knows
- Maintain personality consistency and user rapport across days/weeks

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

### 4.8 Agentic Loop Architecture

The mux is itself an agent — it runs a continuous tool-calling loop powered by
OpenAI's Responses API. This is the core of the orchestration.

**Why an agentic loop (not single-step request-response):**

Most real interactions require multiple steps:
```
"Set up a code review agent and have it review this file"
  → setup_agent("dave")              // create agent
  → update_agent_config(...)         // set system prompt
  → start_agent("dave")             // start container
  → agent_status("dave")            // wait for healthy
  → send_file_to_agent("dave", ...) // deliver the file
  → send_to_agent("dave", "Review") // get the review
  → relay response to Telegram       // done
```

That's 6 tool calls with decisions between each. The model drives the loop —
it decides what to do next based on each tool result.

**Implementation — own loop with `openai-go` SDK:**

```go
// Core agentic loop (~30 lines, the rest is tool implementations)
func (m *Mux) run(ctx context.Context, userMessage string) (string, error) {
    m.appendUserMessage(userMessage)

    for i := 0; i < m.maxIterations; i++ {
        resp, err := m.client.Responses.New(ctx, m.buildRequest())
        if err != nil {
            return "", err
        }

        toolCalls := extractToolCalls(resp)
        if len(toolCalls) == 0 {
            // No tool calls = final text response
            text := extractText(resp)
            m.appendAssistantMessage(text)
            return text, nil
        }

        // Execute tool calls (parallel when multiple in one response)
        results := m.executeToolCalls(ctx, toolCalls)
        m.appendToolResults(results)
        // Loop continues — model sees results and decides next action
    }

    return "", fmt.Errorf("max iterations (%d) exceeded", m.maxIterations)
}
```

**Design decision: own loop vs framework**

Evaluated alternatives:
- **OpenAI Agents SDK**: Python/TypeScript only. No official Go version.
- **nlpodyssey/openai-agents-go**: Community port, active but not official.
- **Google ADK Go**: Production-grade but Google-ecosystem-oriented, heavy.
- **CloudWeGo Eino**: Full LangChain-equivalent for Go. Overkill.
- **Nullclaw as mux via MCP**: Elegant (write tools as MCP server, let nullclaw
  run the loop), but locks us to nullclaw's UX and memory model.

**Chosen: own loop with `openai-go` (official SDK)**. Reasons:
- The loop is ~30 lines of Go — a framework adds dependency without value
- `openai-go` is official, stable, minimal (thin REST wrapper)
- Full control over Telegram UX, memory, media routing, voice handling
- The hard parts (memory, media, voice) are the same regardless of loop choice
- Future option: expose tools as MCP server later if we want nullclaw-as-mux too

**Loop behavior:**
- Max iterations per turn: configurable (default 20)
- Parallel tool calls: supported (OpenAI returns multiple calls in one response)
- Timeout per iteration: configurable (default 120s)
- Cost tracking: log tokens used per turn
- Context assembly before each call: system prompt + long-term facts + agent
  summaries + compaction summaries + recent messages (see 4.5 Memory)
- On max iterations exceeded: inform user, save context, don't crash

### 4.9 Tool Definitions

The mux exposes tools to the OpenAI model via function calling. Each tool maps
to a `xnullclaw` CLI call or an internal operation.

**Agent communication tools:**
```
send_to_agent(agent: str, message: str) -> str
    Send a message to one agent via xnullclaw, return the response.

send_to_all(message: str) -> [{agent: str, response: str}]
    Send to ALL running agents in parallel. No enumeration needed.

send_to_some(agents: [str], message: str) -> [{agent: str, response: str}]
    Send to a named subset of agents in parallel.

send_file_to_agent(agent: str, file_path: str, message: str) -> str
    Deliver a file to an agent's inbox and send a message about it.
```

**Agent lifecycle tools:**
```
start_agent(agent: str) -> str
    Start an agent via xnullclaw. Returns status.

stop_agent(agent: str) -> str
    Stop an agent via xnullclaw. Returns status.

restart_agent(agent: str) -> str
    Restart an agent via xnullclaw. Returns status.

setup_agent(agent: str) -> str
    Create a new agent with default config.

clone_agent(agent: str, source: str) -> str
    Clone an agent from another.
```

**Agent discovery + config tools:**
```
list_agents() -> [{name: str, status: str, model: str, created: str}]
    List all agents and their current status.

agent_status(agent: str) -> {status: str, uptime: str, details: str}
    Detailed status of a specific agent.

get_agent_config(agent: str) -> object
    Read an agent's current configuration.

update_agent_config(agent: str, key: str, value: any) -> str
    Modify an agent's config.json. Requires restart to take effect.
```

**File tools:**
```
get_agent_file(agent: str, path: str) -> str
    Retrieve a file from an agent's workspace.

list_agent_files(agent: str, path: str) -> [str]
    List files in an agent's workspace directory.
```

**Voice tools:**
```
transcribe_audio(file_id: str) -> str
    Transcribe a Telegram voice/audio file via Whisper.
```

**Mux memory tools:**
```
remember(fact: str, importance: str) -> str
    Store a fact in the mux's long-term memory.

recall(query: str) -> str
    Search the mux's long-term memory.

get_conversation_summary(agent: str) -> str
    Get summary of recent conversation with an agent.
```

All agent-facing tools use the `xnullclaw` CLI with stdin/stdout for message passing.
The mux never directly constructs Docker commands or HTTP requests to agents.
The AI model decides which tools to call — the model IS the router.

## 5. Technical Specification

### 5.1 Tech Stack

| Component | Technology | Notes |
|-----------|-----------|-------|
| Language | **Go** | Pinned via asdf `.tool-versions` |
| Agentic loop | **Own implementation** (~30 LOC) | `for { call model → execute tools → repeat }` |
| OpenAI SDK | **`openai-go`** (official) | Responses API with function calling. Thin REST wrapper, minimal deps. |
| Speech-to-text | **OpenAI Whisper API** | Via `openai-go` SDK |
| Telegram | **Go Telegram Bot API** | Long-polling, media download/upload |
| Agent communication | **`xnullclaw` CLI** (stdin/stdout) | Wrapper handles HTTP/Docker internally |
| Agent lifecycle | **`xnullclaw` CLI** | start/stop/restart/setup/clone/status |
| Mux memory | **SQLite** (`~/.xnullclaw/.mux/`) | Tiered: messages, facts, agent_state, compactions |
| MCP SDK | **`modelcontextprotocol/go-sdk`** (optional, future) | For exposing mux tools to nullclaw agents later |

**Rejected alternatives:**
- OpenAI Agents SDK → Python/TS only, no official Go version
- nlpodyssey/openai-agents-go → community port, adds abstraction without clear value
- Google ADK Go → production-grade but Google-ecosystem-heavy
- CloudWeGo Eino → full LangChain-equivalent, overkill
- Nullclaw-as-mux via MCP → elegant but locks UX/memory to nullclaw's model

### 5.2 Directory Layout

```
xnullclaw/                          ← this repo
├── .tool-versions                  ← asdf: golang version
├── xnullclaw                       ← shell wrapper (bash)
├── mux/                            ← mux bot source (Go, runs on host)
│   ├── go.mod
│   ├── go.sum
│   ├── main.go                    ← entry point, Telegram polling + agentic loop
│   ├── loop/                      ← agentic loop (call model → tools → repeat)
│   │   └── loop.go
│   ├── bot/                       ← Telegram message handling + media
│   │   └── bot.go
│   ├── tools/                     ← tool definitions + executors (shells to xnullclaw)
│   │   ├── tools.go              ← OpenAI function schemas
│   │   ├── agents.go             ← send_to_agent, send_to_all, lifecycle
│   │   ├── files.go              ← send_file, get_file, list_files
│   │   └── memory.go             ← remember, recall, summaries
│   ├── voice/                     ← Whisper transcription + correction
│   │   └── voice.go
│   ├── memory/                    ← tiered memory system (SQLite)
│   │   ├── store.go              ← messages, facts, agent_state, compactions
│   │   ├── compact.go            ← auto-compaction + fact extraction
│   │   └── context.go            ← context assembly for each model call
│   └── config/                    ← mux configuration loader
│       └── config.go
├── PRD.md                          ← this document
└── nullclaw/                       ← upstream clone (gitignored)
```

**Runtime data** (`~/.xnullclaw/.mux/`):
```
~/.xnullclaw/.mux/
├── config.json          ← mux configuration
├── memory.db            ← SQLite (messages, facts, agent_state, compactions)
└── media/               ← temp storage for Telegram media downloads
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

### 5.4 Mux ↔ Agent Communication

The mux runs on the host and communicates with agents exclusively via the `xnullclaw` CLI.

**Message passing uses stdin/stdout** — not command-line arguments — to handle large messages,
multi-line content, and binary-safe payloads without shell escaping issues.

```bash
# Send a message (stdin → agent → stdout)
echo "check the server logs" | xnullclaw alice send
# stdout → JSON: {"status":"ok","response":"Server logs show..."}

# Pipe large content
cat analysis.txt | xnullclaw alice send
# stdout → JSON response

# Send to all running agents (fan-out, parallel)
echo "stop background tasks" | xnullclaw send-all
# stdout → JSON: {"alice":"Stopped.","bob":"No tasks.","carol":"Done."}

# Send to a subset
echo "analyze this data" | xnullclaw send-some alice,bob
# stdout → JSON: {"alice":"...","bob":"..."}

# Discover running agents
xnullclaw running --json
# → [{"name":"alice","status":"running","port":3001},{"name":"bob",...}]
```

**File passing convention:**

Agents operate within their workspace (`~/.xnullclaw/<agent>/data/workspace/`).
To pass files to an agent, the wrapper copies them into the agent's workspace
before sending the message. To retrieve files, the wrapper copies them out.

```bash
# Send a file to an agent (copies to workspace, then sends message referencing it)
xnullclaw alice send-file /path/to/report.pdf "Summarize this PDF"
# → copies report.pdf to ~/.xnullclaw/alice/data/workspace/inbox/report.pdf
# → sends message: "I placed report.pdf in your workspace inbox/. Summarize it."
# → stdout: agent response

# Send multiple files
xnullclaw alice send-files /path/to/data/ "Analyze all CSV files"
# → copies directory contents to workspace/inbox/data/

# Retrieve a file from agent workspace
xnullclaw alice get-file workspace/output/results.csv /local/path/
# → copies from agent workspace to local path

# List files in agent workspace
xnullclaw alice ls [path]
# → lists files in agent's workspace directory
```

**Convention:**
- `inbox/` — files placed by the mux for the agent to process (auto-created)
- `outbox/` — files the agent produces for the mux to retrieve (agent writes here)
- The wrapper handles all copy operations — the mux just calls `send-file` / `get-file`

**All lifecycle and discovery ops:**
```bash
xnullclaw alice start --mux       # start agent in mux mode
xnullclaw alice stop               # stop agent
xnullclaw alice status             # detailed status
xnullclaw list --json              # all agents as JSON
xnullclaw running --json           # running agents as JSON
```

The wrapper handles all Docker details internally: container naming, port assignment,
health checks, HTTP requests. The mux only sees stdin/stdout/exit-codes.

### 5.5 Agent Config in Mux Mode

When an agent runs in mux mode (`xnullclaw <agent> start --mux`), the wrapper:

- Starts the container with an internal port (localhost-only, auto-assigned)
- Sets `NULLCLAW_GATEWAY_HOST=0.0.0.0` inside the container
- Sets `require_pairing: false` (localhost is the trust boundary — only the wrapper talks to it)
- Removes `channels.telegram` (mux handles Telegram)
- Everything else unchanged (model, autonomy, tools, memory, etc.)

### 5.6 Authentication Between Mux and Agents

Agents in mux mode bind to localhost-only ports. Only the `xnullclaw` wrapper
(running on the same host) can reach them. No external network access to agents.

The wrapper passes a fixed Bearer token per agent when calling `/webhook`.
Since this is a single-user personal system, per-user session isolation is not needed.

### 5.7 Rich Media Handling

Agents can produce and consume any type of file — not just text. The mux must bridge
these between Telegram and agent workspaces.

**Inbound (Telegram → agent):**

| Telegram type | Mux action |
|---------------|-----------|
| Voice message | Transcribe via Whisper, send text to agent (and optionally the audio file) |
| Photo | Download to agent's `inbox/`, send message: "Image at inbox/photo_001.jpg" |
| Document (PDF, etc.) | Download to `inbox/`, send message referencing path |
| Video | Download to `inbox/`, send message referencing path |
| Audio file | Download to `inbox/`, send message referencing path |
| Location | Convert to text: "Location: lat, lon" |
| Sticker/GIF | Ignore or convert to description |

```bash
# Wrapper command for file delivery
xnullclaw alice send-file /tmp/downloaded_photo.jpg "User sent this photo. Describe it."
```

**Outbound (agent → Telegram):**

Agents can produce files in their `outbox/` directory. The mux must detect these
and send them to Telegram as the appropriate media type.

| File type | Telegram action |
|-----------|----------------|
| `.jpg`, `.png`, `.gif`, `.webp` | Send as photo (`sendPhoto`) |
| `.mp3`, `.ogg`, `.wav`, `.m4a` | Send as audio (`sendAudio`) or voice (`sendVoice`) |
| `.mp4`, `.mov`, `.webm` | Send as video (`sendVideo`) |
| `.pdf`, `.csv`, `.txt`, `.zip`, any other | Send as document (`sendDocument`) |

**Outbox convention:**

Agent responses can reference files. The mux parses the response for file references
and sends them as Telegram media attachments alongside the text response.

```
Agent response: "Here's the chart you asked for. See outbox/chart.png"
→ Mux sends: [alice] Here's the chart you asked for.
→ Mux sends: 📎 chart.png (as Telegram photo)
```

The wrapper provides:
```bash
# List files in agent outbox
xnullclaw alice ls outbox/

# Retrieve a file
xnullclaw alice get-file outbox/chart.png /tmp/chart.png

# Clean outbox after retrieval
xnullclaw alice clean-outbox
```

**Media routing in multi-agent scenarios:**

When broadcasting or coordinating across agents, the mux tracks which files belong
to which agent's response. Each file is labeled with its source agent when sent to Telegram.

### 5.8 Response Timeout Handling

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
Mux AI: → calls send_to_all("stop all background tasks")
       (wrapper discovers running agents, fans out in parallel)
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

### 6.6 Rich Media Flow

```
User:  📷 (sends a photo of a whiteboard)
Mux:   → downloads photo from Telegram
       → calls send_file_to_agent("alice", "/tmp/photo.jpg", "Transcribe this whiteboard")
       (wrapper copies to alice's inbox/, sends message)
Alice: → reads inbox/photo.jpg, transcribes
       ← "The whiteboard contains: 1) API redesign... 2) ..."
User sees: [alice] The whiteboard contains: 1) API redesign... 2) ...
```

```
User:  "bob, generate a chart of last month's sales"
Mux AI: → calls send_to_agent("bob", "generate a chart of last month's sales")
Bob:   → generates chart, writes to outbox/sales_chart.png
       ← "Done. Chart saved to outbox/sales_chart.png"
Mux:   → detects file reference, retrieves outbox/sales_chart.png
       → sends to Telegram as photo with caption
User sees:
  [bob] Here's last month's sales chart.
  📎 sales_chart.png (as inline photo)
```

## 7. Implementation Phases

### Phase 0 — Foundation (DONE)
- [x] xnullclaw wrapper script
- [x] Hardened Docker setup
- [x] Multi-agent lifecycle (setup/start/stop/clone/destroy)
- [x] Security review of nullclaw
- [x] No-port-by-default mode (Telegram outbound polling)

### Phase 1 — Mux Core
- [ ] Go project scaffold (`mux/`, `.tool-versions` with asdf golang)
- [ ] Telegram bot connection (long-polling, text messages)
- [ ] OpenAI integration with function calling (tool use)
- [ ] xnullclaw wrapper: add `send` (stdin/stdout), `running --json`, `--mux` flag
- [ ] Core tools: `send_to_agent`, `list_agents`, `agent_status`
- [ ] Basic intent routing (AI determines agent vs mux)
- [ ] Response formatting with agent prefixes

### Phase 2 — Memory + Stability
- [ ] SQLite memory system (messages, facts, agent_state, compactions tables)
- [ ] Rolling window context assembly
- [ ] Auto-compaction with fact extraction
- [ ] Agent state tracking (updated after every interaction)
- [ ] `remember` / `recall` tools
- [ ] Graceful restart with full memory recovery
- [ ] Compaction log for "what happened last week" queries

### Phase 3 — Voice + Media
- [ ] Voice message handling (download from Telegram)
- [ ] Whisper transcription integration
- [ ] Transcription correction dictionary
- [ ] Show-before-act flow (display transcription, then process)
- [ ] xnullclaw wrapper: add `send-file`, `get-file`, `ls`, `clean-outbox`
- [ ] Inbound media: photos, documents, audio, video → agent inbox/
- [ ] Outbound media: agent outbox/ → Telegram (auto-detect type, send as photo/audio/document/video)
- [ ] File reference detection in agent responses

### Phase 4 — Agent Lifecycle via Mux
- [ ] Tools: `start_agent`, `stop_agent`, `restart_agent`
- [ ] Tools: `setup_agent`, `clone_agent`
- [ ] `update_agent_config` + auto-restart
- [ ] `get_agent_config` tool
- [ ] Proactive health monitoring and alerting

### Phase 5 — Advanced Orchestration
- [ ] `send_to_all` — broadcast to all running agents (no enumeration)
- [ ] `send_to_some` — selective multi-agent dispatch
- [ ] Response synthesis (mux summarizes/combines agent outputs)
- [ ] Agent role awareness (mux knows what each agent is good at)
- [ ] Auto-routing based on agent specialization
- [ ] Multi-agent file passing (agent A produces → agent B consumes)

### Phase 6 — Polish
- [ ] Telegram command fallbacks (`/dm`, `/list`, `/status`, etc.)
- [ ] Error recovery (agent crash detection, auto-restart)
- [ ] Timeout handling with Telegram typing indicators
- [ ] Voice vocabulary learning over time
- [ ] Periodic fact pruning and memory self-maintenance
- [ ] Mux self-update capability

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
