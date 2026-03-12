// Package mux wires together the Telegram bot, agentic loop, memory, tools,
// and all other subsystems into the running mux process.
package mux

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
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
	if err := os.MkdirAll(muxHome, 0755); err != nil {
		return fmt.Errorf("create mux home: %w", err)
	}

	// Load config.
	cfg, err := config.Load(mc.CfgPath)
	if err != nil {
		cfg = config.DefaultConfig()
		_ = cfg.Save(mc.CfgPath)
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
		logger.LogToolCall(name, args, result, duration, err)
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
	if err := os.MkdirAll(mediaTmpDir, 0755); err != nil {
		return fmt.Errorf("create media tmp: %w", err)
	}

	// Turn serialization.
	var (
		turnMu     sync.Mutex
		lastChatID int64
	)

	// Track which agent(s) were called via send_to_agent during a turn,
	// so we can prepend the identity header when sending to Telegram.
	var turnAgents []string
	origOnToolCall := muxLoop.OnToolCall
	muxLoop.OnToolCall = func(name string, args map[string]any, result string, duration time.Duration, err error) {
		if name == "send_to_agent" {
			if a, ok := args["agent"].(string); ok {
				turnAgents = append(turnAgents, a)
			}
		}
		if origOnToolCall != nil {
			origOnToolCall(name, args, result, duration, err)
		}
	}

	runTurn := func(chatID int64, userText string) {
		turnAgents = nil // reset for this turn

		ctxData, err := assembler.Assemble(userText)
		if err != nil {
			logger.Error("context assembly failed", "error", err)
			tgBot.Send(chatID, "Internal error assembling context.")
			return
		}

		systemPrompt := promptBuilder.Build(ctxData.Agents, ctxData.Facts, ctxData.Compactions, ctxData.Rules)
		muxLoop.SetSystemPrompt(systemPrompt)

		if err := store.AddMessage(memory.Message{
			Role:    "user",
			Content: userText,
			Stream:  "conversation",
		}); err != nil {
			logger.Error("failed to store user message", "error", err)
		}

		tgBot.SendTyping(chatID)

		response, err := muxLoop.Run(ctx, userText)
		if err != nil {
			logger.Error("loop error", "error", err)
			tgBot.Send(chatID, fmt.Sprintf("Error: %v", err))
			return
		}

		if err := store.AddMessage(memory.Message{
			Role:    "assistant",
			Content: response,
			Stream:  "conversation",
		}); err != nil {
			logger.Error("failed to store assistant message", "error", err)
		}

		cleanText, attachments := media.Parse(response)

		if cleanText != "" {
			// If exactly one agent was called, prepend its identity header.
			if len(turnAgents) == 1 {
				cleanText = agentIdentityHeader(cfg, turnAgents[0]) + cleanText
			}
			if err := tgBot.Send(chatID, cleanText); err != nil {
				logger.Error("telegram send failed", "error", err)
			}
		}

		for _, att := range attachments {
			sendAttachment(tgBot, logger, chatID, att)
		}

		logger.Info("turn complete", "input_len", len(userText), "output_len", len(response), "attachments", len(attachments))
	}

	// Text messages.
	tgBot.SetOnMessage(func(chatID int64, userID string, text string) {
		turnMu.Lock()
		defer turnMu.Unlock()
		lastChatID = chatID
		logger.LogIncoming(userID, text, "text")
		runTurn(chatID, text)
	})

	// Voice messages.
	tgBot.SetOnVoice(func(chatID int64, userID string, fileID string) {
		turnMu.Lock()
		defer turnMu.Unlock()
		lastChatID = chatID
		logger.LogIncoming(userID, fileID, "voice")

		if !cfg.Voice.Enabled {
			tgBot.Send(chatID, "Voice messages are disabled.")
			return
		}

		voicePath := filepath.Join(mediaTmpDir, fmt.Sprintf("voice_%s_%d.ogg", userID, time.Now().UnixNano()))
		if err := tgBot.DownloadFile(fileID, voicePath); err != nil {
			logger.Error("voice download failed", "error", err)
			tgBot.Send(chatID, fmt.Sprintf("Failed to download voice: %v", err))
			return
		}
		defer os.Remove(voicePath)

		tgBot.SendTyping(chatID)
		transcript, err := voice.Transcribe(ctx, voicePath, cfg.OpenAI.APIKey, cfg.OpenAI.WhisperModel)
		if err != nil {
			logger.Error("transcription failed", "error", err)
			tgBot.Send(chatID, fmt.Sprintf("Transcription failed: %v", err))
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
		runTurn(chatID, transcript)
	})

	// Media messages (photos, documents).
	tgBot.SetOnMedia(func(chatID int64, userID string, fileID string, mediaType string, caption string, fileName string) {
		turnMu.Lock()
		defer turnMu.Unlock()
		lastChatID = chatID
		logger.LogIncoming(userID, fileID, mediaType)

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
			tgBot.Send(chatID, fmt.Sprintf("Failed to download %s: %v", mediaType, err))
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
		runTurn(chatID, userText)
	})

	// Commands.
	tgBot.SetOnCommand(func(chatID int64, userID string, cmd telegram.Command) {
		logger.Info("command received", "user", userID, "command", cmd.Name, "agent", cmd.Agent)

		switch cmd.Name {
		case "help":
			tgBot.Send(chatID, `*Available commands:*
/help — Show this help message
/agents — List all agents with emoji + name + status
/stats — Show memory database statistics
/costs — Show today's cost summary
/clear — Clear all mux memory (requires confirmation)
/clear confirm — Confirm memory clear

All other messages are handled by the mux AI.`)

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
				tgBot.Send(chatID, fmt.Sprintf("Error: %v", err))
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
				tgBot.Send(chatID, fmt.Sprintf("Error: %v", err))
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
				if err := store.ClearAll(); err != nil {
					tgBot.Send(chatID, fmt.Sprintf("Error clearing memory: %v", err))
					return
				}
				muxLoop.ClearHistory()
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
		for _, agentName := range cfg.Agents.AutoStart {
			logger.LogLifecycle("auto-starting", agentName, "")
			cn := agent.ContainerName(mc.Home, agentName)

			// Check if already running.
			running, err := dk.IsRunning(ctx, cn)
			if err == nil && running {
				logger.LogLifecycle("already-running", agentName, "")
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
			}
		}
	}()

	logger.Info("mux online")

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutdown signal received", "signal", sig.String())

	// Graceful shutdown.
	// Stop mux-managed agents.
	for _, agentName := range cfg.Agents.MuxManaged {
		logger.LogLifecycle("stopping", agentName, "shutdown")
		cn := agent.ContainerName(mc.Home, agentName)
		if err := dk.StopContainer(context.Background(), cn); err != nil {
			logger.Error("agent stop failed", "agent", agentName, "error", err)
		}
	}

	// Send goodbye before stopping the bot (Stop closes the send queue).
	if lastChatID != 0 {
		tgBot.Send(lastChatID, "Mux going offline. Agents stopped.")
	}

	tgBot.Stop()

	cancel()
	logger.Info("mux shutdown complete")
	return nil
}

// sendAttachment sends a media attachment via Telegram.
func sendAttachment(tgBot *telegram.Bot, logger *logging.Logger, chatID int64, att media.Attachment) {
	hostPath := att.Path

	if strings.HasPrefix(att.Path, "/nullclaw-data/") {
		logger.Error("attachment path appears to be a container path", "path", att.Path)
		tgBot.Send(chatID, fmt.Sprintf("Could not send attachment: %s (container path — use get_agent_file first)", att.Path))
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
		err = tgBot.SendPhoto(chatID, hostPath, "")
	case media.TypeVideo:
		err = tgBot.SendVideo(chatID, hostPath, "")
	case media.TypeVoice:
		if ext == ".ogg" || ext == ".oga" || ext == ".opus" {
			err = tgBot.SendVoice(chatID, hostPath)
		} else {
			err = tgBot.SendAudio(chatID, hostPath, "")
		}
	case media.TypeAudio:
		err = tgBot.SendAudio(chatID, hostPath, "")
	default:
		err = tgBot.SendDocument(chatID, hostPath, "")
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

// truncateLog truncates a string for log output at rune boundaries.
func truncateLog(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
