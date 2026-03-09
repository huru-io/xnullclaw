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
- `xnullclaw <agent> drain` — return any buffered unsolicited output since last drain (cron results, alerts, heartbeats)
- `xnullclaw <agent> watch` — block and stream agent output as it arrives (for long-running monitoring)
- `xnullclaw <agent> costs [--json] [--today|--month]` — read agent's costs.jsonl, return summary
- `xnullclaw <agent> config get [key]` — read agent config (full or specific key)
- `xnullclaw <agent> config set <key> <value>` — set a config value (see supported keys below)

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
- Pass through messages transparently when clearly directed at an agent (see passthrough rules below)
- Handle ambiguous intent by asking for clarification or using context
- Understand follow-up messages belong to the same agent conversation
- Recognize when the user switches topics/agents mid-conversation

**Passthrough rules — mux as transparent relay:**

When the user addresses an agent directly, the mux is a **transparent pipe** — it
forwards the message to the agent with minimal intervention. The mux does NOT
rephrase, summarize, or add its own interpretation.

However, the mux MAY apply lightweight corrections before forwarding:
- Fix obvious spelling/typos (e.g., "chekc" → "check")
- Fix grammar/structure that would confuse the agent (broken sentences, missing words)
- Correct technical terms the mux knows the user meant (from learned patterns)
- These corrections are **silent** — the mux does not announce them

The mux MUST NOT:
- Rephrase the user's intent or add instructions the user didn't give
- Filter or censor the user's message
- Add context the user didn't ask for (unless a passthrough rule says otherwise)
- Hold back a message because it "seems wrong" — the user decides, not the mux

**Passthrough rules are runtime-configurable.** The user can tweak them at any time:

```
User: "From now on, always add 'be concise' when you forward to alice"
→ mux stores rule: alice passthrough append "be concise"

User: "Stop correcting my spelling when talking to bob"
→ mux stores rule: bob passthrough corrections=off

User: "When I send code to carol, don't touch it at all — raw passthrough"
→ mux stores rule: carol passthrough raw=true (zero modification)

User: "For all agents, translate my Spanish to English before forwarding"
→ mux stores rule: global passthrough translate=es→en

User: "Remove that translation rule"
→ mux removes the rule
```

Rules are stored in the mux's long-term memory (`facts` table) and loaded into the
system prompt so the model applies them consistently. Rules can be:
- **Per-agent**: apply only when forwarding to a specific agent
- **Global**: apply to all agent-directed messages
- **Typed**: apply only to certain message types (code, voice transcriptions, etc.)

**Examples:**
```
User: "Ask alice to check the server logs"
→ mux routes to alice: "check the server logs"

User: "alice chekc the sever logs pls"
→ mux routes to alice: "check the server logs pls" (silent typo fix)

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
- Prefix agent responses with emoji badge + name: `🍎 alice: response text...`
- Track which agent the user is currently "talking to" for follow-ups
- Handle agent errors/timeouts gracefully: `🍎 alice: (offline)` or `🍎 alice: (timeout — still thinking)`

**The mux MUST support these interaction patterns:**
- **One-to-one**: user talks to a single agent (most common)
- **Broadcast**: user sends a message to all running agents — no enumeration required
- **Selective**: user sends to a subset of agents ("ask alice and bob to...")
- **Orchestrated**: mux decides which agent(s) should handle a request based on their roles/capabilities

### 4.2.1 Proactive Agent Output

Agents are not purely request/response — they can produce output autonomously from
internal heartbeats, cron jobs, monitoring alerts, scheduled tasks, or background work.

**ALL agent output MUST flow through the mux to Telegram.** No agent output should be
silently lost in container logs.

**How it works:**

Nullclaw agents write logs to stdout inside their Docker containers. The wrapper
tails the container logs in real-time and filters for structured output lines
(JSON events from cron completions, heartbeat pings, alerts, etc.).

The mux runs a background goroutine per running agent that watches for output:

```bash
# Real-time stream (mux uses this — one goroutine per agent, long-lived)
xnullclaw alice watch
# → tails container logs, filters for structured output events
# → prints each event as JSON line to stdout as it arrives
# → blocks indefinitely until agent stops or watch is killed
# Example output:
# {"type":"cron","task":"backup","result":"3 files archived","ts":"2026-03-09T14:00:00Z"}
# {"type":"alert","message":"API returned 503","severity":"high","ts":"2026-03-09T14:05:12Z"}

# One-shot drain (useful for manual checks or catch-up after mux restart)
xnullclaw alice drain
# → returns any buffered unsolicited output since last drain, then exits
```

**The wrapper implements `watch` using `docker logs --follow --since`**, tracking
the last-read timestamp to avoid duplicates. Events are identified by a prefix
convention in agent log output (e.g., lines starting with `[EVENT]` JSON).

**The mux starts a `watch` goroutine for every running agent at mux startup.**
When a new agent starts, a new watcher is spawned. When an agent stops, the
watcher exits cleanly. This gives near-instant relay (<1s) of agent output.

**The mux MUST:**
- Monitor all running agents for unprompted output (heartbeats, cron results, alerts)
- Relay ALL agent output to Telegram with the agent's emoji badge:
  ```
  🍎 alice: [cron] Daily backup completed. 3 files archived, 12 MB total.
  🍊 bob: [alert] API endpoint returned 503 — monitoring.
  🍋 carol: [heartbeat] Build pipeline healthy. Last run: 2m ago.
  ```
- Tag the output type when detectable: `[cron]`, `[alert]`, `[heartbeat]`, `[task]`
- Batch rapid-fire output to avoid Telegram spam (e.g., buffer 5s, send as one message)
- Allow the user to mute/unmute agents: "mute carol's heartbeats" or `/mute carol heartbeat`
- Allow per-agent output filters: only relay alerts, suppress heartbeats, etc.

**Output types:**
| Type | Source | Default relay |
|------|--------|---------------|
| Cron results | Scheduled tasks completing | Always relay |
| Alerts | Monitoring / threshold triggers | Always relay (high priority) |
| Heartbeats | Periodic health pings | Muted by default (opt-in) |
| Task progress | Long-running work updates | Relay (batched) |
| Errors | Agent crashes, tool failures | Always relay (high priority) |

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

**The mux MUST be able to:**
- Create new agents from scratch (`xnullclaw <agent> setup`) — assigns next emoji, generates config
- Configure new agents fully: model, provider, temperature, system prompt, autonomy level, tools, memory backend
- Clone agents from existing ones (`xnullclaw <agent> clone <source>`) — new identity, independent config/data
- Modify any agent configuration (edit config.json fields) with auto-restart
- Destroy agents (with user confirmation)

**Agent creation flow (mux-driven):**
```
User: "Create a new agent for code review, call it dave"
Mux:  → setup_agent("dave")
      → update_agent_config("dave", "agents.defaults.system_prompt", "You are a senior code reviewer...")
      → update_agent_config("dave", "agents.defaults.model.primary", "openai/gpt-4o")
      → update_agent_config("dave", "autonomy.level", "supervised")
      → start_agent("dave")
User sees: 🍇 dave is ready. Configured as a code review agent with GPT-4o, supervised mode.

User: "Actually, clone alice's setup for a new agent called eve"
Mux:  → clone_agent("eve", "alice")
      → start_agent("eve")
User sees: 🍓 eve is ready. Cloned from 🍎 alice's config. Independent data.
```

### 4.4 Voice Input (Whisper)

**The mux MUST:**
- Accept Telegram voice messages and audio files
- Transcribe audio using OpenAI Whisper API
- Process the transcription as if it were a text message
- Apply intelligent correction to transcription artifacts (names, technical terms, agent names the user commonly says)
- Show the transcription to the user before acting: `🎙️ heard: "ask alice to check the server logs"`
- Allow the user to correct: replying "no, I said BOB" re-routes accordingly

**The mux SHOULD:**
- Learn the user's speech patterns over time (common words, agent names, technical jargon)
- Maintain a custom vocabulary/corrections dictionary

### 4.4.1 Text-to-Speech Output (TTS)

The mux can speak back to the user via OpenAI TTS. This is opt-in and complementary
to text — **text is always sent**, TTS is an additional audio message.

**The mux MUST:**
- Generate voice responses via OpenAI TTS API when TTS mode is enabled
- Always send the text version of the response (TTS is additive, never a replacement)
- Send both: first the text message, then the voice note (Telegram voice message)
- Allow enabling/disabling TTS: "speak to me" / "stop speaking" / config toggle
- Use a configurable voice, changeable at any time via tool or voice command
- Supported OpenAI TTS voices: alloy, ash, ballad, coral, echo, fable, onyx, nova, sage, shimmer
- Voice can be changed by the user: "change your voice to coral" or "use the ash voice"
- The mux itself can select a voice contextually (e.g., different tone for alerts vs casual chat)

**TTS applies to:**
- Mux's own responses (summaries, status updates, orchestration replies)
- Agent responses relayed by the mux (spoken version of what the agent said)

**TTS does NOT apply to:**
- Raw file deliveries, code blocks, or structured data (these are text-only)
- Heartbeats, cron output, or batched status updates (too noisy for voice)

**Config:**
```json
{
  "voice": {
    "tts_enabled": false,
    "tts_voice": "nova",
    "tts_for_agents": true,
    "tts_max_length": 4000
  }
}
```

When `tts_max_length` is exceeded, only the text is sent with a note: "Response too
long for voice. Read above."

### 4.4.2 Agent Audio/Voice Passthrough

When an agent produces audio or voice content (voice notes, generated music, audio
analysis results, TTS from the agent itself), the mux MUST pass it through to Telegram
as-is — the user hears/sees the original audio from the agent.

**The mux MUST:**
- Detect audio files in agent responses (`outbox/*.ogg`, `*.mp3`, `*.wav`, etc.)
- Send them as Telegram voice messages or audio files (preserving format)
- Also include any text the agent sent alongside the audio
- The mux remains aware of what the agent sent (for context/memory) but does not
  re-encode, transcribe, or alter the agent's audio

**Example flow:**
```
User: "bob, read this article aloud"
Bob:  → generates outbox/reading.ogg (TTS of article)
     ← "Here's the article read aloud. See outbox/reading.ogg"
Mux:  → sends to Telegram:
       🍊 bob: Here's the article read aloud.
       🎤 (voice message: reading.ogg)
```

**The mux logs the content** (stores text description in memory/agent_state)
but the audio itself is delivered untouched to the user.

### 4.5 Conversation Memory and Long-Term Stability

The mux is an always-on 24/7 orchestrator. Its memory system must be designed for
**indefinite operation** — months or years of continuous use without degradation.

#### 4.5.1 Memory Tiers

| Tier | What | Storage | Token budget | Lifecycle |
|------|------|---------|-------------|-----------|
| **System prompt** | Role, tools, passthrough rules, agent identities | In-memory | ~4k tokens (fixed) | Rebuilt each turn |
| **Agent summaries** | Per-agent: status, current task, last interaction, role | SQLite `agent_state` | ~200 tokens/agent (~2k for 10 agents) | Updated after every agent interaction |
| **Long-term facts** | Important facts, decisions, preferences, rules | SQLite `facts` | ~4k tokens (top-K by relevance) | Persistent, scored, pruned |
| **Compaction summaries** | Condensed blocks of past conversation | SQLite `compactions` | ~4k tokens (most recent N blocks) | Append-only, oldest dropped from context |
| **Rolling window** | Recent messages in full detail | SQLite `messages` + in-memory | Remainder (~110k tokens for 128k model) | FIFO, compacted when evicted |

**Total context budget**: managed explicitly. Each tier has a token ceiling. The rolling
window gets whatever remains after the fixed tiers are assembled. This guarantees the
model never sees a truncated or overflowed context.

```
┌─────────────────────────────────────────────┐
│ Context window (e.g., 128k tokens)          │
├──────────────┬──────────────────────────────┤
│ System       │ ~4k   (fixed)               │
│ Agent states │ ~2k   (scales with agents)   │
│ Facts        │ ~4k   (top-K retrieved)      │
│ Compactions  │ ~4k   (last N blocks)        │
│ Messages     │ ~114k (rolling window, FIFO) │
└──────────────┴──────────────────────────────┘
```

Token counts are **measured, not estimated** — the mux counts tokens (via tiktoken or
the model's tokenizer) when assembling context. If agent count grows and pushes the
agent state tier over budget, the mux summarizes less-active agents more aggressively.

#### 4.5.2 Message Types in the Rolling Window

Not all messages are equal. The rolling window contains:

| Message type | Source | Volume | Retention priority |
|---|---|---|---|
| User messages | Telegram input | Low (human-paced) | High — keep longest |
| Mux responses | Model output | Low | High |
| Agent responses | `send_to_agent` results | Medium | Medium — summarize large ones |
| Agent proactive output | `watch` stream (cron, alerts) | **High** (can be continuous) | Low — compact aggressively |
| Tool call/result pairs | Agentic loop internals | High | Low — compact after turn ends |

**Critical insight**: agent proactive output (heartbeats, cron results) can generate
far more volume than user conversation. Without special handling, 10 agents with
5-minute heartbeats = 120 messages/hour flooding the window.

**Solution — two-stream architecture:**

The rolling window has two streams with independent budgets:

```
Rolling window (~114k tokens):
├── Conversation stream (90%): user ↔ mux ↔ agent request/response
└── Background stream (10%):   agent proactive output (cron, alerts, heartbeats)
```

Background stream messages are compacted much more aggressively:
- Heartbeats: only keep the latest per agent (replace, don't accumulate)
- Cron results: keep for 1 compaction cycle, then summarize
- Alerts: keep until acknowledged, then compact
- Errors: keep until resolved

This prevents autonomous agent chatter from drowning out the user's conversation.

#### 4.5.3 Compaction Engine

Compaction is the process of evicting old messages from the rolling window without
losing important information. It runs as a **background operation** between user turns.

**Trigger**: compaction fires when the rolling window exceeds 80% of its token budget.
This gives headroom — compaction happens before the limit, not at it.

**Compaction steps (single cycle):**

```
1. SELECT oldest block of messages (e.g., oldest 25% of rolling window)
2. In a SEPARATE model call (cheap, fast model like gpt-4o-mini):
   a. Summarize the block into 1-3 paragraphs → store in `compactions` table
   b. Extract facts (decisions, preferences, rules) → upsert into `facts` table
   c. Update agent_state for any agents mentioned → upsert into `agent_state`
3. DELETE the original messages from the rolling window
4. If compactions table exceeds its token budget, drop the oldest compaction
```

**Compaction model**: uses a cheaper/faster model than the main mux model.
Configurable — default `gpt-4o-mini`. This keeps compaction costs low since it
runs frequently.

**Compaction timing:**
- After each user turn completes (non-blocking, background goroutine)
- Periodically if no user activity (every 30 min during idle periods)
- Forced when token budget is at 90% (synchronous, blocks next turn)

**What compaction extracts:**

Facts are typed and tagged:
```sql
CREATE TABLE facts (
    id        INTEGER PRIMARY KEY,
    type      TEXT NOT NULL,  -- 'preference', 'decision', 'rule', 'knowledge', 'pattern'
    content   TEXT NOT NULL,  -- the fact itself
    source    TEXT,           -- which compaction/interaction produced it
    agent     TEXT,           -- related agent (nullable)
    score     REAL DEFAULT 1.0,  -- relevance score (decays over time)
    created   DATETIME,
    accessed  DATETIME,       -- last time this fact was included in context
    access_count INTEGER DEFAULT 0
);
```

#### 4.5.4 Fact Management (Long-Term Knowledge)

Facts are the mux's permanent knowledge. They survive compaction, restarts, and
months of operation. But they must be actively managed or they become noise.

**Fact lifecycle:**

```
  Created (score=1.0)
      │
      ├─── accessed by context assembly → score boosted, access_count++
      │
      ├─── not accessed for 30 days → score decays (×0.9 per week)
      │
      ├─── score < 0.1 → candidate for pruning review
      │
      └─── pruning review (periodic model call):
           ├── still relevant → score reset to 0.5
           ├── outdated → deleted
           └── contradicts newer fact → merged or deleted
```

**Fact retrieval for context assembly:**

Each turn, the mux retrieves facts relevant to the current message. This is NOT
"dump all facts" — it's a retrieval step:

1. **Keyword match**: extract keywords from current message, match against facts
2. **Agent match**: if message targets an agent, include that agent's facts
3. **Type priority**: `rule` and `preference` facts always included (these are
   the user's standing instructions). `knowledge` and `pattern` only if relevant.
4. **Score sort**: among matches, pick top-K by score (budget: ~4k tokens)
5. **Always include**: passthrough rules, agent roles, active user preferences

This keeps the fact injection focused. With 500 stored facts, maybe 20-30 enter
context for a given turn.

**Deduplication**: before inserting a new fact, check for semantic overlap with
existing facts (simple: substring/keyword match. Future: embedding similarity).
Merge or replace rather than accumulate duplicates.

#### 4.5.5 Agent State Tracking

Each agent has a persistent summary in `agent_state`:

```sql
CREATE TABLE agent_state (
    agent       TEXT PRIMARY KEY,
    emoji       TEXT,
    status      TEXT,           -- 'running', 'stopped', 'error'
    role        TEXT,           -- what this agent does (user-defined or inferred)
    model       TEXT,           -- current model
    current_task TEXT,          -- what it's working on right now
    last_message TEXT,          -- last message sent to it
    last_response TEXT,         -- last response (truncated to ~500 chars)
    last_interaction DATETIME,
    error       TEXT,           -- last error if any
    updated     DATETIME
);
```

Updated **after every interaction** with the agent (send, lifecycle change, config
change, proactive output). The mux always has a current picture of all agents
without needing to query them.

**Agent state is always included in context** (it's small — ~200 tokens per agent).
This means the model can answer "what is alice doing?" instantly from memory.

#### 4.5.6 Compaction Log (Historical Memory)

The compaction log is an append-only archive of summarized conversation blocks:

```sql
CREATE TABLE compactions (
    id         INTEGER PRIMARY KEY,
    period_start DATETIME,
    period_end   DATETIME,
    summary    TEXT,            -- 1-3 paragraph summary of the conversation block
    agents     TEXT,            -- comma-separated agents mentioned
    token_count INTEGER,
    created    DATETIME
);
```

Used for:
- "What happened yesterday?" → query by date range
- "What did I ask bob to do last week?" → query by agent + date
- Context assembly: include the most recent N compaction summaries

**Retention**: compaction summaries are never deleted from SQLite. Only the most
recent N are included in context (budget: ~4k tokens). Older ones remain queryable
via the `recall` tool.

#### 4.5.7 Restart Recovery

On mux restart (crash, reboot, manual restart):

1. Load config from `~/.xnullclaw/.mux/config.json`
2. Open SQLite `memory.db` — all tiers are intact
3. Rebuild working context:
   - Load agent_state → know all agents and their status
   - Load recent messages from rolling window → resume conversation
   - Load facts → standing knowledge restored
   - Load recent compactions → recent history context
4. Start `watch` goroutines for all running agents
5. Resume Telegram polling
6. First model call includes everything — the model picks up where it left off

**No amnesia.** The mux should behave as if it never restarted. If the restart
was long (hours), the mux can note: "I was restarted. Catching up on agent status..."
and drain any buffered agent output.

#### 4.5.8 Scaling Boundaries

Designed to work indefinitely, but with known limits:

| Dimension | Comfortable range | Degradation mode |
|-----------|------------------|-----------------|
| Agents | 1–20 | >20: summarize inactive agents more aggressively |
| Facts | 1–1000 | >1000: increase pruning frequency, tighter relevance threshold |
| Messages/day | 1–500 | >500: compact more aggressively, shorter rolling window |
| Compactions | 1–10,000 | >10k: no issue (SQLite handles this), just not all in context |
| SQLite DB size | <100 MB | >100 MB: vacuum periodically, archive old compactions |
| Uptime | Indefinite | Memory is all in SQLite — process memory stays bounded |

**The mux MUST:**
- Never lose important facts during compaction
- Survive restarts without amnesia (all tiers persist in SQLite)
- Answer "what happened yesterday/last week?" from compaction logs
- Track user preferences learned over time ("I prefer alice for code tasks")
- Self-maintain: periodically review and prune stale facts (weekly model call)
- Handle context overflow gracefully — degrade by dropping old summaries, not crashing
- Keep Go process memory bounded — no unbounded in-memory growth

**The mux MUST maintain:**
- Its own conversation history with the user (persistent, compacted, across restarts)
- A summary of what each agent is currently doing / last did
- Knowledge of each agent's configuration (model, role, capabilities)
- Context about recent interactions (which agent was last addressed, ongoing tasks)
- User preferences and patterns learned over time
- Passthrough rules and standing instructions

**The mux uses this memory to:**
- Route follow-up messages correctly without explicit agent naming
- Answer "what is alice doing?" without asking alice
- Provide situational awareness: "you asked bob to research X an hour ago, he's probably done"
- Avoid re-asking agents for information the mux already knows
- Maintain personality consistency and user rapport across days/weeks
- Apply standing rules and preferences consistently even after compaction

### 4.6 Agent Configuration Management

**The mux MUST be able to modify agent configs when instructed:**
- Change an agent's model: "switch alice to claude-sonnet"
- Change temperature: "make bob more creative"
- Change autonomy level: "give carol full autonomy"
- Change system prompt: "tell alice she's a security researcher now"
- These changes persist (written to the agent's `config.json`)
- Agent restart required for config changes to take effect (mux handles this automatically)

**Wrapper `config set` — pragmatic supported keys:**

The wrapper supports a flat set of common config mutations. No arbitrary JSON path
walking — just the knobs people actually turn:

```bash
xnullclaw alice config set model "openai/gpt-4o"
xnullclaw alice config set temperature 0.9
xnullclaw alice config set autonomy supervised
xnullclaw alice config set system_prompt "You are a security researcher..."
xnullclaw alice config set max_actions_per_hour 50
xnullclaw alice config set memory_backend sqlite
xnullclaw alice config set cost_daily_limit 25
xnullclaw alice config set cost_enabled true

xnullclaw alice config get model
# → openai/gpt-4o
xnullclaw alice config get                    # dump full config
```

**Supported keys and their JSON paths:**

| Key | JSON path | Type |
|-----|-----------|------|
| `model` | `agents.defaults.model.primary` | string |
| `temperature` | `default_temperature` | float |
| `autonomy` | `autonomy.level` | string (supervised/autonomous/full/yolo) |
| `system_prompt` | `agents.defaults.system_prompt` | string |
| `max_actions_per_hour` | `autonomy.max_actions_per_hour` | int |
| `memory_backend` | `memory.backend` | string |
| `cost_enabled` | `cost.enabled` | bool |
| `cost_daily_limit` | `cost.daily_limit_usd` | float |
| `cost_monthly_limit` | `cost.monthly_limit_usd` | float |

The wrapper reads `config.json`, modifies the specific field, writes it back.
If a key is not in the table above, the wrapper rejects it with an error.
This keeps it simple and avoids accidentally breaking config structure.

### 4.7 Cost Tracking and Awareness

The mux and its agents consume paid API resources. The mux MUST track costs across
the entire system and make them visible — no surprise bills.

**Two cost domains:**

| Domain | What | How tracked |
|--------|------|-------------|
| **Mux costs** | Agentic loop calls, compaction, Whisper, TTS, fact pruning | Mux tracks directly from OpenAI API responses (tokens + pricing) |
| **Agent costs** | Each agent's provider API calls | Nullclaw writes to `costs.jsonl` inside the container — mapped to `~/.xnullclaw/<agent>/data/.nullclaw/state/costs.jsonl` |

**Mux cost tracking (SQLite):**

```sql
CREATE TABLE costs (
    id          INTEGER PRIMARY KEY,
    timestamp   DATETIME NOT NULL,
    category    TEXT NOT NULL,  -- 'loop', 'compaction', 'whisper', 'tts', 'pruning'
    model       TEXT,           -- 'gpt-5.4', 'gpt-4o-mini', 'whisper-1', 'tts-1'
    agent       TEXT,           -- which agent this was for (nullable)
    input_tokens  INTEGER,
    output_tokens INTEGER,
    cost_usd    REAL NOT NULL
);
```

Recorded after every API call. The mux knows the exact cost of every operation.

**Agent cost reading:**

The wrapper reads each agent's `costs.jsonl` file:
```bash
xnullclaw alice costs              # human-readable summary
xnullclaw alice costs --json       # machine-readable for mux
xnullclaw alice costs --today      # today's costs only
xnullclaw alice costs --month      # this month
```

**Mux cost aggregation:**

The mux can produce a full system cost picture on demand:

```
User: "how much is all this costing me?"

Mux:  📊 Cost report (today):

      Mux operations:           $0.42
        Agentic loop (12 turns): $0.31
        Compaction (3 cycles):   $0.04
        Whisper (2 transcripts): $0.02
        TTS (4 messages):        $0.05

      Agents:
        🍎 alice:  $1.20  (47 requests, gpt-4o)
        🍊 bob:    $0.85  (23 requests, gpt-4o-mini)
        🍋 carol:  $0.00  (stopped)

      Total today:  $2.47
      This month:   $38.12
      Budget:       $100/month (38% used)
```

**Budget alerts:**

The mux monitors total system costs and can warn proactively:

- **Warning threshold** (configurable, default 80%): "Heads up — we're at $80 of your $100 monthly budget."
- **Hard limit** (optional): mux refuses to start new agent interactions, tells user
- **Per-agent limits**: "alice has used $25 today, which seems high. Want me to check what she's doing?"
- **Anomaly detection**: if an agent's cost spikes (e.g., stuck in a tool loop), alert immediately

**Mux config for costs:**
```json
{
  "costs": {
    "track": true,
    "monthly_budget_usd": 100,
    "daily_budget_usd": 10,
    "warn_at_percent": 80,
    "per_agent_daily_limit_usd": 25,
    "report_currency": "USD"
  }
}
```

**Nullclaw agent-side config** (generated by wrapper):
```json
{
  "cost": {
    "enabled": true,
    "daily_limit_usd": 25,
    "monthly_limit_usd": 100,
    "warn_at_percent": 80
  }
}
```

**Agent cost consolidation (periodic):**

Nullclaw has a `CostTracker` module (`src/cost.zig`) that writes to `costs.jsonl`,
but it needs `cost.enabled=true` in the agent config. The wrapper's `generate_config()`
MUST set this by default for all new agents.

The mux runs a background consolidation loop:
1. Every 15 minutes (configurable), call `xnullclaw <agent> costs --json --since <last_check>`
   for each running agent
2. Store the agent cost snapshots in the mux's `costs` table (with `category='agent'`)
3. Check against per-agent and total budgets, alert if thresholds crossed
4. On-demand: when user asks for costs, fetch fresh data immediately

This means the mux always has a reasonably current cost picture without hammering
the agents. Fresh data is at most 15 minutes stale, or instant when explicitly asked.

The mux enforces budgets from its side (it can stop sending messages to expensive
agents), AND nullclaw enforces from the agent side (agents refuse to call providers
when over budget). Belt and suspenders.

### 4.8 Telegram Commands (fallback for explicit control)

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
| `/costs [agent]` | Show cost report (today, or per agent) |
| `/budget [limit]` | Show or set monthly budget |

These commands exist as a reliable fallback. In normal use, the AI handles intent from natural language.

### 4.9 Agentic Loop Architecture

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
- No MCP for the mux at this stage — keep it simple

**Loop behavior:**
- Max iterations per turn: configurable (default 20)
- Parallel tool calls: supported (OpenAI returns multiple calls in one response)
- Timeout per iteration: configurable (default 120s)
- Cost tracking: log tokens used per turn
- Context assembly before each call: system prompt + long-term facts + agent
  summaries + compaction summaries + recent messages (see 4.5 Memory)
- On max iterations exceeded: inform user, save context, don't crash

### 4.10 Tool Definitions

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

speak(text: str) -> str
    Generate TTS audio via OpenAI TTS and send as Telegram voice message.
    Text version is always sent alongside. Returns "sent".

set_voice(voice: str) -> str
    Change the TTS voice. Options: alloy, ash, ballad, coral, echo, fable,
    onyx, nova, sage, shimmer. Persists in config. Returns confirmation.

toggle_tts(enabled: bool) -> str
    Enable or disable TTS output. Returns new state.
```

**Agent output monitoring tools:**
```
drain_agent(agent: str) -> str
    Return any buffered unsolicited output from an agent since last drain.

drain_all() -> [{agent: str, output: str}]
    Drain unsolicited output from ALL running agents.

mute_agent(agent: str, types: [str]) -> str
    Mute specific output types from an agent (heartbeat, cron, etc.).

unmute_agent(agent: str, types: [str]) -> str
    Unmute specific output types from an agent.
```

**Persona tools:**
```
set_persona(field: str, value: str) -> str
    Update persona: name, personality, communication_style, language,
    extra_instructions. Persists to config.

get_persona() -> object
    Return current persona settings.
```

**Cost tools:**
```
get_costs(period: str) -> object
    Get full system cost report. Period: "today", "week", "month", "all".
    Returns mux costs + per-agent costs breakdown.

get_agent_costs(agent: str, period: str) -> object
    Get cost details for a specific agent.

set_budget(scope: str, limit_usd: float, period: str) -> str
    Set budget limit. Scope: "total", agent name. Period: "daily", "monthly".
```

**Passthrough rule tools:**
```
set_passthrough_rule(scope: str, rule: str) -> str
    Add or update a passthrough rule. Scope: agent name or "global".
    Example: set_passthrough_rule("alice", "append 'be concise'")

remove_passthrough_rule(scope: str, rule_id: str) -> str
    Remove a passthrough rule by ID.

list_passthrough_rules() -> [{scope: str, rule: str, id: str}]
    List all active passthrough rules.
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

### 4.11 Agent Identity

Every agent has a **display identity** — an emoji badge and a short, speakable name.
This makes it instant to recognize who's talking in the Telegram chat, and easy to
address agents by voice or text.

**Identity components:**

| Component | Purpose | Example |
|-----------|---------|---------|
| **Emoji badge** | Visual identifier — one colorful emoji from a curated pool | 🍎 |
| **Display name** | Short, easy-to-say name — the "friendly" name | alice |
| **Internal name** | Container/folder name — may match display name, doesn't have to | alice |

**Emoji pool — colorful, distinct, pronounceable:**

Drawn from easily distinguishable emoji groups: fruits, vegetables, flowers, animals,
gems, weather, space. Each agent gets a unique emoji assigned at setup time.

Default pool (first picks in order):
```
🍎 🍊 🍋 🍇 🍓 🫐 🍑 🥝 🍒 🥭
🌻 🌸 🌵 🍀 🌺 🪻 🌷 🪷 🌿 🍄
🐙 🦊 🐝 🦉 🐋 🦎 🐺 🦜 🐬 🦋
💎 🔮 ⭐ 🌙 ❄️ 🔥 🌈 ⚡ 🪐 🌊
```

**Assignment rules:**
- First agent gets 🍎, second 🍊, etc. (sequential from pool)
- User can override via config or voice: "give bob the octopus"
- Emoji must be unique across all agents — no duplicates
- Cloned agents get the next available emoji, not the source's
- Destroyed agents release their emoji back to the pool

**Display format in Telegram:**

All messages from agents are prefixed with their badge + name:

```
🍎 alice: The API is responding normally. Status 200 on all endpoints.

🍊 bob: I found 3 relevant articles about the topic. Here's a summary...

🍋 carol: Build completed successfully. All 847 tests passing.
```

For multi-agent responses (broadcast, orchestrated):
```
You: "everyone, status report"

🍎 alice: All monitoring checks green. No alerts in the last 24h.
🍊 bob: Research task 60% complete. Estimating 2 more hours.
🍋 carol: Idle. No pending tasks.
```

**Fuzzy name matching (for voice and typos):**

The mux must resolve agent references from imprecise input — spoken names from
Whisper transcription, typos, nicknames, or partial matches.

Resolution order:
1. **Exact match** — "alice" → alice
2. **Case-insensitive** — "Alice", "ALICE" → alice
3. **Prefix match** — "al" → alice (if unambiguous)
4. **Phonetic/fuzzy match** — "ellis", "allice", "alyss" → alice
5. **Emoji reference** — "the apple one", "apple agent" → 🍎 alice
6. **Correction dictionary** — explicit mappings from config (Whisper artifacts)
7. **Context** — if ambiguous, use conversation context (last addressed agent)
8. **Ask** — if still ambiguous: "Did you mean 🍎 alice or 🍊 bob?"

The correction dictionary in mux config (section 5.3) maps known Whisper
misrecognitions to agent names. The mux can also learn new corrections:
"No, I said alice" → mux remembers that variant for next time.

**Identity in the mux config (`~/.xnullclaw/.mux/config.json`):**

```json
{
  "agents": {
    "default": "alice",
    "auto_start": ["alice", "bob"],
    "identities": {
      "alice": { "emoji": "🍎", "aliases": ["al"] },
      "bob":   { "emoji": "🍊", "aliases": ["robert"] },
      "carol": { "emoji": "🍋" }
    }
  }
}
```

If no identity is configured for an agent, the mux auto-assigns the next available
emoji from the pool when the agent first appears.

**Identity in the wrapper (`~/.xnullclaw/<agent>/.meta`):**

The wrapper stores the assigned emoji in the agent's meta file so it persists
across mux restarts and is available to `xnullclaw list`:

```
EMOJI=🍎
```

`xnullclaw list` output:
```
🍎  alice    running   openai/gpt-4o         (mux)
🍊  bob      running   openai/gpt-4o-mini    (mux)
🍋  carol    stopped   anthropic/claude-sonnet
    dave     stopped   openai/gpt-4o         (no identity)
```

**The mux system prompt includes agent identities** so the model knows the mapping:

```
Active agents:
  🍎 alice — code review and monitoring (running)
  🍊 bob — research and analysis (running)
  🍋 carol — build automation (stopped)

When relaying agent responses, always prefix with their emoji and name.
When the user refers to an agent by name, nickname, or emoji — resolve it.
```

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
| ~~MCP SDK~~ | Skipped for now | Not needed at this stage |

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
├── memory.db            ← SQLite database (5 tables):
│                           messages     — rolling window (FIFO, compacted)
│                           facts        — long-term knowledge (scored, pruned)
│                           agent_state  — per-agent status/task/role
│                           compactions  — summarized conversation blocks (append-only)
│                           costs        — mux API cost log
├── logs/                ← structured JSON logs (rotated daily, 7 day retention)
│   ├── mux.log          ← main mux log
│   ├── agents.log       ← agent interaction log
│   └── costs.log        ← cost events
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
    "auto_start": ["alice", "bob"],
    "identities": {
      "alice": { "emoji": "🍎", "aliases": ["al"] },
      "bob":   { "emoji": "🍊", "aliases": ["robert"] },
      "carol": { "emoji": "🍋" }
    }
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
  },

  "logging": {
    "level": "info",
    "dir": "logs",
    "rotate_days": 7
  }
}
```

### 5.3.1 Mux System Prompt

The system prompt is the core of the mux's behavior. It's assembled dynamically
each turn from fixed instructions + live state. This is what makes the mux an
intelligent orchestrator rather than a dumb router.

**System prompt structure (assembled per turn):**

```
┌─────────────────────────────────────────────┐
│ 1. Persona + role (user-customizable)  ~400 │
│ 2. Tools available                     ~800 │
│ 3. Passthrough rules (user-configured) ~300 │
│ 4. Agent roster (live state)           ~500 │
│ 5. Long-term facts (top-K relevant)   ~4000 │
│ 6. Recent compaction summaries        ~2000 │
│                               total: ~8000  │
└─────────────────────────────────────────────┘
```

**1. Persona + role (customizable):**

The mux has a **persona** — its name, personality, tone, and communication style.
This is NOT hardcoded. The user can shape it at any time via natural language or
config, and it persists across restarts.

The system prompt's identity block is assembled from two parts:

**a) Core role (fixed — always present, not user-editable):**

```
You are a personal AI orchestrator managing a fleet of AI agents.
You run 24/7 as the user's intelligence layer between them and their agents.

Your job:
- Route messages to the right agent(s) based on intent
- Manage agent lifecycle (start, stop, create, configure, clone, destroy)
- Synthesize multi-agent responses when useful
- Maintain situational awareness of all agents
- Remember user preferences and apply them consistently
- Be transparent: always prefix agent output with their emoji + name

When the user addresses an agent, be a transparent pipe — forward the message
with minimal intervention (silent typo fixes only, unless passthrough rules say
otherwise). Do not add your own commentary to agent-directed messages.

When the user talks to YOU, respond directly. You handle:
- Agent management ("start alice", "what's bob doing?", "create a new agent")
- System status ("costs?", "who's running?", "what happened today?")
- Multi-agent coordination ("ask everyone to...", "have alice and bob collaborate on...")
- Memory/preferences ("remember that I prefer...", "what did I say about...?")
- Voice/TTS settings ("change your voice", "mute carol's heartbeats")
```

**b) Persona (user-customizable, loaded from config):**

```
Your name is {name}.
{personality}
{communication_style}
{language}
{extra_instructions}
```

**Persona config (`~/.xnullclaw/.mux/config.json`):**

```json
{
  "persona": {
    "name": "mux",
    "personality": "You are concise, direct, and competent. You have a dry sense of humor. You care about efficiency and don't waste the user's time.",
    "communication_style": "Short sentences. No fluff. Use bullet points for lists. Emoji only when labeling agents.",
    "language": "en",
    "extra_instructions": ""
  }
}
```

**Examples of persona tweaking at runtime:**

```
User: "call yourself Nova from now on"
Mux:  → updates persona.name = "Nova"
      → "Done. I'm Nova now."

User: "be more casual, use slang, be funny"
Mux:  → updates persona.personality
      → "aight, vibe shift complete. your agents are still running btw 🤙"

User: "actually, be professional again but keep the name"
Mux:  → updates persona.personality back to professional tone
      → "Understood. Professional mode restored. All systems nominal."

User: "speak to me in Spanish"
Mux:  → updates persona.language = "es"
      → "Entendido. A partir de ahora responderé en español."

User: "when reporting agent status, always include uptime and cost"
Mux:  → updates persona.extra_instructions
      → "Noted. Status reports will now include uptime and cost."
```

**Persona changes persist** — written to config.json immediately. On restart,
the mux retains its personality. The user never has to re-teach it.

**Persona tools:**

The model can modify its own persona via tools:

```
set_persona(field: str, value: str) -> str
    Update persona field: name, personality, communication_style, language,
    extra_instructions. Persists to config. Returns confirmation.

get_persona() -> object
    Return current persona settings.
```

**What persona does NOT affect:**
- Core routing logic (always works the same regardless of personality)
- Agent emoji badges and prefixes (always present)
- Tool execution (tools work identically regardless of tone)
- Memory and compaction behavior (system-level, not persona-level)

Persona only shapes how the mux *talks* — not how it *works*.

**2. Tools available (fixed, from tool definitions):**

Injected from the tool schema (section 4.10). The model sees all available tools
and their parameter schemas. This is standard OpenAI function calling — no special
formatting needed.

**3. Passthrough rules (dynamic, from facts table):**

```
Active passthrough rules:
- GLOBAL: fix obvious typos/grammar silently before forwarding
- alice: append "be concise" to all forwarded messages
- bob: no spelling corrections (raw passthrough for code)
- GLOBAL: translate Spanish to English before forwarding
```

Loaded from `facts` table where `type='rule'`. Updated when user changes rules.

**4. Agent roster (dynamic, from agent_state table):**

```
Active agents:
  🍎 alice — code review and monitoring (running, gpt-4o, uptime 14h, last msg 5m ago)
  🍊 bob — research and analysis (running, gpt-4o-mini, uptime 14h, last msg 2h ago)
  🍋 carol — build automation (stopped)
  🍇 dave — security scanning (running, claude-sonnet, uptime 1h, last msg 1h ago)

Default agent: alice (messages go to her unless another agent is addressed)
Current conversation context: user was talking to alice about API monitoring

Correction dictionary (for voice):
  alice: ellis, allice, alyss
  bob: bop, barb
```

Rebuilt from `agent_state` table each turn. Includes status, model, uptime,
last interaction time, and current task. This gives the model full awareness
of the fleet without needing to call any tools.

**5. Long-term facts (dynamic, retrieved per turn):**

Top-K facts relevant to the current message (see 4.5.4 for retrieval logic).
Example:
```
Known facts:
- User prefers alice for code-related tasks
- User's timezone is UTC-5
- bob was assigned to research Acme Corp competitors on 2026-03-08
- User dislikes verbose agent responses — prefers bullet points
- carol's build pipeline config is at /workspace/ci/pipeline.yml
```

**6. Recent compaction summaries (dynamic):**

Last 2-3 compaction blocks to give the model recent historical context:
```
Recent history:
[2026-03-09 10:00-12:00] User worked with alice on API monitoring setup.
  Configured alerting thresholds. alice found 3 endpoints with high latency.
  User asked bob to research competitor APIs for comparison.
[2026-03-09 08:00-10:00] Morning check-in. All agents healthy. User asked
  for cost report — $2.30 yesterday. Adjusted bob's temperature to 0.5.
```

**After the system prompt, the rolling window of recent messages follows** — these
are the actual conversation turns (user messages, mux responses, tool calls/results).

**The system prompt is token-budgeted** (see 4.5.1). If the agent roster grows
large or facts accumulate, the assembler trims by:
- Summarizing inactive agents more aggressively (one line instead of three)
- Including fewer facts (tighter relevance threshold)
- Including fewer compaction summaries (2 instead of 3)

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
xnullclaw alice status             # detailed status (human-readable)
xnullclaw alice status --json      # → {"status":"running","uptime":"2h34m","restarts":0,...}
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
→ Mux sends: 🍎 alice: Here's the chart you asked for.
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
4. If timeout → inform user: `🍎 alice: Still working... (timeout after 120s)`
5. Optionally: poll the agent or retry

Agent-side timeout is configurable via `agents.defaults.message_timeout_secs` (default: 600s = 10 minutes).

### 5.9 Telegram Message Splitting

Telegram messages are capped at **4096 characters**. Agent responses and mux
summaries can exceed this. The mux MUST split long messages cleanly.

**Splitting rules:**

1. If message ≤ 3800 chars (4096 minus margin for emoji badge header) → send as-is
2. If message > 3800 chars → split into parts:
   - Split at paragraph boundaries (double newline) when possible
   - If no paragraph break fits, split at the last newline before the limit
   - If no newline fits (e.g., giant code block), split at 3800 chars
3. Each part gets a header: `🍎 alice (1/3):`, `🍎 alice (2/3):`, `🍎 alice (3/3):`
4. Parts are sent sequentially with minimal delay (~100ms between sends)
5. Code blocks (triple backticks) are never split mid-block — the split point
   moves before the opening ``` if the block would be broken

**Examples:**
```
Short response → single message:
  🍎 alice: The server is healthy.

Long response → split:
  🍎 alice (1/2): Here's the full analysis of the codebase...
  [4000 chars of content]

  🍎 alice (2/2): ...and the remaining findings.
  [2000 chars of content]
```

### 5.10 Concurrent Agent Interactions

The mux handles multiple agent conversations concurrently. Sending to alice
MUST NOT block sending to bob.

**Per-agent message queue:**

Each agent has an independent goroutine with a message queue (Go channel):

```
                    ┌─ alice queue ─→ goroutine ─→ xnullclaw alice send
User message ──→ mux loop
                    ├─ bob queue   ─→ goroutine ─→ xnullclaw bob send
                    └─ carol queue ─→ goroutine ─→ xnullclaw carol send
```

- User sends "alice, check logs" → queued to alice's goroutine
- User immediately sends "bob, check email" → queued to bob's goroutine
- Both execute concurrently — bob doesn't wait for alice
- Responses arrive in whatever order agents finish, each relayed to Telegram immediately
- If user sends two messages to the same agent, they queue in order (FIFO per agent)

**Agentic loop interaction:**

The mux's agentic loop (section 4.9) may issue multiple tool calls in one response
(e.g., `send_to_agent("alice", ...)` and `send_to_agent("bob", ...)` in parallel).
The `executeToolCalls()` function runs these concurrently via goroutines and collects
results before returning to the loop.

For `send_to_all` and `send_to_some`, the wrapper itself fans out in parallel and
collects all responses before returning.

**Typing indicators:**

When any agent is thinking, the mux sends Telegram's "typing..." action. Multiple
concurrent agents = continuous typing indicator until all respond.

### 5.11 Container Health Awareness

Agents run in Docker containers with `--restart unless-stopped`. If a container
crashes, Docker restarts it automatically. The mux doesn't manage restarts —
Docker does. But the mux MUST be aware of container health.

**What the mux monitors:**

The mux's `watch` goroutine per agent (section 4.2.1) naturally detects container
exits — the `docker logs --follow` stream ends. On detection:

1. Log the event: "alice container exited"
2. Wait briefly (5s) for Docker's auto-restart
3. Check status via `xnullclaw alice status`
4. If back up → note the restart in agent_state, check uptime
5. If still down after 30s → alert user: "🍎 alice crashed and hasn't restarted. Last logs: ..."

**Uptime tracking:**

The wrapper's `status` command includes container uptime. The mux tracks this:

```bash
xnullclaw alice status --json
# → {"status":"running","uptime":"2h34m","restarts":0,"started":"2026-03-09T12:00:00Z"}
```

If restarts > 0 and uptime is short (container keeps crashing and restarting), the
mux alerts:
```
⚠️ 🍎 alice has restarted 3 times in the last hour. Uptime only 45s.
Last log lines:
  error: out of memory at src/agent.zig:142
  panic: allocation failure

Likely cause: memory limit too low. Want me to check her config?
```

**Periodic health check:**

Every 5 minutes, the mux runs `xnullclaw running --json` and compares against its
known agent_state. This catches:
- Agents that disappeared without the watcher noticing
- Agents started externally (via CLI, not mux)
- Port changes, status mismatches

### 5.12 Whisper Correction Learning

When Whisper misrecognizes a word and the user corrects it, the mux stores the
correction in the `facts` table for future use:

```
User: 🎤 "tell Ellis to check the logs"
Mux:  🎙️ heard: "tell Ellis to check the logs"
      → routes to 🍎 alice (matched via correction dictionary)

User: "no, I said bob!"
Mux:  → re-routes to 🍊 bob: "check the logs"
      → stores fact: type='pattern', content='"Ellis" in voice → user meant bob'
      → updates correction_dictionary: bob += ["Ellis"]
```

Corrections persist across restarts (stored in `facts` with `type='pattern'`).
The correction dictionary in config is the seed — learned corrections augment it.
Over time, the mux builds a personalized speech model for the user's voice.

### 5.13 Mux Logging

The mux writes structured logs to files, not stdout (it's a background daemon).

**Log location:** `~/.xnullclaw/.mux/logs/`

```
~/.xnullclaw/.mux/logs/
├── mux.log              ← main mux log (rotated)
├── mux.log.1            ← previous rotation
├── agents.log           ← agent interaction log (sends, responses, lifecycle)
└── costs.log            ← cost events (each API call with tokens + USD)
```

**Log format:** structured JSON lines (one JSON object per line), parseable by
standard tools (`jq`, etc.):

```json
{"ts":"2026-03-09T14:32:01Z","level":"info","msg":"agent response","agent":"alice","tokens":1240,"latency_ms":3200}
{"ts":"2026-03-09T14:32:02Z","level":"info","msg":"telegram send","chat_id":123456,"parts":1}
{"ts":"2026-03-09T14:35:00Z","level":"warn","msg":"agent restart detected","agent":"bob","restarts":2,"uptime":"12s"}
```

**Rotation:** daily rotation, keep last 7 days. Configurable in mux config.

**Log levels:** `debug`, `info`, `warn`, `error`. Default: `info`. Configurable.

The `xnullclaw mux logs [-f]` command tails `mux.log` (with optional follow).

### 5.14 Graceful Shutdown and Startup

**Shutdown sequence (mux stop):**

When the mux receives a stop signal (SIGTERM, SIGINT, or `xnullclaw mux stop`):

1. Stop accepting new Telegram messages
2. Wait for in-flight agent calls to complete (timeout: 30s)
3. Send final message to Telegram: "Mux going offline. Agents will be stopped."
4. Stop all mux-controlled agents: `xnullclaw <agent> stop` for each agent
   that was started with `--mux` (the mux tracks which agents it manages)
5. Flush pending log writes
6. Close SQLite database cleanly
7. Exit

If in-flight calls don't complete within 30s, the mux logs the timeout and
proceeds with shutdown — agent containers will timeout on their own.

**Startup sequence (mux start):**

1. Load config from `~/.xnullclaw/.mux/config.json`
2. Open SQLite `memory.db` — restore all memory tiers
3. Start logging
4. Start mux-controlled agents listed in `agents.auto_start`:
   `xnullclaw <agent> start --mux` for each
5. Wait for agents to become healthy (poll status, timeout 60s per agent)
6. Start `watch` goroutines for all running agents
7. Start Telegram polling
8. Start background goroutines (cost consolidation, health checks, compaction)
9. Send startup message to Telegram: "Mux online. Agents: 🍎 alice (running), 🍊 bob (running)."

**Mux tracks which agents it controls:**

The mux stores in its config which agents are "mux-managed" (started via `--mux`).
On shutdown, only those agents are stopped. Agents running in direct mode
(their own Telegram bot) are left alone.

```json
{
  "agents": {
    "auto_start": ["alice", "bob"],
    "mux_managed": ["alice", "bob", "carol"]
  }
}
```

`mux_managed` is updated at runtime: when the mux starts an agent, it's added.
When the user destroys one via mux, it's removed.

### 5.15 Telegram Rate Limits

**DM is the best option.** Telegram rate limits for bots:

| Chat type | Limit | Notes |
|-----------|-------|-------|
| **Private/DM** | ~1 msg/sec (~60/min) | Short bursts tolerated |
| Group | 20 msgs/min | 3x worse than DM |
| Channel | 20 msgs/min | Same as group |

A group or channel would *reduce* throughput vs DM. The mux uses DM exclusively.

**Practical impact:**

With ~60 msgs/min capacity and typical usage (a few agent responses + occasional
splits), rate limits are unlikely to be hit in normal operation. The worst case
is a broadcast to 10 agents, all responding with long messages requiring 2-3 splits
each — that's ~30 messages, which at 1/sec takes 30 seconds. Acceptable.

**Rate limit handling:**

The mux maintains a Telegram send queue with pacing:
- Messages are sent through a single goroutine with a token bucket (1 msg/sec sustained)
- Bursts up to 3 msgs/sec are allowed briefly
- If Telegram returns 429 (Too Many Requests), respect `retry_after` exactly
- A 429 blocks ALL sends (not just the offending chat), so avoiding it is critical
- Queue priority: user-facing responses > agent alerts > heartbeats > proactive output

**Optimization — fewer messages:**
- Use `sendMediaGroup` for multiple files (up to 10 in one API call, counts as 1 message)
- Combine multi-agent broadcast results into a single message when possible
- Batch proactive output (already specified in 4.2.1 — 5s buffer window)

## 6. Message Flow Examples

### 6.1 Text to Specific Agent

```
User:  "alice, check if the API is responding"
Mux AI: → calls send_to_agent("alice", "check if the API is responding")
Agent:  ← {"response": "The API at api.example.com is returning 200 OK..."}
Mux AI: → formats and sends to Telegram
User sees: 🍎 alice: The API at api.example.com is returning 200 OK...
```

### 6.2 Voice Message

```
User:  🎤 (voice message)
Mux:   → calls transcribe_audio(file_id)
Whisper: ← "tell Ellis to check the server"
Mux AI: → recognizes "Ellis" → "alice" (from correction dictionary)
Mux:   → sends to Telegram: 🎙️ heard: "tell alice to check the server"
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
  🍎 alice: All background tasks stopped. 2 cron jobs paused.
  🍊 bob: Stopped web scraping job. No other tasks running.
  🍋 carol: No background tasks were active.
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
User sees: 🍎 alice: The whiteboard contains: 1) API redesign... 2) ...
```

```
User:  "bob, generate a chart of last month's sales"
Mux AI: → calls send_to_agent("bob", "generate a chart of last month's sales")
Bob:   → generates chart, writes to outbox/sales_chart.png
       ← "Done. Chart saved to outbox/sales_chart.png"
Mux:   → detects file reference, retrieves outbox/sales_chart.png
       → sends to Telegram as photo with caption
User sees:
  🍊 bob: Here's last month's sales chart.
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
- [ ] Agent identity system: emoji pool, auto-assignment, .meta persistence
- [ ] xnullclaw wrapper: add `send` (stdin/stdout), `running --json`, `--mux` flag
- [ ] Core tools: `send_to_agent`, `list_agents`, `agent_status`
- [ ] Basic intent routing (AI determines agent vs mux)
- [ ] Response formatting with emoji badge + name prefixes
- [ ] Fuzzy name matching (case-insensitive, prefix, correction dictionary)

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
- [ ] TTS output via OpenAI TTS API (text always sent alongside voice)
- [ ] Agent audio/voice passthrough (agent-produced audio → Telegram as-is)
- [ ] xnullclaw wrapper: add `send-file`, `get-file`, `ls`, `clean-outbox`
- [ ] Inbound media: photos, documents, audio, video → agent inbox/
- [ ] Outbound media: agent outbox/ → Telegram (auto-detect type, send as photo/audio/document/video)
- [ ] File reference detection in agent responses

### Phase 4 — Agent Lifecycle + Proactive Output
- [ ] Tools: `start_agent`, `stop_agent`, `restart_agent`
- [ ] Tools: `setup_agent`, `clone_agent` (mux creates + fully configures new agents)
- [ ] `update_agent_config` + auto-restart
- [ ] `get_agent_config` tool
- [ ] Proactive health monitoring and alerting
- [ ] xnullclaw wrapper: add `drain`, `watch` commands for unsolicited agent output
- [ ] Agent output monitoring: relay all cron results, alerts, errors to Telegram
- [ ] Output batching (prevent Telegram spam from rapid agent output)
- [ ] Per-agent output mute/filter controls

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

1. User can talk to any agent by name (or emoji, or nickname) via a single Telegram bot
2. Mux correctly routes >95% of messages without explicit commands
3. Voice messages work with <5s latency (transcribe + route + response display)
4. Agent lifecycle (start/stop/restart/create/configure) manageable entirely from Telegram
5. Mux maintains useful context across conversations (no "who is alice?" amnesia)
6. Zero host ports exposed in mux mode
7. All containers maintain hardened security posture
8. All agent output (cron, alerts, heartbeats, errors) reaches user via Telegram promptly
9. Agent emoji badges are visually distinct and consistent across restarts
10. TTS works bidirectionally: user voice → Whisper → text, mux/agent text → TTS → voice
