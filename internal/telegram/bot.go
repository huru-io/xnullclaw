// Package telegram provides the Telegram bot interface for mux.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// maxMessageLen is the safe limit per Telegram message (4096 minus margin for prefix).
const maxMessageLen = 3800

// Priority levels for the send queue (lower = higher priority).
const (
	PriorityNormal    = 0
	PriorityAlert     = 1
	PriorityHeartbeat = 2
)

// sendRequest is an item in the rate-limited send queue.
type sendRequest struct {
	chattable tgbotapi.Chattable
	priority  int
	errCh     chan error
}

// Command represents a parsed Telegram /command.
type Command struct {
	Name    string // e.g. "dm", "list", "status"
	Agent   string // target agent for dm/status/start/stop etc.
	Args    string // remaining text after command + agent
	RawText string // full original message text
}

// Bot handles Telegram communication.
//
// Group mode (GroupID != 0):
//
//	TopicID = -1  discovery mode: log incoming thread IDs, don't process
//	TopicID =  0  no topic filtering, no message_thread_id in sends (non-forum groups)
//	TopicID =  1  General topic in forum groups (filter + send to thread 1)
//	TopicID >  1  specific topic (filter + send to that thread)
//
// Private mode (GroupID == 0): only accept private chats.
type Bot struct {
	api       *tgbotapi.BotAPI
	allowFrom map[string]bool // allowed user IDs (string)
	groupID   int64           // configured group chat ID (0 = private mode)
	topicID   int             // configured forum topic ID (-1 = discover, 0 = all, >0 = specific)
	sendQueue chan sendRequest // rate-limited send queue
	onMessage   func(chatID int64, userID string, text string)
	onVoice     func(chatID int64, userID string, fileID string)
	onMedia     func(chatID int64, userID string, fileID string, mediaType string, caption string, fileName string)
	onCommand   func(chatID int64, userID string, cmd Command)
	onDiscovery func(chatID int64, userID string, threadID int, text string)
	stopCh      chan struct{}
	stopOnce    sync.Once

	// Token bucket for rate limiting.
	bucketMu     sync.Mutex
	bucketTokens float64
	bucketMax    float64
	bucketRate   float64 // tokens per second
	bucketLast   time.Time
}

// New creates a new Bot from the given Telegram config.
func New(cfg *config.TelegramConfig) (*Bot, error) {
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("bot: telegram bot_token is required")
	}

	api, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("bot: invalid or expired Telegram token — verify with @BotFather and update bot_token in mux config: %w", err)
	}

	allow := make(map[string]bool, len(cfg.AllowFrom))
	for _, id := range cfg.AllowFrom {
		allow[id] = true
	}

	b := &Bot{
		api:          api,
		allowFrom:    allow,
		groupID:      cfg.GroupID,
		topicID:      cfg.TopicID,
		sendQueue:    make(chan sendRequest, 256),
		stopCh:       make(chan struct{}),
		bucketTokens: 3, // start with full burst
		bucketMax:    3,
		bucketRate:   1, // 1 token/sec sustained
		bucketLast:   time.Now(),
	}
	return b, nil
}

// SetOnMessage sets the callback for incoming text messages.
func (b *Bot) SetOnMessage(fn func(chatID int64, userID string, text string)) {
	b.onMessage = fn
}

// SetOnVoice sets the callback for incoming voice messages.
func (b *Bot) SetOnVoice(fn func(chatID int64, userID string, fileID string)) {
	b.onVoice = fn
}

// SetOnMedia sets the callback for incoming media (photos, documents).
func (b *Bot) SetOnMedia(fn func(chatID int64, userID string, fileID string, mediaType string, caption string, fileName string)) {
	b.onMedia = fn
}

// SetOnCommand sets the callback for parsed /commands.
func (b *Bot) SetOnCommand(fn func(chatID int64, userID string, cmd Command)) {
	b.onCommand = fn
}

// SetOnDiscovery sets the callback for topic discovery mode messages.
func (b *Bot) SetOnDiscovery(fn func(chatID int64, userID string, threadID int, text string)) {
	b.onDiscovery = fn
}

// Start begins long-polling for messages. It blocks until ctx is cancelled
// or Stop is called. Uses manual getUpdates to extract message_thread_id
// which is not exposed by the library (predates Telegram forum support).
func (b *Bot) Start(ctx context.Context) error {
	// Start the send queue processor.
	go b.processSendQueue(ctx)

	offset := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.stopCh:
			return nil
		default:
		}

		params := tgbotapi.Params{}
		params.AddNonZero("offset", offset)
		params.AddNonZero("timeout", 60)

		resp, err := b.api.MakeRequest("getUpdates", params)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		var rawUpdates []json.RawMessage
		if err := json.Unmarshal(resp.Result, &rawUpdates); err != nil {
			time.Sleep(time.Second)
			continue
		}

		for _, raw := range rawUpdates {
			var update tgbotapi.Update
			if err := json.Unmarshal(raw, &update); err != nil {
				continue
			}

			// Extract message_thread_id (not in library v5.5.1).
			threadID := extractThreadID(raw)

			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}

			b.handleUpdate(update, threadID)
		}
	}
}

// Stop gracefully stops the bot.
func (b *Bot) Stop() {
	b.stopOnce.Do(func() {
		close(b.stopCh)
	})
}

// rawThreadInfo extracts message_thread_id from raw update JSON.
type rawThreadInfo struct {
	Message *struct {
		MessageThreadID int `json:"message_thread_id"`
	} `json:"message"`
}

func extractThreadID(raw json.RawMessage) int {
	var info rawThreadInfo
	if err := json.Unmarshal(raw, &info); err != nil || info.Message == nil {
		return 0
	}
	return info.Message.MessageThreadID
}

// handleUpdate processes a single incoming update.
func (b *Bot) handleUpdate(update tgbotapi.Update, threadID int) {
	if update.Message == nil {
		return
	}

	msg := update.Message
	chatID := msg.Chat.ID
	userID := ""
	if msg.From != nil {
		userID = fmt.Sprintf("%d", msg.From.ID)
	}

	// Chat filtering.
	if b.groupID != 0 {
		// Discovery mode (topic_id = -1): log ALL incoming messages
		// regardless of group_id so the user can find the right IDs.
		if b.topicID == -1 {
			if b.onDiscovery != nil {
				b.onDiscovery(chatID, userID, threadID, msg.Text)
			}
			return
		}

		// Group mode: only accept messages from the configured group.
		if chatID != b.groupID {
			return
		}

		// Specific topic mode (topic_id > 0): only accept that thread.
		if b.topicID > 0 && threadID != b.topicID {
			return
		}

		// topic_id == 0: accept all topics (no filtering).
	} else {
		// Private mode: only accept private chats.
		if msg.Chat.Type != "private" {
			return
		}
	}

	// User filtering (works in both modes).
	if len(b.allowFrom) > 0 && !b.allowFrom[userID] {
		return
	}

	// Voice message.
	if msg.Voice != nil {
		if b.onVoice != nil {
			b.onVoice(chatID, userID, msg.Voice.FileID)
		}
		return
	}

	// Photo message.
	if len(msg.Photo) > 0 {
		// Use the largest photo (last in the slice).
		largest := msg.Photo[len(msg.Photo)-1]
		if b.onMedia != nil {
			b.onMedia(chatID, userID, largest.FileID, "photo", msg.Caption, "")
		}
		return
	}

	// Document message.
	if msg.Document != nil {
		if b.onMedia != nil {
			b.onMedia(chatID, userID, msg.Document.FileID, "document", msg.Caption, msg.Document.FileName)
		}
		return
	}

	// Text message.
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	// Check for /commands.
	if strings.HasPrefix(text, "/") {
		cmd := ParseCommand(text)
		if cmd != nil {
			if b.onCommand != nil {
				b.onCommand(chatID, userID, *cmd)
			}
			return
		}
	}

	// Plain text message.
	if b.onMessage != nil {
		b.onMessage(chatID, userID, text)
	}
}

// ParseCommand parses a /command string into a Command struct.
// Returns nil if the text is not a recognized command.
func ParseCommand(text string) *Command {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return nil
	}

	// Remove leading slash and split.
	parts := strings.Fields(text[1:])
	if len(parts) == 0 {
		return nil
	}

	// Strip bot username suffix (e.g., /list@mybot).
	cmdName := strings.ToLower(parts[0])
	if at := strings.Index(cmdName, "@"); at != -1 {
		cmdName = cmdName[:at]
	}

	cmd := &Command{
		Name:    cmdName,
		RawText: text,
	}

	switch cmdName {
	case "dm":
		// /dm <agent> <message>
		if len(parts) >= 3 {
			cmd.Agent = parts[1]
			cmd.Args = strings.Join(parts[2:], " ")
		} else if len(parts) == 2 {
			cmd.Agent = parts[1]
		}
	case "switch", "start", "stop", "restart", "status", "history", "costs":
		// /cmd [agent]
		if len(parts) >= 2 {
			cmd.Agent = parts[1]
			if len(parts) > 2 {
				cmd.Args = strings.Join(parts[2:], " ")
			}
		}
	case "broadcast":
		// /broadcast <message>
		if len(parts) >= 2 {
			cmd.Args = strings.Join(parts[1:], " ")
		}
	case "config":
		// /config <agent> <key> <value>
		if len(parts) >= 2 {
			cmd.Agent = parts[1]
			if len(parts) > 2 {
				cmd.Args = strings.Join(parts[2:], " ")
			}
		}
	case "budget":
		// /budget [limit]
		if len(parts) >= 2 {
			cmd.Args = strings.Join(parts[1:], " ")
		}
	case "list", "agents", "mux", "help", "stats":
		// no arguments needed
	case "clear":
		// /clear or /clear confirm
		if len(parts) >= 2 {
			cmd.Args = strings.Join(parts[1:], " ")
		}
	default:
		return nil // unrecognized command
	}

	return cmd
}

// Send sends a text message to the user, auto-splitting if it exceeds Telegram's limit.
func (b *Bot) Send(chatID int64, text string) error {
	if text == "" {
		return nil
	}
	parts := SplitMessage(text, maxMessageLen)
	var lastErr error
	for _, part := range parts {
		msg := tgbotapi.NewMessage(chatID, part)
		msg.ParseMode = "Markdown"
		if err := b.enqueueSend(msg, PriorityNormal); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// SendTyping sends a "typing..." indicator.
func (b *Bot) SendTyping(chatID int64) error {
	action := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	return b.enqueueSend(action, PriorityHeartbeat)
}

// SendPhoto sends a photo to the user.
func (b *Bot) SendPhoto(chatID int64, filePath string, caption string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("bot: open photo %s: %w", filePath, err)
	}
	f.Close()

	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
	photo.Caption = caption
	return b.enqueueSend(photo, PriorityNormal)
}

// SendDocument sends a document to the user.
func (b *Bot) SendDocument(chatID int64, filePath string, caption string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("bot: open document %s: %w", filePath, err)
	}
	f.Close()

	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
	doc.Caption = caption
	return b.enqueueSend(doc, PriorityNormal)
}

// DownloadFile downloads a Telegram file by its fileID to the given destination path.
func (b *Bot) DownloadFile(fileID, destPath string) error {
	url, err := b.api.GetFileDirectURL(fileID)
	if err != nil {
		return fmt.Errorf("bot: get file URL: %w", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("bot: download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bot: download file HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("bot: create dest file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("bot: write file: %w", err)
	}

	return nil
}

// SendVoice sends a voice message (ogg/opus only; for other audio use SendAudio).
func (b *Bot) SendVoice(chatID int64, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("bot: open voice %s: %w", filePath, err)
	}
	f.Close()

	voice := tgbotapi.NewVoice(chatID, tgbotapi.FilePath(filePath))
	return b.enqueueSend(voice, PriorityNormal)
}

// SendAudio sends an audio file as a playable audio message (mp3, m4a, etc).
func (b *Bot) SendAudio(chatID int64, filePath string, caption string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("bot: open audio %s: %w", filePath, err)
	}
	f.Close()

	audio := tgbotapi.NewAudio(chatID, tgbotapi.FilePath(filePath))
	audio.Caption = caption
	return b.enqueueSend(audio, PriorityNormal)
}

// SendVideo sends a video file.
func (b *Bot) SendVideo(chatID int64, filePath string, caption string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("bot: open video %s: %w", filePath, err)
	}
	f.Close()

	video := tgbotapi.NewVideo(chatID, tgbotapi.FilePath(filePath))
	video.Caption = caption
	return b.enqueueSend(video, PriorityNormal)
}

// enqueueSend puts a message on the send queue and waits for the result.
func (b *Bot) enqueueSend(c tgbotapi.Chattable, priority int) error {
	req := sendRequest{
		chattable: c,
		priority:  priority,
		errCh:     make(chan error, 1),
	}

	select {
	case b.sendQueue <- req:
	case <-b.stopCh:
		return fmt.Errorf("bot: stopped")
	}

	select {
	case err := <-req.errCh:
		return err
	case <-b.stopCh:
		return fmt.Errorf("bot: stopped")
	}
}

// processSendQueue is the single goroutine that sends all messages,
// enforcing rate limits via a token bucket.
func (b *Bot) processSendQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stopCh:
			return
		case req := <-b.sendQueue:
			b.waitForToken()
			err := b.doSend(req.chattable)
			req.errCh <- err
		}
	}
}

// waitForToken blocks until a send token is available (token bucket algorithm).
func (b *Bot) waitForToken() {
	for {
		b.bucketMu.Lock()
		now := time.Now()
		elapsed := now.Sub(b.bucketLast).Seconds()
		b.bucketTokens += elapsed * b.bucketRate
		if b.bucketTokens > b.bucketMax {
			b.bucketTokens = b.bucketMax
		}
		b.bucketLast = now

		if b.bucketTokens >= 1 {
			b.bucketTokens--
			b.bucketMu.Unlock()
			return
		}

		// Calculate wait time for next token.
		deficit := 1.0 - b.bucketTokens
		wait := time.Duration(deficit/b.bucketRate*1000) * time.Millisecond
		b.bucketMu.Unlock()
		time.Sleep(wait)
	}
}

// doSend performs the actual Telegram API call, handling 429 retries.
// When topicID > 0, injects message_thread_id into the request.
func (b *Bot) doSend(c tgbotapi.Chattable) error {
	for attempts := 0; attempts < 5; attempts++ {
		var err error
		if b.topicID > 0 {
			_, err = b.sendWithTopic(c)
		} else {
			_, err = b.api.Request(c)
		}
		if err == nil {
			return nil
		}

		// Check for rate limit (429).
		if apiErr, ok := err.(*tgbotapi.Error); ok && apiErr.Code == 429 {
			retryAfter := apiErr.RetryAfter
			if retryAfter <= 0 {
				retryAfter = 1
			}
			log.Printf("bot: rate limited, retrying after %ds", retryAfter)
			time.Sleep(time.Duration(retryAfter) * time.Second)
			continue
		}

		return fmt.Errorf("bot: send failed: %w", err)
	}
	return fmt.Errorf("bot: send failed after retries")
}

// sendWithTopic sends a message with message_thread_id injected.
// The go-telegram-bot-api v5.5.1 predates Telegram forum support,
// so we build params manually and call MakeRequest/UploadFiles directly.
func (b *Bot) sendWithTopic(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	threadStr := strconv.Itoa(b.topicID)

	switch v := c.(type) {
	case tgbotapi.MessageConfig:
		p := tgbotapi.Params{}
		p.AddFirstValid("chat_id", v.BaseChat.ChatID, v.BaseChat.ChannelUsername)
		p.AddNonEmpty("text", v.Text)
		p.AddNonEmpty("parse_mode", v.ParseMode)
		p.AddBool("disable_web_page_preview", v.DisableWebPagePreview)
		p["message_thread_id"] = threadStr
		return b.api.MakeRequest("sendMessage", p)

	case tgbotapi.ChatActionConfig:
		p := tgbotapi.Params{}
		p.AddFirstValid("chat_id", v.BaseChat.ChatID, v.BaseChat.ChannelUsername)
		p["action"] = string(v.Action)
		p["message_thread_id"] = threadStr
		return b.api.MakeRequest("sendChatAction", p)

	case tgbotapi.PhotoConfig:
		p := tgbotapi.Params{}
		p.AddFirstValid("chat_id", v.BaseChat.ChatID, v.BaseChat.ChannelUsername)
		p.AddNonEmpty("caption", v.Caption)
		p.AddNonEmpty("parse_mode", v.ParseMode)
		p["message_thread_id"] = threadStr
		return b.api.UploadFiles("sendPhoto", p, []tgbotapi.RequestFile{{
			Name: "photo",
			Data: v.File,
		}})

	case tgbotapi.DocumentConfig:
		p := tgbotapi.Params{}
		p.AddFirstValid("chat_id", v.BaseChat.ChatID, v.BaseChat.ChannelUsername)
		p.AddNonEmpty("caption", v.Caption)
		p.AddNonEmpty("parse_mode", v.ParseMode)
		p["message_thread_id"] = threadStr
		return b.api.UploadFiles("sendDocument", p, []tgbotapi.RequestFile{{
			Name: "document",
			Data: v.File,
		}})

	case tgbotapi.VoiceConfig:
		p := tgbotapi.Params{}
		p.AddFirstValid("chat_id", v.BaseChat.ChatID, v.BaseChat.ChannelUsername)
		p.AddNonEmpty("caption", v.Caption)
		p["message_thread_id"] = threadStr
		return b.api.UploadFiles("sendVoice", p, []tgbotapi.RequestFile{{
			Name: "voice",
			Data: v.File,
		}})

	case tgbotapi.AudioConfig:
		p := tgbotapi.Params{}
		p.AddFirstValid("chat_id", v.BaseChat.ChatID, v.BaseChat.ChannelUsername)
		p.AddNonEmpty("caption", v.Caption)
		p.AddNonEmpty("parse_mode", v.ParseMode)
		p["message_thread_id"] = threadStr
		return b.api.UploadFiles("sendAudio", p, []tgbotapi.RequestFile{{
			Name: "audio",
			Data: v.File,
		}})

	case tgbotapi.VideoConfig:
		p := tgbotapi.Params{}
		p.AddFirstValid("chat_id", v.BaseChat.ChatID, v.BaseChat.ChannelUsername)
		p.AddNonEmpty("caption", v.Caption)
		p.AddNonEmpty("parse_mode", v.ParseMode)
		p["message_thread_id"] = threadStr
		return b.api.UploadFiles("sendVideo", p, []tgbotapi.RequestFile{{
			Name: "video",
			Data: v.File,
		}})

	default:
		return nil, fmt.Errorf("bot: sendWithTopic: unhandled Chattable type %T", c)
	}
}

// truncateLog truncates a string for log output at rune boundaries.
func truncateLog(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// SplitMessage splits a long message into parts that fit within maxLen,
// respecting paragraph boundaries and code blocks.
func SplitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var parts []string
	remaining := text

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			parts = append(parts, remaining)
			break
		}

		cutAt := findSplitPoint(remaining, maxLen)
		parts = append(parts, strings.TrimRight(remaining[:cutAt], "\n\r "))
		remaining = strings.TrimLeft(remaining[cutAt:], "\n\r ")
	}

	return parts
}

// findSplitPoint finds the best position to split text at or before maxLen,
// respecting code blocks and paragraph boundaries.
func findSplitPoint(text string, maxLen int) int {
	if len(text) <= maxLen {
		return len(text)
	}

	// Find code block boundaries within the first maxLen characters.
	// Never split inside a code block.
	codeBlockStart := -1
	pos := 0
	for pos < maxLen {
		idx := strings.Index(text[pos:], "```")
		if idx == -1 {
			break
		}
		absIdx := pos + idx
		if absIdx >= maxLen {
			break
		}
		if codeBlockStart == -1 {
			// Opening fence.
			codeBlockStart = absIdx
		} else {
			// Closing fence — code block is complete, reset.
			codeBlockStart = -1
		}
		pos = absIdx + 3
	}

	// If we're inside an open code block at the maxLen boundary,
	// move the split point before the opening ```.
	effectiveMax := maxLen
	if codeBlockStart != -1 {
		effectiveMax = codeBlockStart
		if effectiveMax == 0 {
			// Edge case: code block starts at the very beginning and is
			// too large. We have to split inside it as a last resort.
			effectiveMax = maxLen
		}
	}

	// 1. Try to split at paragraph boundary (double newline).
	searchArea := text[:effectiveMax]
	if idx := strings.LastIndex(searchArea, "\n\n"); idx > 0 {
		return idx + 2 // include the double newline in the first part for clean cut
	}

	// 2. Try to split at single newline.
	if idx := strings.LastIndex(searchArea, "\n"); idx > 0 {
		return idx + 1
	}

	// 3. Hard cut at effectiveMax.
	return effectiveMax
}
