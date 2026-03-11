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

// runInit bootstraps xnc from scratch: directories, mux config, credentials, agents.
//
// Modes:
//   - Non-interactive: xnc init --openai-key sk-... --telegram-token 123:ABC -n 3 --model gpt-4o
//   - Interactive:     xnc init (prompts for missing values)
func runInit(args []string) {
	opts := parseInitFlags(args)

	home := opts.home
	muxHome := filepath.Join(home, ".mux")

	// Validate home: must be an existing xnc home or empty/non-existent.
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

	// Generate instance ID.
	agent.InstanceID(home)
	fmt.Printf(":: home directory: %s\n", home)

	// 2. Mux config — load existing or create default.
	cfgPath := filepath.Join(muxHome, "config.json")
	cfg := config.DefaultConfig()
	if existing, err := config.Load(cfgPath); err == nil {
		cfg = existing
		fmt.Println(":: using existing mux config")
	}

	// 3. Collect credentials (flags take priority, then interactive).
	interactive := opts.interactive()

	// OpenAI key.
	openaiKey := opts.openaiKey
	if openaiKey == "" {
		openaiKey = os.Getenv("OPENAI_API_KEY")
	}
	if openaiKey == "" && cfg.OpenAI.APIKey != "" {
		openaiKey = cfg.OpenAI.APIKey
	}
	if openaiKey == "" && interactive {
		openaiKey = promptInput("OpenAI API key (or OPENAI_API_KEY env): ")
	}
	if openaiKey != "" {
		cfg.OpenAI.APIKey = openaiKey
	}

	// Model.
	if opts.model != "" {
		cfg.OpenAI.Model = opts.model
	} else if cfg.OpenAI.Model == "" {
		cfg.OpenAI.Model = "gpt-4o"
	}

	// Telegram token.
	telegramToken := opts.telegramToken
	if telegramToken == "" {
		telegramToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	}
	if telegramToken == "" && cfg.Telegram.BotToken != "" {
		telegramToken = cfg.Telegram.BotToken
	}
	if telegramToken == "" && interactive {
		telegramToken = promptInput("Telegram bot token (optional, press Enter to skip): ")
	}
	if telegramToken != "" {
		cfg.Telegram.BotToken = telegramToken
	}

	// Telegram allowed users.
	if opts.telegramUser != "" {
		cfg.Telegram.AllowFrom = []string{opts.telegramUser}
	}

	// Owner name.
	if opts.ownerName != "" {
		cfg.Persona.OwnerName = opts.ownerName
	} else if cfg.Persona.OwnerName == "Controller" && interactive {
		name := promptInput("Your name (for the mux persona, press Enter for 'Controller'): ")
		if name != "" {
			cfg.Persona.OwnerName = name
		}
	}

	// Save mux config.
	if err := cfg.Save(cfgPath); err != nil {
		log.Fatalf("save config: %v", err)
	}
	fmt.Printf(":: mux config saved: %s\n", cfgPath)

	// 4. Create agents.
	agentCount := opts.agentCount
	if agentCount == 0 && interactive {
		countStr := promptInput("Number of agents to create (default 1): ")
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

	// Create agents — use explicit names first, then auto-suggest remaining.
	var createdAgents []string
	nameIdx := 0
	for i := 0; i < agentCount; i++ {
		var name string
		if nameIdx < len(opts.agentNames) {
			name = opts.agentNames[nameIdx]
			nameIdx++
		} else {
			name = agent.SuggestName(home)
		}

		if agent.Exists(home, name) {
			fmt.Printf(":: agent %s already exists, skipping\n", name)
			createdAgents = append(createdAgents, name)
			continue
		}

		if err := agent.Setup(home, name); err != nil {
			log.Fatalf("setup agent %s: %v", name, err)
		}

		// Inject OpenAI key into agent config.
		if openaiKey != "" {
			dir := agent.Dir(home, name)
			agent.ConfigSet(dir, "openai_key", openaiKey)
		}

		dir := agent.Dir(home, name)
		meta, _ := agent.ReadMeta(dir)
		fmt.Printf("ok: agent %s %s created\n", meta["EMOJI"], name)
		createdAgents = append(createdAgents, name)
	}

	// Wire created agents into mux config (auto_start + mux_managed + default).
	if len(createdAgents) > 0 {
		cfg.Agents.AutoStart = uniqueStrings(append(cfg.Agents.AutoStart, createdAgents...))
		cfg.Agents.MuxManaged = uniqueStrings(append(cfg.Agents.MuxManaged, createdAgents...))
		if cfg.Agents.Default == "" {
			cfg.Agents.Default = createdAgents[0]
		}

		// Set identities from agent meta.
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

	// 5. Docker image check.
	fmt.Println()
	fmt.Printf(":: model: %s\n", cfg.OpenAI.Model)
	if k := cfg.OpenAI.APIKey; len(k) >= 8 {
		fmt.Printf(":: openai key: %s...%s\n", k[:4], k[len(k)-4:])
	} else if k != "" {
		fmt.Println(":: openai key: set")
	} else {
		fmt.Println(":: openai key: not set (set later with OPENAI_API_KEY or edit config)")
	}
	if cfg.Telegram.BotToken != "" {
		fmt.Println(":: telegram: configured")
	} else {
		fmt.Println(":: telegram: not configured (optional — set later in config)")
	}
	fmt.Println()
	fmt.Printf(":: %d agent(s) ready\n", len(createdAgents))
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  xnc image build          Build the nullclaw Docker image")
	fmt.Printf("  xnc start %-15sStart an agent\n", createdAgents[0])
	if cfg.Telegram.BotToken != "" {
		fmt.Println("  xnc mux                  Start the Telegram mux")
	}
	fmt.Println()
	fmt.Println("ok: init complete")
}

// initOpts holds parsed init flags.
type initOpts struct {
	home          string
	openaiKey     string
	telegramToken string
	telegramUser  string
	model         string
	agentCount    int
	agentNames    []string
	ownerName     string
	nonInter      bool // --yes or explicit credentials = skip prompts
}

func (o initOpts) interactive() bool {
	if o.nonInter {
		return false
	}
	// Interactive if stdin is a terminal.
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
				opts.nonInter = true
				i++
			}
		case "--telegram-token":
			if i+1 < len(args) {
				opts.telegramToken = args[i+1]
				opts.nonInter = true
				i++
			}
		case "--telegram-user":
			if i+1 < len(args) {
				opts.telegramUser = args[i+1]
				i++
			}
		case "--model":
			if i+1 < len(args) {
				opts.model = args[i+1]
				i++
			}
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
				i++
			}
		case "--yes", "-y":
			opts.nonInter = true
		default:
			// Treat bare args as agent names.
			if !strings.HasPrefix(args[i], "-") {
				opts.agentNames = append(opts.agentNames, args[i])
			}
		}
	}

	// If explicit names given, set count to match.
	if len(opts.agentNames) > 0 && opts.agentCount == 0 {
		opts.agentCount = len(opts.agentNames)
	}

	return opts
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
