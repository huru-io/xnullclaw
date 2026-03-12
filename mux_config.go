package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jotavich/xnullclaw/internal/config"
)

// muxConfigKey describes a settable mux config field.
type muxConfigKey struct {
	Path     string // dotted JSON path (e.g. "telegram.group_id")
	Type     string // string, int, int64, float, string_array
	Desc     string // human-readable description
	Redacted bool   // mask value in output
}

var muxConfigKeys = []muxConfigKey{
	{"telegram.bot_token", "string", "Telegram bot token", true},
	{"telegram.group_id", "int64", "Telegram group chat ID (0 = private mode)", false},
	{"telegram.topic_id", "int", "Forum topic ID (-1 = discover, 0 = all, >0 = specific)", false},
	{"telegram.allow_from", "string_array", "Allowed Telegram user IDs (comma-separated)", false},
	{"openai.api_key", "string", "OpenAI API key", true},
	{"openai.model", "string", "LLM model name", false},
	{"openai.temperature", "float", "LLM temperature (0.0-2.0)", false},
	{"openai.base_url", "string", "OpenAI-compatible API base URL", false},
	{"costs.daily_budget_usd", "float", "Daily budget in USD", false},
	{"costs.monthly_budget_usd", "float", "Monthly budget in USD", false},
	{"logging.level", "string", "Log level (debug/info/warn/error)", false},
	{"persona.show_header", "bool", "Show mux identity header (🔀) on messages", false},
}

func lookupMuxKey(path string) (muxConfigKey, bool) {
	for _, k := range muxConfigKeys {
		if k.Path == path {
			return k, true
		}
	}
	return muxConfigKey{}, false
}

// muxConfig dispatches mux config subcommands.
func muxConfig(cfgPath string, args []string) {
	if len(args) == 0 {
		muxConfigDump(cfgPath)
		return
	}

	switch args[0] {
	case "get":
		if len(args) < 2 {
			log.Fatal("usage: xnc mux config get <key>")
		}
		muxConfigGet(cfgPath, args[1])
	case "set":
		if len(args) < 3 {
			log.Fatal("usage: xnc mux config set <key> <value>")
		}
		muxConfigSet(cfgPath, args[1], strings.Join(args[2:], " "))
	case "keys":
		muxConfigListKeys()
	default:
		// Treat as shorthand: xnc mux config <key> → get
		muxConfigGet(cfgPath, args[0])
	}
}

// muxConfigDump prints the full config with secrets redacted.
func muxConfigDump(cfgPath string) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Marshal to map for redaction.
	data, _ := json.Marshal(cfg)
	var m map[string]any
	json.Unmarshal(data, &m)

	// Redact secrets.
	for _, k := range muxConfigKeys {
		if !k.Redacted {
			continue
		}
		parts := strings.SplitN(k.Path, ".", 2)
		if len(parts) != 2 {
			continue
		}
		section, ok := m[parts[0]].(map[string]any)
		if !ok {
			continue
		}
		if val, ok := section[parts[1]].(string); ok && val != "" {
			section[parts[1]] = redactSecret(val)
		}
	}

	out, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(out))
}

// muxConfigGet prints a single config value.
func muxConfigGet(cfgPath string, key string) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Marshal to generic map for dotted-path lookup.
	data, _ := json.Marshal(cfg)
	var m map[string]any
	json.Unmarshal(data, &m)

	val := getPath(m, key)
	if val == nil {
		// Key might be absent due to omitempty — if it's a known key, return zero value.
		if mk, ok := lookupMuxKey(key); ok {
			switch mk.Type {
			case "string":
				val = ""
			case "int", "int64":
				val = float64(0)
			case "float":
				val = float64(0)
			case "bool":
				val = false
			case "string_array":
				val = []any{}
			}
		} else {
			log.Fatalf("unknown key: %s", key)
		}
	}

	// Redact if needed.
	mk, known := lookupMuxKey(key)
	if known && mk.Redacted {
		if s, ok := val.(string); ok && s != "" {
			val = redactSecret(s)
		}
	}

	// Print: scalars as plain text, complex as JSON.
	switch v := val.(type) {
	case string:
		fmt.Println(v)
	case float64:
		// Print integers without decimal if they are whole.
		if v == float64(int64(v)) {
			fmt.Println(int64(v))
		} else {
			fmt.Println(v)
		}
	case bool:
		fmt.Println(v)
	default:
		out, _ := json.Marshal(v)
		fmt.Println(string(out))
	}
}

// muxConfigSet sets a config value and saves.
func muxConfigSet(cfgPath string, key string, value string) {
	mk, known := lookupMuxKey(key)
	if !known {
		fmt.Fprintf(os.Stderr, "unknown key: %s\n", key)
		fmt.Fprintln(os.Stderr, "run 'xnc mux config keys' to see available keys")
		os.Exit(1)
	}

	// Load or create config.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		cfg = config.DefaultConfig()
	}

	switch mk.Path {
	case "telegram.bot_token":
		cfg.Telegram.BotToken = value
	case "telegram.group_id":
		v, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			log.Fatalf("invalid int64 for %s: %s", key, value)
		}
		cfg.Telegram.GroupID = v
	case "telegram.topic_id":
		v, err := strconv.Atoi(value)
		if err != nil {
			log.Fatalf("invalid int for %s: %s", key, value)
		}
		cfg.Telegram.TopicID = v
	case "telegram.allow_from":
		parts := strings.Split(value, ",")
		var cleaned []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				cleaned = append(cleaned, p)
			}
		}
		cfg.Telegram.AllowFrom = cleaned
	case "openai.api_key":
		cfg.OpenAI.APIKey = value
	case "openai.model":
		cfg.OpenAI.Model = value
	case "openai.temperature":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			log.Fatalf("invalid float for %s: %s", key, value)
		}
		cfg.OpenAI.Temperature = v
	case "openai.base_url":
		cfg.OpenAI.BaseURL = value
	case "costs.daily_budget_usd":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			log.Fatalf("invalid float for %s: %s", key, value)
		}
		cfg.Costs.DailyBudgetUSD = v
	case "costs.monthly_budget_usd":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			log.Fatalf("invalid float for %s: %s", key, value)
		}
		cfg.Costs.MonthlyBudgetUSD = v
	case "logging.level":
		value = strings.ToLower(value)
		switch value {
		case "debug", "info", "warn", "error":
			cfg.Logging.Level = value
		default:
			log.Fatalf("invalid log level %q (must be debug/info/warn/error)", value)
		}
	case "persona.show_header":
		switch strings.ToLower(value) {
		case "true", "1", "yes":
			cfg.Persona.ShowHeader = true
		case "false", "0", "no":
			cfg.Persona.ShowHeader = false
		default:
			log.Fatalf("invalid bool for %s: %s (use true/false)", key, value)
		}
	}

	if err := cfg.Save(cfgPath); err != nil {
		log.Fatalf("save config: %v", err)
	}

	// Show confirmation.
	display := value
	if mk.Redacted && len(value) > 8 {
		display = redactSecret(value)
	}
	fmt.Printf("ok: set %s = %s\n", key, display)

	// Hint about restart if mux is running.
	muxHome := filepath.Dir(cfgPath)
	pidFile := filepath.Join(muxHome, "mux.pid")
	if pid := readPID(pidFile); pid > 0 && processAlive(pid) {
		fmt.Println("note: restart mux for changes to take effect")
	}
}

// muxConfigListKeys prints all settable keys.
func muxConfigListKeys() {
	for _, k := range muxConfigKeys {
		fmt.Printf("  %-30s %s (%s)\n", k.Path, k.Desc, k.Type)
	}
}

// getPath navigates a map[string]any by dotted path.
func getPath(m map[string]any, path string) any {
	parts := strings.Split(path, ".")
	var current any = m
	for _, p := range parts {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = cm[p]
		if !ok {
			return nil
		}
	}
	return current
}

// redactSecret masks a string, showing first 4 and last 4 characters.
func redactSecret(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}
