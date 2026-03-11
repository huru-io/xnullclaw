package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jotavich/xnullclaw/mux/bot"
	"github.com/jotavich/xnullclaw/mux/config"
	"github.com/jotavich/xnullclaw/mux/logging"
	"github.com/jotavich/xnullclaw/mux/loop"
	"github.com/jotavich/xnullclaw/mux/media"
	"github.com/jotavich/xnullclaw/mux/memory"
	"github.com/jotavich/xnullclaw/mux/prompt"
	"github.com/jotavich/xnullclaw/mux/tools"
	"github.com/jotavich/xnullclaw/mux/voice"
)

var version = "dev"

func main() {
	// 1. Determine mux home directory.
	muxHome := filepath.Join(os.Getenv("HOME"), ".xnc", ".mux")
	if err := os.MkdirAll(muxHome, 0755); err != nil {
		log.Fatalf("failed to create mux home %s: %v", muxHome, err)
	}

	// Config path: default or from first CLI argument.
	cfgPath := filepath.Join(muxHome, "config.json")
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	// 2. Load config.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Printf("config: using defaults (%v)", err)
		cfg = config.DefaultConfig()
		if saveErr := cfg.Save(cfgPath); saveErr != nil {
			log.Printf("config: could not write defaults to %s: %v", cfgPath, saveErr)
		}
	}

	// 3. Initialize logging.
	logger, err := logging.New(&cfg.Logging, muxHome)
	if err != nil {
		log.Fatalf("logging: %v", err)
	}
	defer logger.Close()
	logger.Info("mux starting", "version", version)

	// 4. Open SQLite memory store.
	dbPath := cfg.Memory.DBPath
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(muxHome, dbPath)
	}
	store, err := memory.New(dbPath)
	if err != nil {
		logger.Error("memory: failed to open database", "error", err)
		log.Fatalf("memory: %v", err)
	}
	defer store.Close()
	logger.Info("memory loaded", "db", dbPath)

	// 5. Find xnc wrapper binary.
	wrapperPath := findWrapper()
	logger.Info("wrapper found", "path", wrapperPath)

	// 6. Initialize tool registry.
	registry := tools.NewRegistry()
	tools.RegisterAll(registry, cfg, cfgPath, store, wrapperPath)
	logger.Info("tools registered", "count", len(registry.Definitions()))

	// 7. Create OpenAI adapter (implements loop.ChatClient).
	openaiClient := newOpenAIAdapter(cfg)

	// 8. Create the agentic loop.
	muxLoop := loop.New(cfg, openaiClient)

	// Register tools from the registry.
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

	// Set observability hooks.
	muxLoop.OnToolCall = func(name string, args map[string]any, duration time.Duration) {
		logger.LogToolCall(name, args, "", duration, nil)
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

	// 9. Create context assembler and prompt builder.
	assembler := memory.NewAssembler(store)
	promptBuilder := prompt.New(cfg)

	// 10. Create Telegram bot.
	telegramBot, err := bot.New(&cfg.Telegram)
	if err != nil {
		logger.Error("telegram bot init failed", "error", err)
		log.Fatalf("telegram: %v", err)
	}

	// 11. Context with cancellation for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 12. Media temp directory.
	mediaTmpDir := filepath.Join(muxHome, "media_tmp")
	if err := os.MkdirAll(mediaTmpDir, 0755); err != nil {
		log.Fatalf("failed to create media temp dir: %v", err)
	}

	// 13. Wire the message handler.
	// We need to map userID (string) to a chatID (int64) for sending.
	// For private Telegram chats, chat ID equals the user's numeric ID.
	var (
		turnMu     sync.Mutex // serialize turns (one message at a time)
		lastChatID int64      // store chat ID derived from user ID
	)

	// runTurn executes the agentic loop and sends the response to Telegram,
	// handling media markers in the response.
	runTurn := func(chatID int64, userText string) {
		// Assemble context.
		ctxData, err := assembler.Assemble(userText)
		if err != nil {
			logger.Error("context assembly failed", "error", err)
			telegramBot.Send(chatID, "Internal error assembling context.")
			return
		}

		// Build system prompt.
		systemPrompt := promptBuilder.Build(ctxData.Agents, ctxData.Facts, ctxData.Compactions, ctxData.Rules)
		muxLoop.SetSystemPrompt(systemPrompt)

		// Store user message.
		if err := store.AddMessage(memory.Message{
			Role:    "user",
			Content: userText,
			Stream:  "conversation",
		}); err != nil {
			logger.Error("failed to store user message", "error", err)
		}

		// Send typing indicator.
		telegramBot.SendTyping(chatID)

		// Run the agentic loop.
		response, err := muxLoop.Run(ctx, userText)
		if err != nil {
			logger.Error("loop error", "error", err)
			telegramBot.Send(chatID, fmt.Sprintf("Error: %v", err))
			return
		}

		// Store assistant response.
		if err := store.AddMessage(memory.Message{
			Role:    "assistant",
			Content: response,
			Stream:  "conversation",
		}); err != nil {
			logger.Error("failed to store assistant message", "error", err)
		}

		// Parse media markers from response.
		cleanText, attachments := media.Parse(response)

		// Send text part.
		if cleanText != "" {
			if err := telegramBot.Send(chatID, cleanText); err != nil {
				logger.Error("telegram send failed", "error", err)
			}
		}

		// Send media attachments.
		for _, att := range attachments {
			sendAttachment(ctx, telegramBot, logger, chatID, att, mediaTmpDir)
		}

		logger.Info("turn complete", "input_len", len(userText), "output_len", len(response), "attachments", len(attachments))
	}

	telegramBot.SetOnMessage(func(userID string, text string) {
		turnMu.Lock()
		defer turnMu.Unlock()

		chatID, _ := strconv.ParseInt(userID, 10, 64)
		lastChatID = chatID

		logger.LogIncoming(userID, text, "text")
		runTurn(chatID, text)
	})

	// 14. Handle voice messages — download, transcribe via Whisper, run as text.
	telegramBot.SetOnVoice(func(userID string, fileID string) {
		turnMu.Lock()
		defer turnMu.Unlock()

		chatID, _ := strconv.ParseInt(userID, 10, 64)
		lastChatID = chatID
		logger.LogIncoming(userID, fileID, "voice")

		if !cfg.Voice.Enabled {
			telegramBot.Send(chatID, "Voice messages are disabled.")
			return
		}

		// Download voice file from Telegram.
		voicePath := filepath.Join(mediaTmpDir, fmt.Sprintf("voice_%s_%d.ogg", userID, time.Now().UnixNano()))
		if err := telegramBot.DownloadFile(fileID, voicePath); err != nil {
			logger.Error("voice download failed", "error", err)
			telegramBot.Send(chatID, fmt.Sprintf("Failed to download voice: %v", err))
			return
		}
		defer os.Remove(voicePath)

		// Transcribe via Whisper.
		telegramBot.SendTyping(chatID)
		transcript, err := voice.Transcribe(ctx, voicePath, cfg.OpenAI.APIKey, cfg.OpenAI.WhisperModel)
		if err != nil {
			logger.Error("transcription failed", "error", err)
			telegramBot.Send(chatID, fmt.Sprintf("Transcription failed: %v", err))
			return
		}

		if strings.TrimSpace(transcript) == "" {
			telegramBot.Send(chatID, "Could not transcribe voice message (empty result).")
			return
		}

		// Show transcription if configured.
		if cfg.Voice.ShowTranscription {
			telegramBot.Send(chatID, fmt.Sprintf("_Heard:_ %s", transcript))
		}

		logger.Info("voice transcribed", "text", transcript)

		// Run the transcribed text through the agentic loop.
		runTurn(chatID, transcript)
	})

	// 15. Handle media messages (photos, documents).
	telegramBot.SetOnMedia(func(userID string, fileID string, mediaType string, caption string) {
		turnMu.Lock()
		defer turnMu.Unlock()

		chatID, _ := strconv.ParseInt(userID, 10, 64)
		lastChatID = chatID
		logger.LogIncoming(userID, fileID, mediaType)

		// Download file from Telegram.
		ext := ".bin"
		switch mediaType {
		case "photo":
			ext = ".jpg"
		case "document":
			// Keep generic — we don't have the original filename easily.
			ext = ".dat"
		}
		destPath := filepath.Join(mediaTmpDir, fmt.Sprintf("%s_%s_%d%s", mediaType, userID, time.Now().UnixNano(), ext))
		if err := telegramBot.DownloadFile(fileID, destPath); err != nil {
			logger.Error("media download failed", "error", err, "type", mediaType)
			telegramBot.Send(chatID, fmt.Sprintf("Failed to download %s: %v", mediaType, err))
			return
		}

		// Build a message describing the media for the mux loop.
		var userText string
		if caption != "" {
			userText = fmt.Sprintf("[User sent a %s with caption: %s]\nThe file has been saved to: %s", mediaType, caption, destPath)
		} else {
			userText = fmt.Sprintf("[User sent a %s]\nThe file has been saved to: %s", mediaType, destPath)
		}

		logger.Info("media received", "type", mediaType, "path", destPath, "caption", caption)

		runTurn(chatID, userText)
	})

	// 16. Handle commands.
	telegramBot.SetOnCommand(func(userID string, cmd bot.Command) {
		chatID, _ := strconv.ParseInt(userID, 10, 64)
		logger.Info("command received", "user", userID, "command", cmd.Name, "agent", cmd.Agent)

		switch cmd.Name {
		case "help":
			helpText := `*Available commands:*
/help — Show this help message
/agents — List all agents with emoji + name + status
/stats — Show memory database statistics
/costs — Show today's cost summary
/clear — Clear all mux memory (requires confirmation)
/clear confirm — Confirm memory clear

All other messages are handled by the mux AI.`
			telegramBot.Send(chatID, helpText)

		case "agents", "list":
			out, err := tools.RunWrapper(ctx, wrapperPath, "list", "--json")
			if err != nil {
				telegramBot.Send(chatID, fmt.Sprintf("Error listing agents: %v", err))
				return
			}
			// Also show agent states from memory.
			states, _ := store.AllAgentStates()
			if len(states) == 0 && out == "" {
				telegramBot.Send(chatID, "No agents found.")
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
			if out != "" {
				lines = append(lines, "", "_Raw:_", out)
			}
			telegramBot.Send(chatID, strings.Join(lines, "\n"))

		case "stats":
			stats, err := store.Stats()
			if err != nil {
				telegramBot.Send(chatID, fmt.Sprintf("Error: %v", err))
				return
			}
			text := fmt.Sprintf(`*Memory stats:*
Messages: %d
Facts: %d
Compactions: %d
Costs: %d
Agent states: %d`,
				stats["messages"], stats["facts"], stats["compactions"],
				stats["costs"], stats["agent_state"])
			telegramBot.Send(chatID, text)

		case "costs":
			now := time.Now()
			startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			summary, err := store.CostSummary(startOfDay, now)
			if err != nil {
				telegramBot.Send(chatID, fmt.Sprintf("Error: %v", err))
				return
			}
			if len(summary) == 0 {
				telegramBot.Send(chatID, "No costs recorded today.")
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
			telegramBot.Send(chatID, strings.Join(lines, "\n"))

		case "clear":
			if cmd.Args == "confirm" {
				// Actually clear.
				if err := store.ClearAll(); err != nil {
					telegramBot.Send(chatID, fmt.Sprintf("Error clearing memory: %v", err))
					return
				}
				muxLoop.ClearHistory()
				telegramBot.Send(chatID, "Memory cleared. Conversation history, facts, compactions, and costs have been wiped. Agent state preserved.")
				logger.Info("memory cleared by user", "user", userID)
			} else {
				telegramBot.Send(chatID, "⚠️ This will delete all conversation history, facts, compactions, and cost records. Agent state is preserved.\n\nSend /clear confirm to proceed.")
			}

		default:
			// Forward unhandled commands to the mux loop as text.
			telegramBot.Send(chatID, fmt.Sprintf("Unknown command /%s. Use /help for available commands.", cmd.Name))
		}
	})

	// 17. Start Telegram bot in background.
	go func() {
		logger.Info("telegram polling started")
		if err := telegramBot.Start(ctx); err != nil && ctx.Err() == nil {
			logger.Error("telegram error", "error", err)
		}
	}()

	// 18. Auto-start agents.
	go func() {
		for _, agent := range cfg.Agents.AutoStart {
			logger.LogLifecycle("auto-starting", agent, "")
			out, err := tools.RunWrapper(ctx, wrapperPath, agent, "start", "--mux")
			if err != nil {
				logger.Error("auto-start failed", "agent", agent, "error", err)
			} else {
				logger.LogLifecycle("started", agent, out)
			}
		}
	}()

	logger.Info("mux online")

	// 19. Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutdown signal received", "signal", sig.String())

	// 20. Graceful shutdown.
	// Stop Telegram polling first to stop accepting new messages.
	telegramBot.Stop()

	// Stop mux-managed agents.
	for _, agent := range cfg.Agents.MuxManaged {
		logger.LogLifecycle("stopping", agent, "shutdown")
		if _, err := tools.RunWrapper(context.Background(), wrapperPath, agent, "stop"); err != nil {
			logger.Error("agent stop failed", "agent", agent, "error", err)
		}
	}

	// Send goodbye message if we have a chat ID.
	if lastChatID != 0 {
		telegramBot.Send(lastChatID, "Mux going offline. Agents stopped.")
	}

	// Cancel context (stops any in-flight operations).
	cancel()

	logger.Info("mux shutdown complete")
}

// ---------------------------------------------------------------------------
// findWrapper locates the xnc wrapper binary.
// ---------------------------------------------------------------------------

func findWrapper() string {
	// 1. Check PATH.
	if p, err := exec.LookPath("xnc"); err == nil {
		return p
	}

	// 2. Same directory as the running binary (co-located fallback).
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "xnc")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}

	log.Fatal("xnc wrapper not found in PATH or next to xnc-mux binary")
	return ""
}

// ---------------------------------------------------------------------------
// OpenAI adapter — implements loop.ChatClient using net/http
// ---------------------------------------------------------------------------

// openAIAdapter bridges the OpenAI chat completions API to loop.ChatClient.
type openAIAdapter struct {
	apiKey      string
	model       string
	temperature float64
	httpClient  *http.Client
	baseURL     string
}

func newOpenAIAdapter(cfg *config.Config) *openAIAdapter {
	return &openAIAdapter{
		apiKey:      cfg.OpenAI.APIKey,
		model:       cfg.OpenAI.Model,
		temperature: cfg.OpenAI.Temperature,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		baseURL: "https://api.openai.com/v1",
	}
}

// Complete implements loop.ChatClient.
func (a *openAIAdapter) Complete(ctx context.Context, req loop.ChatRequest) (loop.ChatResponse, error) {
	// Build the messages array for the OpenAI API.
	var messages []oaiMessage

	// System prompt.
	if req.SystemPrompt != "" {
		messages = append(messages, oaiMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	// Conversation messages.
	for _, m := range req.Messages {
		msg := oaiMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				msg.ToolCalls = append(msg.ToolCalls, oaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: oaiFunction{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}
		messages = append(messages, msg)
	}

	// Build tools array.
	var oaiTools []oaiTool
	for _, t := range req.Tools {
		oaiTools = append(oaiTools, oaiTool{
			Type: "function",
			Function: oaiToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	// Build request body.
	body := oaiRequest{
		Model:       req.Model,
		Messages:    messages,
		Temperature: req.Temperature,
	}
	if len(oaiTools) > 0 {
		body.Tools = oaiTools
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	// Make HTTP request.
	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/chat/completions", bytes.NewReader(bodyJSON))
	if err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return loop.ChatResponse{}, fmt.Errorf("openai: API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response.
	var oaiResp oaiResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: parse response: %w", err)
	}

	result := loop.ChatResponse{
		InputTokens:  oaiResp.Usage.PromptTokens,
		OutputTokens: oaiResp.Usage.CompletionTokens,
	}

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]
		result.Text = choice.Message.Content

		for _, tc := range choice.Message.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					// If we can't parse args, pass raw string as a single arg.
					args = map[string]any{"_raw": tc.Function.Arguments}
				}
			}
			result.ToolCalls = append(result.ToolCalls, loop.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: args,
			})
		}
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// OpenAI API types (minimal, for chat completions with function calling)
// ---------------------------------------------------------------------------

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	Temperature float64      `json:"temperature"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string          `json:"type"`
	Function oaiToolFunction `json:"function"`
}

type oaiToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Usage   oaiUsage    `json:"usage"`
}

type oaiChoice struct {
	Message oaiMessage `json:"message"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ---------------------------------------------------------------------------
// Media attachment sender
// ---------------------------------------------------------------------------

// sendAttachment extracts a file from an agent container (if needed) and sends it
// via Telegram. For container paths (/nullclaw-data/...) it uses docker cp.
// For host paths it sends directly.
func sendAttachment(ctx context.Context, tgBot *bot.Bot, logger *logging.Logger, chatID int64, att media.Attachment, tmpDir string) {
	hostPath := att.Path

	// If the path looks like it's inside a container (starts with /nullclaw-data),
	// we can't send it directly — the mux LLM will have already used get_agent_file
	// to extract it. The path in the marker should be a host path at that point.
	if strings.HasPrefix(att.Path, "/nullclaw-data/") {
		logger.Error("attachment path appears to be a container path, not a host path", "path", att.Path)
		tgBot.Send(chatID, fmt.Sprintf("Could not send attachment: %s (container path — use get_agent_file first)", att.Path))
		return
	}

	// Verify file exists on host.
	if _, err := os.Stat(hostPath); err != nil {
		logger.Error("attachment file not found", "path", hostPath, "error", err)
		tgBot.Send(chatID, fmt.Sprintf("Attachment not found: %s", filepath.Base(hostPath)))
		return
	}

	ext := strings.ToLower(filepath.Ext(hostPath))

	var err error
	switch att.Type {
	case media.TypeImage:
		// Images: send as inline photo.
		err = tgBot.SendPhoto(chatID, hostPath, "")

	case media.TypeVideo:
		// Videos: send as playable video.
		err = tgBot.SendVideo(chatID, hostPath, "")

	case media.TypeVoice:
		// Voice: ogg/opus as voice note, others as audio.
		if ext == ".ogg" || ext == ".oga" || ext == ".opus" {
			err = tgBot.SendVoice(chatID, hostPath)
		} else {
			err = tgBot.SendAudio(chatID, hostPath, "")
		}

	case media.TypeAudio:
		// Audio: send as playable audio (mp3, m4a, etc).
		err = tgBot.SendAudio(chatID, hostPath, "")

	default:
		// FILE, DOCUMENT — send as downloadable document.
		err = tgBot.SendDocument(chatID, hostPath, "")
	}

	if err != nil {
		logger.Error("send attachment failed", "type", att.Type, "path", hostPath, "error", err)
		tgBot.Send(chatID, fmt.Sprintf("Failed to send %s: %v", att.Type, err))
	}
}
