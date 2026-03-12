// Package mux wires together the Telegram bot, agentic loop, memory, tools,
// and all other subsystems into the running mux process.
package mux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/llm"
	"github.com/jotavich/xnullclaw/internal/logging"
	"github.com/jotavich/xnullclaw/internal/loop"
	"github.com/jotavich/xnullclaw/internal/media"
	"github.com/jotavich/xnullclaw/internal/memory"
	"github.com/jotavich/xnullclaw/internal/prompt"
	"github.com/jotavich/xnullclaw/internal/telegram"
	"github.com/jotavich/xnullclaw/internal/tools"
	"github.com/jotavich/xnullclaw/internal/voice"
)

// turnResult holds the output of a turn for deferred delivery after
// releasing the turn lock. This decouples Telegram I/O from the
// serialization mutex so new messages aren't blocked during sends (M20).
//
// Invariant: errMsg and text/attachments are mutually exclusive.
// When errMsg is set, text and attachments are always empty.
type turnResult struct {
	heartbeatOK bool               // true if LLM responded with HEARTBEAT_OK (suppresses delivery)
	chatID      int64              // target chat for delivery
	text        string             // clean text to send (after media.Parse)
	attachments []media.Attachment // media attachments to send
	errMsg      string             // error message to send (mutually exclusive with text)
}

// Config holds the parameters needed to run the mux.
type Config struct {
	Home       string // XNC home (e.g. ~/.xnc)
	CfgPath    string // path to config.json
	Image      string // Docker image name
	Version    string
	Foreground bool // mirror logs to stderr
}

// Run starts the mux and blocks until shutdown.
func Run(mc Config) error {
	muxHome := filepath.Join(mc.Home, "mux")
	if err := os.MkdirAll(muxHome, 0700); err != nil {
		return fmt.Errorf("create mux home: %w", err)
	}

	// Load config — use defaults only if file doesn't exist yet.
	cfg, err := config.Load(mc.CfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = config.DefaultConfig()
			_ = cfg.Save(mc.CfgPath)
		} else {
			return fmt.Errorf("config: %w (fix the file or delete it to reset)", err)
		}
	}

	// Logging.
	logger, err := logging.New(&cfg.Logging, muxHome)
	if err != nil {
		return fmt.Errorf("logging: %w", err)
	}
	defer logger.Close()
	if mc.Foreground {
		logger.SetMirror(os.Stderr)
	}
	logger.Info("mux starting", "version", mc.Version)

	// Log chat mode.
	if cfg.Telegram.GroupID != 0 {
		logger.Info("group mode", "group_id", cfg.Telegram.GroupID, "topic_id", cfg.Telegram.TopicID)
		if cfg.Telegram.TopicID == -1 {
			logger.Info("topic discovery mode: logging thread IDs, not processing messages")
		}
	} else {
		logger.Info("private chat mode")
	}

	// SQLite memory store.
	dbPath := cfg.Memory.DBPath
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(muxHome, dbPath)
	}
	store, err := memory.New(dbPath)
	if err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	defer store.Close()
	logger.Info("memory loaded", "db", dbPath)

	// Docker client.
	dk, err := docker.NewClient()
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	defer dk.Close()

	// Tool registry — all tools use direct Go calls.
	registry := tools.NewRegistry()
	deps := tools.Deps{
		Docker:  dk,
		Store:   store,
		Cfg:     cfg,
		CfgPath: mc.CfgPath,
		Home:    mc.Home,
		Image:   mc.Image,
	}
	tools.RegisterAll(registry, deps)
	logger.Info("tools registered", "count", len(registry.Definitions()))

	// OpenAI adapter.
	openaiClient := llm.NewOpenAIAdapter(cfg)

	// Agentic loop.
	muxLoop := loop.New(cfg, openaiClient)
	for _, def := range registry.Definitions() {
		toolName := def.Name
		muxLoop.RegisterTool(loop.ToolDef{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		}, func(ctx context.Context, args map[string]any) (string, error) {
			return registry.Execute(ctx, toolName, args)
		})
	}

	// Observability hooks.
	muxLoop.OnToolCall = func(name string, args map[string]any, result string, duration time.Duration, err error) {
		logger.LogToolCall(name, redactToolArgs(name, args), result, duration, err)
	}
	muxLoop.OnModelCall = func(inputTokens, outputTokens int, costUSD float64) {
		logger.LogModelCall(cfg.OpenAI.Model, inputTokens, outputTokens, costUSD, 0)
		model := cfg.OpenAI.Model
		if err := store.AddCost(memory.Cost{
			Category:     "loop",
			Model:        &model,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			CostUSD:      costUSD,
		}); err != nil {
			logger.Error("failed to record cost", "error", err)
		}
	}

	// Context assembler + prompt builder.
	assembler := memory.NewAssembler(store)
	promptBuilder := prompt.New(cfg)

	// Telegram bot.
	tgBot, err := telegram.New(&cfg.Telegram)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}

	// Shutdown context.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Media temp directory.
	mediaTmpDir := filepath.Join(muxHome, "media_tmp")
	if err := os.MkdirAll(mediaTmpDir, 0700); err != nil {
		return fmt.Errorf("create media tmp: %w", err)
	}

	// Turn serialization.
	var (
		turnMu     sync.Mutex
		lastChatID int64
	)

	// maxTurnDuration caps how long a single agentic loop turn can run.
	// This prevents the turnMu from being held indefinitely (H9).
	const maxTurnDuration = 5 * time.Minute

	// runTurn executes one agentic loop turn. The stream parameter controls
	// where messages are stored ("conversation" for user/task turns,
	// "scheduler" for heartbeats to avoid polluting conversation context).
	// Returns a turnResult for deferred delivery outside the turn lock (M20).
	// Callers are responsible for SendTyping before acquiring the lock.
	runTurn := func(chatID int64, userText, stream string) turnResult {
		turnCtx, turnCancel := context.WithTimeout(ctx, maxTurnDuration)
		defer turnCancel()

		ctxData, err := assembler.Assemble(userText)
		if err != nil {
			logger.Error("context assembly failed", "error", err)
			return turnResult{chatID: chatID, errMsg: "Internal error assembling context."}
		}

		systemPrompt := promptBuilder.Build(ctxData.Agents, ctxData.Facts, ctxData.Compactions, ctxData.Rules, ctxData.DrainMsgs)
		muxLoop.SetSystemPrompt(systemPrompt)

		if err := store.AddMessage(memory.Message{
			Role:    "user",
			Content: userText,
			Stream:  stream,
		}); err != nil {
			logger.Error("failed to store user message", "error", err)
		}

		response, err := muxLoop.Run(turnCtx, userText)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				logger.Error("turn timed out", "timeout", maxTurnDuration, "input_len", len(userText))
				return turnResult{chatID: chatID, errMsg: "Your request timed out. Please try a simpler request."}
			}
			logger.Error("loop error", "error", err)
			return turnResult{chatID: chatID, errMsg: "Something went wrong processing your message. Please try again."}
		}

		// Suppress HEARTBEAT_OK responses — don't store or forward.
		if IsHeartbeatOK(response) {
			logger.Info("turn complete (heartbeat ack suppressed)", "input_len", len(userText))
			return turnResult{heartbeatOK: true, chatID: chatID}
		}

		if err := store.AddMessage(memory.Message{
			Role:    "assistant",
			Content: response,
			Stream:  stream,
		}); err != nil {
			logger.Error("failed to store assistant message", "error", err)
		}

		cleanText, attachments := media.Parse(response)
		logger.Info("turn complete", "input_len", len(userText), "output_len", len(response), "attachments", len(attachments))
		return turnResult{
			chatID:      chatID,
			text:        cleanText,
			attachments: attachments,
		}
	}

	// deliverResult sends the turn output to Telegram. Called AFTER releasing
	// the turn lock so that Telegram I/O doesn't block incoming messages (M20).
	// A deliverMu serializes delivery calls to preserve message ordering
	// between consecutive turns (#10).
	var deliverMu sync.Mutex
	deliverResult := func(r turnResult) {
		deliverMu.Lock()
		defer deliverMu.Unlock()
		deliverTurnResult(tgBot, logger, r)
	}

	// Text messages.
	tgBot.SetOnMessage(func(chatID int64, userID string, text string) {
		atomic.StoreInt64(&lastChatID, chatID)
		logger.LogIncoming(userID, text, "text")
		tgBot.SendTyping(chatID)

		turnMu.Lock()
		result := runTurn(chatID, text, "conversation")
		turnMu.Unlock()

		deliverResult(result)
	})

	// Voice messages — all preprocessing (download, transcribe) happens
	// before acquiring the turn lock so network I/O doesn't starve other turns.
	tgBot.SetOnVoice(func(chatID int64, userID string, fileID string) {
		atomic.StoreInt64(&lastChatID, chatID)
		logger.LogIncoming(userID, fileID, "voice")

		if !cfg.Voice.Enabled {
			tgBot.Send(chatID, "Voice messages are disabled.")
			return
		}

		voicePath := filepath.Join(mediaTmpDir, fmt.Sprintf("voice_%s_%d.ogg", userID, time.Now().UnixNano()))
		if err := tgBot.DownloadFile(fileID, voicePath); err != nil {
			logger.Error("voice download failed", "error", err)
			tgBot.Send(chatID, "Failed to download voice message. Please try again.")
			return
		}
		defer os.Remove(voicePath)

		tgBot.SendTyping(chatID)
		transcript, err := voice.Transcribe(ctx, voicePath, cfg.OpenAI.APIKey, cfg.OpenAI.WhisperModel)
		if err != nil {
			logger.Error("transcription failed", "error", err)
			tgBot.Send(chatID, "Voice transcription failed. Please try again or send text instead.")
			return
		}
		if strings.TrimSpace(transcript) == "" {
			tgBot.Send(chatID, "Could not transcribe voice message (empty result).")
			return
		}
		if cfg.Voice.ShowTranscription {
			tgBot.Send(chatID, fmt.Sprintf("_Heard:_ %s", transcript))
		}
		logger.Info("voice transcribed", "text", transcript)

		turnMu.Lock()
		result := runTurn(chatID, transcript, "conversation")
		turnMu.Unlock()

		deliverResult(result)
	})

	// Media messages (photos, documents) — download happens before the lock
	// so network I/O doesn't starve other turns.
	tgBot.SetOnMedia(func(chatID int64, userID string, fileID string, mediaType string, caption string, fileName string) {
		atomic.StoreInt64(&lastChatID, chatID)
		logger.LogIncoming(userID, fileID, mediaType)

		// Sanitize user-supplied text to prevent prompt injection.
		caption = config.SanitizeText(caption, 500)
		fileName = config.SanitizeText(fileName, 100)

		// Determine filename and extension.
		var destPath string
		if fileName != "" {
			// Sanitize: strip path components, keep only base name.
			safeName := filepath.Base(fileName)
			if safeName == "." || safeName == "/" || safeName == "" {
				safeName = "upload"
			}
			destPath = filepath.Join(mediaTmpDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), safeName))
		} else {
			ext := ".bin"
			switch mediaType {
			case "photo":
				ext = ".jpg"
			case "document":
				ext = ".dat"
			}
			destPath = filepath.Join(mediaTmpDir, fmt.Sprintf("%s_%s_%d%s", mediaType, userID, time.Now().UnixNano(), ext))
		}
		if err := tgBot.DownloadFile(fileID, destPath); err != nil {
			logger.Error("media download failed", "error", err, "type", mediaType)
			tgBot.Send(chatID, fmt.Sprintf("Failed to download %s. Please try again.", mediaType))
			return
		}

		var userText string
		nameInfo := ""
		if fileName != "" {
			nameInfo = fmt.Sprintf(" (filename: %s)", fileName)
		}
		if caption != "" {
			userText = fmt.Sprintf("[User sent a %s%s with caption: %s]\nThe file has been saved to: %s", mediaType, nameInfo, caption, destPath)
		} else {
			userText = fmt.Sprintf("[User sent a %s%s]\nThe file has been saved to: %s", mediaType, nameInfo, destPath)
		}
		logger.Info("media received", "type", mediaType, "path", destPath, "caption", caption, "filename", fileName)

		turnMu.Lock()
		result := runTurn(chatID, userText, "conversation")
		turnMu.Unlock()

		deliverResult(result)
	})

	// Commands.
	tgBot.SetOnCommand(func(chatID int64, userID string, cmd telegram.Command) {
		logger.Info("command received", "user", userID, "command", cmd.Name, "agent", cmd.Agent)

		switch cmd.Name {
		case "help":
			tgBot.Send(chatID, `*Commands:*
/help — Show this help
/agents — List agents with status
/stats — Memory database stats
/costs — Today's cost summary
/clear confirm — Clear all mux memory

*You can also just talk to me:*
• Ask questions — I'll answer or route to the right agent
• "ask alice to research X" — delegate tasks to agents
• "start/stop bob" — manage agent lifecycles
• "what are the agents working on?" — get status updates
• Send files, photos, or voice messages — I handle all media types`)

		case "agents", "list":
			states, _ := store.AllAgentStates()
			if len(states) == 0 {
				tgBot.Send(chatID, "No agents found.")
				return
			}
			var lines []string
			lines = append(lines, "*Agents:*")
			for _, s := range states {
				emoji := "❓"
				if s.Emoji != nil && *s.Emoji != "" {
					emoji = *s.Emoji
				}
				status := "unknown"
				if s.Status != nil {
					status = *s.Status
				}
				role := ""
				if s.Role != nil && *s.Role != "" {
					role = " — " + *s.Role
				}
				lines = append(lines, fmt.Sprintf("%s *%s* (%s)%s", emoji, s.Agent, status, role))
			}
			tgBot.Send(chatID, strings.Join(lines, "\n"))

		case "stats":
			stats, err := store.Stats()
			if err != nil {
				logger.Error("stats command failed", "error", err)
				tgBot.Send(chatID, "Failed to retrieve stats.")
				return
			}
			text := fmt.Sprintf("*Memory stats:*\nMessages: %d\nFacts: %d\nCompactions: %d\nCosts: %d\nAgent states: %d",
				stats["messages"], stats["facts"], stats["compactions"],
				stats["costs"], stats["agent_state"])
			tgBot.Send(chatID, text)

		case "costs":
			now := time.Now()
			startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			summary, err := store.CostSummary(startOfDay, now)
			if err != nil {
				logger.Error("costs command failed", "error", err)
				tgBot.Send(chatID, "Failed to retrieve cost data.")
				return
			}
			if len(summary) == 0 {
				tgBot.Send(chatID, "No costs recorded today.")
				return
			}
			var lines []string
			lines = append(lines, "*Today's costs:*")
			var total float64
			for cat, amount := range summary {
				lines = append(lines, fmt.Sprintf("  %s: $%.4f", cat, amount))
				total += amount
			}
			lines = append(lines, fmt.Sprintf("  *Total: $%.4f*", total))
			if cfg.Costs.DailyBudgetUSD > 0 {
				pct := (total / cfg.Costs.DailyBudgetUSD) * 100
				lines = append(lines, fmt.Sprintf("  Budget: $%.2f (%.1f%% used)", cfg.Costs.DailyBudgetUSD, pct))
			}
			tgBot.Send(chatID, strings.Join(lines, "\n"))

		case "clear":
			if cmd.Args == "confirm" {
				// Acquire turnMu to prevent races with concurrent loop turns
				// that read/write muxLoop.messages and store (#3).
				turnMu.Lock()
				clearErr := store.ClearAll()
				if clearErr == nil {
					muxLoop.ClearHistory()
				}
				turnMu.Unlock()
				if clearErr != nil {
					logger.Error("memory clear failed", "error", clearErr)
					tgBot.Send(chatID, "Failed to clear memory. Check logs for details.")
					return
				}
				tgBot.Send(chatID, "Memory cleared. Conversation history, facts, compactions, and costs have been wiped. Agent state preserved.")
				logger.Info("memory cleared by user", "user", userID)
			} else {
				tgBot.Send(chatID, "⚠️ This will delete all conversation history, facts, compactions, and cost records. Agent state is preserved.\n\nSend /clear confirm to proceed.")
			}

		default:
			tgBot.Send(chatID, fmt.Sprintf("Unknown command /%s. Use /help for available commands.", cmd.Name))
		}
	})

	// Topic discovery callback — logs via structured logger so it shows in mux.log.
	tgBot.SetOnDiscovery(func(chatID int64, userID string, threadID int, text string) {
		logger.Info("topic discovery", "chat_id", chatID, "user_id", userID, "thread_id", threadID, "text", truncateLog(text, 80))
	})

	// Start Telegram polling.
	go func() {
		logger.Info("telegram polling started")
		if err := tgBot.Start(ctx); err != nil && ctx.Err() == nil {
			logger.Error("telegram error", "error", err)
		}
	}()

	// Auto-start agents (direct Go calls, no wrapper).
	go func() {
		started := 0
		for _, agentName := range cfg.Agents.AutoStart {
			logger.LogLifecycle("auto-starting", agentName, "")
			cn := agent.ContainerName(mc.Home, agentName)

			// Check if already running.
			running, err := dk.IsRunning(ctx, cn)
			if err == nil && running {
				logger.LogLifecycle("already-running", agentName, "")
				started++
				continue
			}

			if !agent.HasProviderKey(mc.Home, agentName) {
				logger.Error("auto-start skipped: no API key", "agent", agentName)
				continue
			}

			opts := agent.StartOpts(mc.Image, mc.Home, agentName, 0)
			if err := dk.StartContainer(ctx, cn, opts); err != nil {
				logger.Error("auto-start failed", "agent", agentName, "error", err)
			} else {
				logger.LogLifecycle("started", agentName, "")
				started++
			}
		}

		// Send online message to Telegram.
		chatID := cfg.Telegram.GroupID
		if chatID != 0 {
			msg := fmt.Sprintf("Mux online. %d agent(s) managed.", started)
			if err := tgBot.Send(chatID, msg); err != nil {
				logger.Error("failed to send online message", "error", err)
			}
		}
	}()

	// Start drain goroutine — polls agent .outbox/ directories and sends
	// responses directly to Telegram, bypassing the LLM.
	drainDone := make(chan struct{})
	var drainWg sync.WaitGroup
	drainer := &Drainer{
		home:   mc.Home,
		store:  store,
		bot:    tgBot,
		cfg:    cfg,
		logger: logger,
		chatID: &lastChatID,
		turnMu: &turnMu,
	}
	drainWg.Add(1)
	go func() {
		defer drainWg.Done()
		drainer.Run(drainInterval, drainDone)
	}()
	logger.Info("drain goroutine started", "interval", drainInterval)

	// Start scheduler goroutine — checks for due tasks and fires synthetic turns.
	schedulerDone := make(chan struct{})
	scheduler := &Scheduler{
		store:         store,
		cfg:           cfg,
		logger:        logger,
		chatID:        &lastChatID,
		turnMu:        &turnMu,
		runTurn:       runTurn,
		deliver:       deliverResult,
		lastHeartbeat: time.Now(), // avoid stale drain scan on first tick (M8)
	}
	go scheduler.Run(schedulerInterval, schedulerDone)
	logger.Info("scheduler goroutine started", "interval", schedulerInterval)

	logger.Info("mux online")

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutdown signal received", "signal", sig.String())

	// Graceful shutdown.
	// 1. Stop agents in parallel (bounded concurrency) — let them flush final output.
	const maxParallelStops = 5
	stopSem := make(chan struct{}, maxParallelStops)
	var stopWg sync.WaitGroup
	for _, agentName := range cfg.Agents.MuxManaged {
		stopWg.Add(1)
		go func(name string) {
			defer stopWg.Done()
			stopSem <- struct{}{}
			defer func() { <-stopSem }()
			logger.LogLifecycle("stopping", name, "shutdown")
			cn := agent.ContainerName(mc.Home, name)
			if err := dk.StopContainer(context.Background(), cn); err != nil {
				logger.Error("agent stop failed", "agent", name, "error", err)
			}
		}(agentName)
	}
	stopWg.Wait()

	// 2. Stop scheduler — no more synthetic turns after this.
	close(schedulerDone)

	// 3. Final drain pass — pick up messages written before/during agent shutdown.
	drainer.drainAll()
	// 4. Stop drain goroutine and wait for it to exit.
	close(drainDone)
	drainWg.Wait()

	// Send goodbye before stopping the bot (Stop closes the send queue).
	if cid := atomic.LoadInt64(&lastChatID); cid != 0 {
		tgBot.Send(cid, "Mux going offline. Agents stopped.")
	}

	tgBot.Stop()

	cancel()
	logger.Info("mux shutdown complete")
	return nil
}

// deliverTurnResult sends a turnResult to Telegram. Extracted from the
// deliverResult closure for testability (#7). The deliverResult closure
// wraps this with a deliverMu for ordering (#10).
func deliverTurnResult(bot telegram.Sender, logger *logging.Logger, r turnResult) {
	if r.errMsg != "" {
		if err := bot.Send(r.chatID, r.errMsg); err != nil {
			logger.Error("telegram send failed", "error", err)
		}
		return
	}
	if r.text != "" {
		if err := bot.Send(r.chatID, r.text); err != nil {
			logger.Error("telegram send failed", "error", err)
		}
	}
	for _, att := range r.attachments {
		sendAttachment(bot, logger, r.chatID, att, "")
	}
}

// sendAttachment sends a media attachment via Telegram.
// caption is used for photo/video/audio/document types (e.g. agent identity).
func sendAttachment(tgBot telegram.Sender, logger *logging.Logger, chatID int64, att media.Attachment, caption string) {
	hostPath := att.Path

	if strings.HasPrefix(att.Path, "/nullclaw-data/") {
		logger.Error("attachment path appears to be a container path", "path", att.Path)
		tgBot.Send(chatID, fmt.Sprintf("Could not send attachment: %s (container path — use get_agent_file first)", att.Path))
		return
	}

	// Ensure path is absolute and clean to prevent path traversal.
	hostPath = filepath.Clean(hostPath)
	if !filepath.IsAbs(hostPath) {
		logger.Error("attachment path not absolute", "path", hostPath)
		tgBot.Send(chatID, "Could not send attachment: invalid path.")
		return
	}

	if _, err := os.Stat(hostPath); err != nil {
		logger.Error("attachment file not found", "path", hostPath, "error", err)
		tgBot.Send(chatID, fmt.Sprintf("Attachment not found: %s", filepath.Base(hostPath)))
		return
	}

	ext := strings.ToLower(filepath.Ext(hostPath))

	var err error
	switch att.Type {
	case media.TypeImage:
		err = tgBot.SendPhoto(chatID, hostPath, caption)
	case media.TypeVideo:
		err = tgBot.SendVideo(chatID, hostPath, caption)
	case media.TypeVoice:
		if ext == ".ogg" || ext == ".oga" || ext == ".opus" {
			err = tgBot.SendVoice(chatID, hostPath)
		} else {
			err = tgBot.SendAudio(chatID, hostPath, caption)
		}
	case media.TypeAudio:
		err = tgBot.SendAudio(chatID, hostPath, caption)
	default:
		err = tgBot.SendDocument(chatID, hostPath, caption)
	}

	if err != nil {
		logger.Error("send attachment failed", "type", att.Type, "path", hostPath, "error", err)
		tgBot.Send(chatID, fmt.Sprintf("Failed to send %s: %v", att.Type, err))
	}
}


// agentIdentityHeader returns "emoji name\n\n" for a given agent,
// using the mux config identities map.
func agentIdentityHeader(cfg *config.Config, name string) string {
	if cfg.Agents.Identities != nil {
		if id, ok := cfg.Agents.Identities[name]; ok && id.Emoji != "" {
			return fmt.Sprintf("%s %s\n\n", id.Emoji, name)
		}
	}
	return fmt.Sprintf("%s\n\n", name)
}

// redactToolArgs returns a copy of args with sensitive values masked.
// For tools that write config values, the "value" arg is redacted if
// the "key" arg corresponds to a secret config key.
func redactToolArgs(toolName string, args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	switch toolName {
	case "update_agent_config":
	default:
		return args
	}
	key, _ := args["key"].(string)
	if key == "" {
		return args
	}
	ck, ok := agent.LookupConfigKey(key)
	if !ok || !ck.Redacted {
		return args
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if k == "value" {
			if s, ok := v.(string); ok {
				out[k] = agent.RedactKey(s)
			} else {
				out[k] = v
			}
		} else {
			out[k] = v
		}
	}
	return out
}

// truncateLog truncates a string for log output at rune boundaries.
func truncateLog(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
