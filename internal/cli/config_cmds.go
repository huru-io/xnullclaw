package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jotavich/xnullclaw/internal/agent"
)

func cmdConfig(g Globals, args []string) {
	if len(args) == 0 {
		die("usage: xnc config <get|set> <agent> [key] [value]")
	}

	subcmd := args[0]
	args = args[1:]

	switch subcmd {
	case "get":
		cmdConfigGet(g, args)
	case "set":
		cmdConfigSet(g, args)
	default:
		die("unknown config subcommand: %s", subcmd)
	}
}

func cmdConfigGet(g Globals, args []string) {
	if len(args) == 0 {
		die("usage: xnc config get <agent> [key]")
	}

	name := args[0]
	if !agent.Exists(g.Home, name) {
		die("agent %q does not exist", name)
	}

	dir := agent.Dir(g.Home, name)

	if len(args) < 2 {
		// Show all config.
		all, err := agent.ConfigGetAll(dir)
		if err != nil {
			die("%v", err)
		}
		data, _ := json.MarshalIndent(all, "", "  ")
		fmt.Println(string(data))
		return
	}

	key := args[1]
	val, err := agent.ConfigGet(dir, key)
	if err != nil {
		die("%v", err)
	}

	// Check if should be redacted.
	if ck, ok := agent.LookupConfigKey(key); ok && ck.Redacted {
		s, ok := val.(string)
		if ok && len(s) > 8 {
			val = s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
		}
	}

	if g.JSON {
		data, _ := json.Marshal(val)
		fmt.Println(string(data))
	} else {
		fmt.Println(val)
	}
}

func cmdConfigSet(g Globals, args []string) {
	if len(args) < 3 {
		die("usage: xnc config set <agent> <key> <value>")
	}

	name := args[0]
	key := args[1]
	value := args[2]

	if !agent.Exists(g.Home, name) {
		die("agent %q does not exist", name)
	}

	dir := agent.Dir(g.Home, name)
	if err := agent.ConfigSet(dir, key, value); err != nil {
		die("%v", err)
	}

	ok("set %s = %s for %s", key, value, name)
}
