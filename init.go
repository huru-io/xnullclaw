package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
)

// runInit bootstraps xnc from scratch: directories, credentials, agents.
//
// Modes:
//   - Non-interactive: xnc init --openai-key sk-... --telegram-token 123:ABC -n 3
//   - Interactive:     xnc init (guided wizard with sections)
func runInit(args []string) {
	// Show init help.
	for _, a := range args {
		if a == "--help" || a == "-h" {
			printInitUsage()
			return
		}
	}

	opts := parseInitFlags(args)

	home := opts.home
	muxHome := filepath.Join(home, "mux")

	if err := agent.ValidateHome(home, true); err != nil {
		log.Fatalf("%v", err)
	}

	// 1. Create directory structure.
	for _, dir := range []string{
		home,
		muxHome,
		filepath.Join(muxHome, "logs"),
		filepath.Join(home, ".tmp"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("create %s: %v", dir, err)
		}
	}

	agent.InstanceID(home)
	fmt.Printf(":: home directory: %s\n", home)

	// 2. Load existing config or create default.
	cfgPath := filepath.Join(muxHome, "config.json")
	cfg := config.DefaultConfig()
	if existing, err := config.Load(cfgPath); err == nil {
		cfg = existing
		fmt.Println(":: using existing config")
	}

	interactive := opts.interactive()

	// Read existing agent list and keys so we can preserve them on re-runs.
	existingAgents, _ := agent.ListAll(home)
	existingKeys := readExistingAgentKeysFrom(home, existingAgents)

	// ── Section 1: LLM Provider keys ──

	if interactive {
		fmt.Println()
		fmt.Println("─── LLM Provider Keys ───")
		fmt.Println("API keys for LLM providers. At least one is required.")
		fmt.Println("Keys are shared between the mux and agents.")
		fmt.Println()
	}

	existingOpenAIKey := resolveValue(cfg.OpenAI.APIKey, existingKeys["openai"])
	openaiKey := resolveValue(opts.openaiKey, os.Getenv("OPENAI_API_KEY"), existingOpenAIKey)
	if openaiKey == "" && interactive {
		openaiKey = promptInput("OpenAI API key (or OPENAI_API_KEY env): ")
	} else if interactive && openaiKey != "" && opts.openaiKey == "" {
		inp := promptInput(fmt.Sprintf("OpenAI API key [%s, Enter to keep]: ", redactKey(openaiKey)))
		if inp != "" {
			openaiKey = inp
		}
	}
	if openaiKey != "" {
		cfg.OpenAI.APIKey = openaiKey
	}

	anthropicKey := resolveValue(opts.anthropicKey, os.Getenv("ANTHROPIC_API_KEY"), existingKeys["anthropic"])
	if anthropicKey == "" && interactive {
		anthropicKey = promptInput("Anthropic API key (optional, Enter to skip): ")
	} else if interactive && anthropicKey != "" && opts.anthropicKey == "" {
		inp := promptInput(fmt.Sprintf("Anthropic API key [%s, Enter to keep]: ", redactKey(anthropicKey)))
		if inp != "" {
			anthropicKey = inp
		}
	}

	openrouterKey := resolveValue(opts.openrouterKey, os.Getenv("OPENROUTER_API_KEY"), existingKeys["openrouter"])
	if openrouterKey == "" && interactive {
		openrouterKey = promptInput("OpenRouter API key (optional, Enter to skip): ")
	} else if interactive && openrouterKey != "" && opts.openrouterKey == "" {
		inp := promptInput(fmt.Sprintf("OpenRouter API key [%s, Enter to keep]: ", redactKey(openrouterKey)))
		if inp != "" {
			openrouterKey = inp
		}
	}

	// Validate keys — only test keys that are new or changed from existing config.
	anyKey := openaiKey != "" || anthropicKey != "" || openrouterKey != ""
	anyKeyValid := false
	for _, kv := range []struct{ provider, key, existing string }{
		{"openai", openaiKey, existingOpenAIKey},
		{"anthropic", anthropicKey, existingKeys["anthropic"]},
		{"openrouter", openrouterKey, existingKeys["openrouter"]},
	} {
		if kv.key == "" {
			continue
		}
		if kv.key == kv.existing {
			// Key unchanged from existing config — skip re-validation.
			anyKeyValid = true
			continue
		}
		if err := agent.TestProviderKey(kv.provider, kv.key); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s key: %v\n", kv.provider, err)
		} else {
			fmt.Printf("ok: %s key verified\n", kv.provider)
			anyKeyValid = true
		}
	}
	if !anyKeyValid && anyKey {
		log.Fatal("all provided API keys failed validation")
	}

	// ── Section 2: Models ──

	if interactive {
		fmt.Println()
		fmt.Println("─── Models ───")
		fmt.Println("The mux model runs the Telegram orchestrator (OpenAI-compatible API).")
		fmt.Println("The agent model runs inside each container (supports openai/, anthropic/, openrouter/).")
		fmt.Println()
	}

	// Mux model (must be OpenAI-compatible).
	if opts.model != "" {
		cfg.OpenAI.Model = opts.model
	} else if interactive {
		m := promptInput(fmt.Sprintf("Mux model [%s]: ", cfg.OpenAI.Model))
		if m != "" {
			cfg.OpenAI.Model = m
		}
	}

	// Mux base URL (for OpenRouter or other compatible APIs).
	if opts.baseURL != "" {
		cfg.OpenAI.BaseURL = opts.baseURL
	} else if interactive {
		current := cfg.OpenAI.BaseURL
		if current == "" {
			current = "https://api.openai.com/v1"
		}
		u := promptInput(fmt.Sprintf("Mux API base URL [%s]: ", current))
		if u != "" {
			cfg.OpenAI.BaseURL = u
		}
	}

	// Mux temperature.
	if opts.temperature != "" {
		if v, err := strconv.ParseFloat(opts.temperature, 64); err == nil {
			cfg.OpenAI.Temperature = v
		}
	} else if interactive {
		t := promptInput(fmt.Sprintf("Mux temperature [%.1f]: ", cfg.OpenAI.Temperature))
		if t != "" {
			if v, err := strconv.ParseFloat(t, 64); err == nil {
				cfg.OpenAI.Temperature = v
			}
		}
	}

	// Agent model.
	agentModel := opts.agentModel
	if agentModel == "" {
		agentModel = existingKeys["model"]
	}
	if interactive && opts.agentModel == "" {
		defaultModel := agentModel
		if defaultModel == "" {
			defaultModel = "openai/gpt-5-mini"
		}
		inp := promptInput(fmt.Sprintf("Agent model [%s]: ", defaultModel))
		if inp != "" {
			agentModel = inp
		}
	}
	if agentModel == "" {
		agentModel = "openai/gpt-5-mini"
	}

	// System prompt — not asked in wizard; auto-generated per agent name.
	// Override later with: xnc config set <agent> system_prompt "..."
	systemPrompt := opts.systemPrompt

	// ── Section 3: Voice / STT / TTS ──

	if interactive {
		fmt.Println()
		fmt.Println("─── Voice & Speech ───")
		fmt.Println("Voice features use the OpenAI Whisper (STT) and TTS APIs.")
		fmt.Println("Requires an OpenAI key even if the mux uses a different provider.")
		fmt.Println()
	}

	// Whisper model.
	if opts.whisperModel != "" {
		cfg.OpenAI.WhisperModel = opts.whisperModel
	} else if interactive {
		m := promptInput(fmt.Sprintf("Whisper model [%s]: ", cfg.OpenAI.WhisperModel))
		if m != "" {
			cfg.OpenAI.WhisperModel = m
		}
	}

	// Voice enabled (STT).
	if opts.voiceEnabled != nil {
		cfg.Voice.Enabled = *opts.voiceEnabled
	} else if interactive {
		yn := promptInput(fmt.Sprintf("Enable voice messages (STT via Whisper)? [%s]: ", boolYN(cfg.Voice.Enabled)))
		if yn != "" {
			cfg.Voice.Enabled = isYes(yn)
		}
	}

	// TTS enabled.
	if opts.ttsEnabled != nil {
		cfg.Voice.TTSEnabled = *opts.ttsEnabled
	} else if interactive {
		yn := promptInput(fmt.Sprintf("Enable text-to-speech (TTS)? [%s]: ", boolYN(cfg.Voice.TTSEnabled)))
		if yn != "" {
			cfg.Voice.TTSEnabled = isYes(yn)
		}
	}

	// TTS voice.
	if opts.ttsVoice != "" {
		cfg.Voice.TTSVoice = opts.ttsVoice
		cfg.OpenAI.TTSVoice = opts.ttsVoice
	} else if interactive && cfg.Voice.TTSEnabled {
		v := promptInput(fmt.Sprintf("TTS voice [%s] (alloy/echo/fable/onyx/nova/shimmer): ", cfg.Voice.TTSVoice))
		if v != "" {
			cfg.Voice.TTSVoice = v
			cfg.OpenAI.TTSVoice = v
		}
	}

	// ── Section 4: Agents ──

	if interactive {
		fmt.Println()
		fmt.Println("─── Agents ───")
		fmt.Println("Agents are AI workers running in Docker containers.")
		if len(existingAgents) > 0 {
			names := make([]string, len(existingAgents))
			for i, a := range existingAgents {
				names[i] = a.Name
			}
			fmt.Printf("Existing agents: %s\n", strings.Join(names, ", "))
		}
		fmt.Println()
	}

	agentCount := opts.agentCount
	if agentCount == 0 && interactive {
		if len(existingAgents) > 0 {
			countStr := promptInput("Additional agents to create [0]: ")
			if countStr != "" {
				n, err := strconv.Atoi(countStr)
				if err != nil || n < 0 {
					log.Fatalf("invalid agent count: %s", countStr)
				}
				agentCount = n
			}
		} else {
			countStr := promptInput("Number of agents to create [1]: ")
			if countStr != "" {
				n, err := strconv.Atoi(countStr)
				if err != nil || n < 0 {
					log.Fatalf("invalid agent count: %s", countStr)
				}
				agentCount = n
			} else {
				agentCount = 1
			}
		}
	}
	if agentCount == 0 && len(existingAgents) == 0 {
		agentCount = 1
	}

	// Collect agent names.
	var agentNames []string
	pendingNames := make(map[string]bool)
	nameIdx := 0
	for i := 0; i < agentCount; i++ {
		if nameIdx < len(opts.agentNames) {
			agentNames = append(agentNames, opts.agentNames[nameIdx])
			pendingNames[opts.agentNames[nameIdx]] = true
			nameIdx++
		} else {
			suggested := suggestUnusedName(home, pendingNames)
			if interactive {
				name := promptInput(fmt.Sprintf("Agent %d name [%s]: ", i+1, suggested))
				if name == "" {
					name = suggested
				}
				agentNames = append(agentNames, name)
				pendingNames[name] = true
			} else {
				agentNames = append(agentNames, suggested)
				pendingNames[suggested] = true
			}
		}
	}

	// ── Section 5: Telegram Mux (optional) ──

	setupMux := opts.setupMux
	muxAlreadyConfigured := cfg.Telegram.BotToken != ""
	if interactive && !setupMux {
		fmt.Println()
		fmt.Println("─── Telegram Mux (optional) ───")
		fmt.Println("The mux is a Telegram bot that orchestrates your agents.")
		fmt.Println("You can skip this and use xnc in CLI-only mode.")
		fmt.Println()
		if muxAlreadyConfigured {
			setupMux = !isNo(promptInput("Reconfigure Telegram mux? [Y/n]: "))
		} else {
			setupMux = isYes(promptInput("Set up Telegram mux? [y/N]: "))
		}
	}

	if setupMux {
		telegramToken := resolveValue(opts.telegramToken, os.Getenv("TELEGRAM_BOT_TOKEN"), cfg.Telegram.BotToken)
		if telegramToken == "" && interactive {
			telegramToken = promptInput("Telegram bot token: ")
		} else if interactive && telegramToken != "" && opts.telegramToken == "" {
			inp := promptInput(fmt.Sprintf("Telegram bot token [%s, Enter to keep]: ", redactKey(telegramToken)))
			if inp != "" {
				telegramToken = inp
			}
		}
		if telegramToken != "" {
			existingToken := cfg.Telegram.BotToken
			if telegramToken != existingToken {
				// Token changed — validate it.
				if err := validateTelegramToken(telegramToken); err != nil {
					if interactive {
						fmt.Fprintf(os.Stderr, "warning: %v\n", err)
						telegramToken = promptInput("Telegram bot token (format: 123456:ABC...): ")
						if err := validateTelegramToken(telegramToken); err != nil {
							log.Fatalf("invalid telegram token: %v", err)
						}
					} else {
						log.Fatalf("invalid telegram token: %v", err)
					}
				}
				// Verify token with Telegram API.
				if err := agent.TestTelegramToken(telegramToken); err != nil {
					if interactive {
						fmt.Fprintf(os.Stderr, "warning: %v\n", err)
						telegramToken = promptInput("Telegram bot token (try again): ")
						if err := validateTelegramToken(telegramToken); err != nil {
							log.Fatalf("invalid telegram token: %v", err)
						}
						if err := agent.TestTelegramToken(telegramToken); err != nil {
							log.Fatalf("telegram token verification failed: %v", err)
						}
					} else {
						log.Fatalf("telegram token verification failed: %v", err)
					}
				}
				fmt.Println("ok: telegram token verified")
			}
			cfg.Telegram.BotToken = telegramToken
		}

		telegramUser := opts.telegramUser
		if telegramUser == "" && len(cfg.Telegram.AllowFrom) > 0 {
			telegramUser = cfg.Telegram.AllowFrom[0]
		}
		if telegramUser == "" && interactive {
			telegramUser = promptInput("Your Telegram user ID (for access control): ")
		}
		if telegramUser != "" {
			cfg.Telegram.AllowFrom = []string{telegramUser}
		}

		// Owner name.
		if opts.ownerName != "" {
			cfg.Persona.OwnerName = opts.ownerName
		} else if interactive {
			name := promptInput(fmt.Sprintf("Your name (mux persona) [%s]: ", cfg.Persona.OwnerName))
			if name != "" {
				cfg.Persona.OwnerName = name
			}
		}

		// Budget.
		if interactive {
			fmt.Println()
			fmt.Println("─── Budget ───")
			daily := promptInput(fmt.Sprintf("Daily budget USD [%.2f]: ", cfg.Costs.DailyBudgetUSD))
			if daily != "" {
				if v, err := strconv.ParseFloat(daily, 64); err == nil {
					cfg.Costs.DailyBudgetUSD = v
				}
			}
			monthly := promptInput(fmt.Sprintf("Monthly budget USD [%.2f]: ", cfg.Costs.MonthlyBudgetUSD))
			if monthly != "" {
				if v, err := strconv.ParseFloat(monthly, 64); err == nil {
					cfg.Costs.MonthlyBudgetUSD = v
				}
			}
		}
	}

	// Save config.
	if err := cfg.Save(cfgPath); err != nil {
		log.Fatalf("save config: %v", err)
	}

	// ── Create agents ──

	// Start with existing agents so they get wired into mux config.
	var allAgentNames []string
	for _, a := range existingAgents {
		allAgentNames = append(allAgentNames, a.Name)
	}

	for _, name := range agentNames {
		if agent.Exists(home, name) {
			fmt.Printf(":: agent %s already exists, skipping\n", name)
			continue
		}

		setupOpts := agent.SetupOpts{
			OpenAIKey:     openaiKey,
			AnthropicKey:  anthropicKey,
			OpenRouterKey: openrouterKey,
			SystemPrompt:  systemPrompt,
		}
		if agentModel != "openai/gpt-5-mini" {
			setupOpts.Model = agentModel
		}

		if err := agent.Setup(home, name, setupOpts); err != nil {
			log.Fatalf("setup agent %s: %v", name, err)
		}

		dir := agent.Dir(home, name)
		meta, _ := agent.ReadMeta(dir)
		fmt.Printf("ok: agent %s %s created\n", meta["EMOJI"], name)
		allAgentNames = append(allAgentNames, name)
	}
	allAgentNames = uniqueStrings(allAgentNames)

	// Update existing agents with any changed keys.
	for _, a := range existingAgents {
		dir := agent.Dir(home, a.Name)
		for _, kv := range []struct{ configKey, value, existing string }{
			{"openai_key", openaiKey, existingKeys["openai"]},
			{"anthropic_key", anthropicKey, existingKeys["anthropic"]},
			{"openrouter_key", openrouterKey, existingKeys["openrouter"]},
		} {
			if kv.value != "" && kv.value != kv.existing {
				agent.ConfigSet(dir, kv.configKey, kv.value)
			}
		}
	}

	// Wire agents into mux config if mux is being set up.
	if setupMux && len(allAgentNames) > 0 {
		cfg.Agents.AutoStart = uniqueStrings(append(cfg.Agents.AutoStart, allAgentNames...))
		cfg.Agents.MuxManaged = uniqueStrings(append(cfg.Agents.MuxManaged, allAgentNames...))
		if cfg.Agents.Default == "" {
			cfg.Agents.Default = allAgentNames[0]
		}

		if cfg.Agents.Identities == nil {
			cfg.Agents.Identities = map[string]config.AgentIdentity{}
		}
		for _, name := range allAgentNames {
			dir := agent.Dir(home, name)
			meta, _ := agent.ReadMeta(dir)
			if meta["EMOJI"] != "" {
				cfg.Agents.Identities[name] = config.AgentIdentity{
					Emoji: meta["EMOJI"],
				}
			}
		}

		if err := cfg.Save(cfgPath); err != nil {
			log.Fatalf("update config: %v", err)
		}
	}

	// ── Summary ──

	fmt.Println()
	fmt.Println("─── Summary ───")
	fmt.Printf("  Home:         %s\n", home)
	if len(allAgentNames) > 0 {
		fmt.Printf("  Agents:       %d (%s)\n", len(allAgentNames), strings.Join(allAgentNames, ", "))
	} else {
		fmt.Println("  Agents:       none")
	}
	fmt.Printf("  Agent model:  %s\n", agentModel)

	// Provider keys.
	if cfg.OpenAI.APIKey != "" {
		fmt.Printf("  OpenAI key:   %s\n", redactKey(cfg.OpenAI.APIKey))
	} else {
		fmt.Println("  OpenAI key:   not set")
	}
	if anthropicKey != "" {
		fmt.Printf("  Anthropic:    %s\n", redactKey(anthropicKey))
	}
	if openrouterKey != "" {
		fmt.Printf("  OpenRouter:   %s\n", redactKey(openrouterKey))
	}

	// Mux details.
	if setupMux || muxAlreadyConfigured {
		muxBase := cfg.OpenAI.BaseURL
		if muxBase == "" {
			muxBase = "api.openai.com"
		}
		fmt.Printf("  Mux model:    %s (via %s)\n", cfg.OpenAI.Model, muxBase)
		fmt.Printf("  Temperature:  %.1f\n", cfg.OpenAI.Temperature)
		if cfg.Telegram.BotToken != "" {
			fmt.Println("  Telegram:     configured")
		} else {
			fmt.Println("  Telegram:     token missing (mux won't start without it)")
		}
	} else {
		fmt.Println("  Mux:          not configured (CLI-only mode)")
	}

	// Voice.
	if cfg.Voice.Enabled || cfg.Voice.TTSEnabled {
		fmt.Printf("  Whisper:      %s", cfg.OpenAI.WhisperModel)
		if !cfg.Voice.Enabled {
			fmt.Print(" (STT disabled)")
		}
		fmt.Println()
	}
	if cfg.Voice.TTSEnabled {
		fmt.Printf("  TTS voice:    %s\n", cfg.Voice.TTSVoice)
	}

	if setupMux {
		fmt.Printf("  Budget:       $%.2f/day, $%.2f/month\n", cfg.Costs.DailyBudgetUSD, cfg.Costs.MonthlyBudgetUSD)
	}

	fmt.Println()
	fmt.Println("Next steps:")
	if len(allAgentNames) > 0 {
		fmt.Println("  xnc image build          Pull the nullclaw Docker image")
		fmt.Printf("  xnc persona %-13sTweak agent personality\n", allAgentNames[0])
		fmt.Printf("  xnc start %-15sStart an agent\n", allAgentNames[0])
		fmt.Printf("  xnc send %s              Send a message via CLI\n", allAgentNames[0])
	} else {
		fmt.Println("  xnc setup <name>         Create an agent")
	}
	if setupMux && cfg.Telegram.BotToken != "" {
		fmt.Println("  xnc persona mux          Tweak mux personality")
		fmt.Println("  xnc mux                  Start the Telegram mux")
	}
	fmt.Println()
	fmt.Println("ok: init complete")
}

// initOpts holds parsed init flags.
type initOpts struct {
	home          string
	openaiKey     string
	anthropicKey  string
	openrouterKey string
	telegramToken string
	telegramUser  string
	model         string // mux model
	baseURL       string // mux API base URL
	temperature   string // mux temperature
	agentModel    string // agent model (e.g. openai/gpt-5-mini)
	systemPrompt  string
	whisperModel  string
	ttsVoice      string
	voiceEnabled  *bool
	ttsEnabled    *bool
	agentCount    int
	agentNames    []string
	ownerName     string
	setupMux      bool
	nonInter      bool
}

func (o initOpts) interactive() bool {
	if o.nonInter {
		return false
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func parseInitFlags(args []string) initOpts {
	opts := initOpts{
		home: agent.DefaultHome(),
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--home":
			if i+1 < len(args) {
				opts.home = args[i+1]
				i++
			}
		case "--openai-key":
			if i+1 < len(args) {
				opts.openaiKey = args[i+1]
				i++
			}
		case "--anthropic-key":
			if i+1 < len(args) {
				opts.anthropicKey = args[i+1]
				i++
			}
		case "--openrouter-key":
			if i+1 < len(args) {
				opts.openrouterKey = args[i+1]
				i++
			}
		case "--telegram-token":
			if i+1 < len(args) {
				opts.telegramToken = args[i+1]
				opts.setupMux = true
				i++
			}
		case "--telegram-user":
			if i+1 < len(args) {
				opts.telegramUser = args[i+1]
				opts.setupMux = true
				i++
			}
		case "--model":
			if i+1 < len(args) {
				opts.model = args[i+1]
				i++
			}
		case "--base-url":
			if i+1 < len(args) {
				opts.baseURL = args[i+1]
				i++
			}
		case "--temperature":
			if i+1 < len(args) {
				opts.temperature = args[i+1]
				i++
			}
		case "--agent-model":
			if i+1 < len(args) {
				opts.agentModel = args[i+1]
				i++
			}
		case "--system-prompt":
			if i+1 < len(args) {
				opts.systemPrompt = args[i+1]
				i++
			}
		case "--whisper-model":
			if i+1 < len(args) {
				opts.whisperModel = args[i+1]
				i++
			}
		case "--tts-voice":
			if i+1 < len(args) {
				opts.ttsVoice = args[i+1]
				i++
			}
		case "--voice":
			b := true
			opts.voiceEnabled = &b
		case "--no-voice":
			b := false
			opts.voiceEnabled = &b
		case "--tts":
			b := true
			opts.ttsEnabled = &b
		case "--no-tts":
			b := false
			opts.ttsEnabled = &b
		case "-n", "--agents":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 0 {
					log.Fatalf("invalid agent count: %s", args[i+1])
				}
				opts.agentCount = n
				i++
			}
		case "--name":
			if i+1 < len(args) {
				opts.agentNames = append(opts.agentNames, args[i+1])
				i++
			}
		case "--owner":
			if i+1 < len(args) {
				opts.ownerName = args[i+1]
				opts.setupMux = true
				i++
			}
		case "--mux":
			opts.setupMux = true
		case "--yes", "-y":
			opts.nonInter = true
		default:
			if !strings.HasPrefix(args[i], "-") {
				opts.agentNames = append(opts.agentNames, args[i])
			}
		}
	}

	if len(opts.agentNames) > 0 && opts.agentCount == 0 {
		opts.agentCount = len(opts.agentNames)
	}

	// If any credential flags given, mark non-interactive.
	if opts.openaiKey != "" || opts.telegramToken != "" {
		opts.nonInter = true
	}

	return opts
}

// suggestUnusedName returns a name not already used by an existing agent
// or pending in the current init batch.
func suggestUnusedName(home string, pending map[string]bool) string {
	for _, name := range agent.NamePool {
		if !agent.Exists(home, name) && !pending[name] {
			return name
		}
	}
	return fmt.Sprintf("agent%d", len(pending)+1)
}

// validateTelegramToken checks that a string looks like a Telegram bot token.
// Format: <bot_id>:<alphanumeric_string> e.g. 123456789:ABCdefGHI-jklMNOpqrs
func validateTelegramToken(token string) error {
	// Catch common mistake: pasting an API key instead.
	if strings.HasPrefix(token, "sk-") {
		return fmt.Errorf("this looks like an API key, not a Telegram bot token")
	}
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("must contain a colon (format: 123456:ABCdef...)")
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return fmt.Errorf("bot ID before colon must be numeric (got %q)", parts[0])
	}
	if len(parts[1]) < 20 {
		return fmt.Errorf("token part after colon is too short")
	}
	return nil
}

// readExistingAgentKeysFrom scans existing agents and returns the first non-empty
// key found for each provider. This lets init re-runs preserve configured keys.
//
// In the single-tenant model, all agents typically share the same provider keys.
// If agents have conflicting keys for the same provider, the first agent's key
// wins and a warning is printed.
func readExistingAgentKeysFrom(home string, agents []agent.Info) map[string]string {
	keys := map[string]string{}
	if len(agents) == 0 {
		return keys
	}
	for _, pair := range []struct {
		configKey string
		mapKey    string
	}{
		{"openai_key", "openai"},
		{"anthropic_key", "anthropic"},
		{"openrouter_key", "openrouter"},
		{"model", "model"},
	} {
		for _, a := range agents {
			dir := agent.Dir(home, a.Name)
			val, err := agent.ConfigGet(dir, pair.configKey)
			if err != nil {
				continue
			}
			s, ok := val.(string)
			if !ok || s == "" {
				continue
			}
			if existing, found := keys[pair.mapKey]; found {
				if s != existing {
					fmt.Fprintf(os.Stderr, "warning: agent %s has a different %s than others, using first found\n", a.Name, pair.mapKey)
				}
				continue
			}
			keys[pair.mapKey] = s
		}
	}
	return keys
}

// resolveValue returns the first non-empty value from the candidates.
func resolveValue(candidates ...string) string {
	for _, v := range candidates {
		if v != "" {
			return v
		}
	}
	return ""
}

// redactKey shows a redacted version of a key for display.
func redactKey(key string) string {
	if len(key) > 12 {
		return key[:4] + "..." + key[len(key)-4:]
	}
	return "****"
}

// boolYN returns "Y" or "N" for display.
func boolYN(b bool) string {
	if b {
		return "Y"
	}
	return "N"
}

// isYes returns true if the input starts with y/Y.
func isYes(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "y")
}

// isNo returns true if the input starts with n/N.
func isNo(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "n")
}

// promptInput prints a prompt and reads a line from stdin.
func promptInput(prompt string) string {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

func printInitUsage() {
	fmt.Print(`xnc init — bootstrap xnc from scratch

With no flags, runs an interactive wizard. With flags, runs non-interactively.

PROVIDER KEYS:
  --openai-key KEY        OpenAI API key (also reads OPENAI_API_KEY env)
  --anthropic-key KEY     Anthropic API key (also reads ANTHROPIC_API_KEY env)
  --openrouter-key KEY    OpenRouter API key (also reads OPENROUTER_API_KEY env)

MODELS:
  --model MODEL           Mux LLM model (default: gpt-5-mini)
  --base-url URL          Mux API base URL (default: https://api.openai.com/v1)
  --temperature FLOAT     Mux temperature (default: 0.7)
  --agent-model MODEL     Agent LLM model (default: openai/gpt-5-mini)
  --system-prompt TEXT     Default agent system prompt

VOICE:
  --whisper-model MODEL   Whisper STT model (default: whisper-1)
  --voice / --no-voice    Enable/disable voice messages (STT)
  --tts / --no-tts        Enable/disable text-to-speech
  --tts-voice VOICE       TTS voice (alloy/echo/fable/onyx/nova/shimmer)

AGENTS:
  -n, --agents N          Number of agents to create (default: 1)
  --name NAME             Agent name (repeatable)

TELEGRAM MUX (optional):
  --mux                   Enable mux setup
  --telegram-token TOKEN  Telegram bot token (also reads TELEGRAM_BOT_TOKEN env)
  --telegram-user ID      Allowed Telegram user ID
  --owner NAME            Owner name for mux persona

OTHER:
  --home PATH             Override XNC_HOME (default: ~/.xnc)
  -y, --yes               Non-interactive mode with defaults
  -h, --help              Show this help

EXAMPLES:
  xnc init                                         Interactive wizard
  xnc init --openai-key sk-... -n 3                CLI-only, 3 agents
  xnc init --openai-key sk-... --mux \
           --telegram-token 123:ABC -n 2            Full setup with mux
  xnc init --openai-key sk-... \
           --agent-model anthropic/claude-sonnet-4   Use Anthropic for agents
`)
}

// uniqueStrings deduplicates a string slice preserving order.
func uniqueStrings(s []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}
