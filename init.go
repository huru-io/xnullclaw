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
	muxHome := filepath.Join(home, ".mux")

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

	// ── Section 1: LLM Provider keys ──

	if interactive {
		fmt.Println()
		fmt.Println("─── LLM Provider Keys ───")
		fmt.Println("API keys for LLM providers. At least one is required.")
		fmt.Println("Keys are shared between the mux and agents.")
		fmt.Println()
	}

	openaiKey := resolveValue(opts.openaiKey, os.Getenv("OPENAI_API_KEY"), cfg.OpenAI.APIKey)
	if openaiKey == "" && interactive {
		openaiKey = promptInput("OpenAI API key (or OPENAI_API_KEY env): ")
	}
	if openaiKey != "" {
		cfg.OpenAI.APIKey = openaiKey
	}

	anthropicKey := resolveValue(opts.anthropicKey, os.Getenv("ANTHROPIC_API_KEY"), "")
	if anthropicKey == "" && interactive {
		anthropicKey = promptInput("Anthropic API key (optional, Enter to skip): ")
	}

	openrouterKey := resolveValue(opts.openrouterKey, os.Getenv("OPENROUTER_API_KEY"), "")
	if openrouterKey == "" && interactive {
		openrouterKey = promptInput("OpenRouter API key (optional, Enter to skip): ")
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
	if agentModel == "" && interactive {
		agentModel = promptInput("Agent model [openai/gpt-5-mini]: ")
	}
	if agentModel == "" {
		agentModel = "openai/gpt-5-mini"
	}

	// System prompt for agents.
	systemPrompt := opts.systemPrompt
	if systemPrompt == "" && interactive {
		systemPrompt = promptInput("Default agent system prompt (optional, Enter to skip): ")
	}

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
		fmt.Println()
	}

	agentCount := opts.agentCount
	if agentCount == 0 && interactive {
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
	if agentCount == 0 {
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
	if interactive && !setupMux {
		fmt.Println()
		fmt.Println("─── Telegram Mux (optional) ───")
		fmt.Println("The mux is a Telegram bot that orchestrates your agents.")
		fmt.Println("You can skip this and use xnc in CLI-only mode.")
		fmt.Println()
		setupMux = isYes(promptInput("Set up Telegram mux? [y/N]: "))
	}

	if setupMux {
		telegramToken := resolveValue(opts.telegramToken, os.Getenv("TELEGRAM_BOT_TOKEN"), cfg.Telegram.BotToken)
		if telegramToken == "" && interactive {
			telegramToken = promptInput("Telegram bot token: ")
		}
		if telegramToken != "" {
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

	var createdAgents []string
	for _, name := range agentNames {
		if agent.Exists(home, name) {
			fmt.Printf(":: agent %s already exists, skipping\n", name)
			createdAgents = append(createdAgents, name)
			continue
		}

		if err := agent.Setup(home, name); err != nil {
			log.Fatalf("setup agent %s: %v", name, err)
		}

		dir := agent.Dir(home, name)

		// Inject credentials into agent config.
		if openaiKey != "" {
			agent.ConfigSet(dir, "openai_key", openaiKey)
		}
		if anthropicKey != "" {
			agent.ConfigSet(dir, "anthropic_key", anthropicKey)
		}
		if openrouterKey != "" {
			agent.ConfigSet(dir, "openrouter_key", openrouterKey)
		}
		if agentModel != "openai/gpt-5-mini" {
			agent.ConfigSet(dir, "model", agentModel)
		}
		if systemPrompt != "" {
			agent.ConfigSet(dir, "system_prompt", systemPrompt)
		}

		meta, _ := agent.ReadMeta(dir)
		fmt.Printf("ok: agent %s %s created\n", meta["EMOJI"], name)
		createdAgents = append(createdAgents, name)
	}

	// Wire agents into mux config if mux is being set up.
	if setupMux && len(createdAgents) > 0 {
		cfg.Agents.AutoStart = uniqueStrings(append(cfg.Agents.AutoStart, createdAgents...))
		cfg.Agents.MuxManaged = uniqueStrings(append(cfg.Agents.MuxManaged, createdAgents...))
		if cfg.Agents.Default == "" {
			cfg.Agents.Default = createdAgents[0]
		}

		for _, name := range createdAgents {
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
	fmt.Printf("  Agents:       %d (%s)\n", len(createdAgents), strings.Join(createdAgents, ", "))
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
	if setupMux {
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
	fmt.Println("  xnc image build          Pull the nullclaw Docker image")
	fmt.Printf("  xnc start %-15sStart an agent\n", createdAgents[0])
	fmt.Printf("  xnc send %s              Send a message via CLI\n", createdAgents[0])
	if setupMux && cfg.Telegram.BotToken != "" {
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

// resolveValue returns the first non-empty value from the candidates.
func resolveValue(candidates ...string) string {
	for _, v := range candidates {
		if v != "" {
			return v
		}
	}
	return ""
}

// redactKey shows first 4 and last 4 chars of a key.
func redactKey(key string) string {
	if len(key) >= 8 {
		return key[:4] + "..." + key[len(key)-4:]
	}
	return "***"
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
