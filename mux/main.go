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
	"sync"
	"syscall"
	"time"

	"github.com/jotavich/xnullclaw/mux/bot"
	"github.com/jotavich/xnullclaw/mux/config"
	"github.com/jotavich/xnullclaw/mux/logging"
	"github.com/jotavich/xnullclaw/mux/loop"
	"github.com/jotavich/xnullclaw/mux/memory"
	"github.com/jotavich/xnullclaw/mux/prompt"
	"github.com/jotavich/xnullclaw/mux/tools"
)

var version = "dev"

func main() {
	// 1. Determine mux home directory.
	muxHome := filepath.Join(os.Getenv("HOME"), ".xnullclaw", ".mux")
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

	// 5. Find xnullclaw wrapper binary.
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

	// 12. Wire the message handler.
	// We need to map userID (string) to a chatID (int64) for sending.
	// For private Telegram chats, chat ID equals the user's numeric ID.
	var (
		turnMu     sync.Mutex // serialize turns (one message at a time)
		lastChatID int64      // store chat ID derived from user ID
	)

	telegramBot.SetOnMessage(func(userID string, text string) {
		turnMu.Lock()
		defer turnMu.Unlock()

		chatID, _ := strconv.ParseInt(userID, 10, 64)
		lastChatID = chatID

		logger.LogIncoming(userID, text, "text")

		// Assemble context.
		ctxData, err := assembler.Assemble(text)
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
			Content: text,
			Stream:  "conversation",
		}); err != nil {
			logger.Error("failed to store user message", "error", err)
		}

		// Send typing indicator.
		telegramBot.SendTyping(chatID)

		// Run the agentic loop.
		response, err := muxLoop.Run(ctx, text)
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

		// Send response to Telegram.
		if err := telegramBot.Send(chatID, response); err != nil {
			logger.Error("telegram send failed", "error", err)
		}

		logger.Info("turn complete", "input_len", len(text), "output_len", len(response))
	})

	// 13. Handle voice messages (Phase 3 stub).
	telegramBot.SetOnVoice(func(userID string, fileID string) {
		chatID, _ := strconv.ParseInt(userID, 10, 64)
		telegramBot.Send(chatID, "Voice messages not yet supported. Send text instead.")
	})

	// 14. Handle commands.
	telegramBot.SetOnCommand(func(userID string, cmd bot.Command) {
		chatID, _ := strconv.ParseInt(userID, 10, 64)
		logger.Info("command received", "user", userID, "command", cmd.Name, "agent", cmd.Agent)
		telegramBot.Send(chatID, fmt.Sprintf("Command /%s received. Processing via mux loop.", cmd.Name))
	})

	// 15. Start Telegram bot in background.
	go func() {
		logger.Info("telegram polling started")
		if err := telegramBot.Start(ctx); err != nil && ctx.Err() == nil {
			logger.Error("telegram error", "error", err)
		}
	}()

	// 16. Auto-start agents.
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

	// 17. Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutdown signal received", "signal", sig.String())

	// 18. Graceful shutdown.
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
// findWrapper locates the xnullclaw binary.
// ---------------------------------------------------------------------------

func findWrapper() string {
	// 1. Same directory as the running binary (co-located install).
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "xnullclaw")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}

	// 2. Check PATH.
	if p, err := exec.LookPath("xnullclaw"); err == nil {
		return p
	}

	// 3. Hardcoded default.
	if info, err := os.Stat("/usr/local/bin/xnullclaw"); err == nil && !info.IsDir() {
		return "/usr/local/bin/xnullclaw"
	}

	log.Fatal("xnullclaw wrapper not found next to xnc-mux binary, in PATH, or at /usr/local/bin/xnullclaw")
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
