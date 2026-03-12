package agent

import (
	"fmt"
	"os"
)

// collectableKeys defines which config keys CollectKeys scans for.
// Each entry maps a config key name to the output map key.
var collectableKeys = []struct {
	configKey string
	mapKey    string
}{
	{"openai_key", "openai"},
	{"anthropic_key", "anthropic"},
	{"openrouter_key", "openrouter"},
	{"brave_key", "brave"},
	{"model", "model"},
}

// CollectKeys scans existing agents and returns the first non-empty value
// found for each provider key. In the single-tenant model, all agents
// typically share the same keys. Each agent's config is loaded once to
// avoid redundant file I/O.
func CollectKeys(home string) map[string]string {
	keys := map[string]string{}
	agents, _ := ListAll(home)
	if len(agents) == 0 {
		return keys
	}

	for _, a := range agents {
		dir := Dir(home, a.Name)
		doc, err := ConfigGetAll(dir)
		if err != nil {
			continue
		}
		for _, pair := range collectableKeys {
			if _, found := keys[pair.mapKey]; found {
				continue
			}
			ck, ok := LookupConfigKey(pair.configKey)
			if !ok {
				continue
			}
			val := GetPath(doc, ck.Path)
			if s, ok := val.(string); ok && s != "" {
				keys[pair.mapKey] = s
			}
		}
	}
	return keys
}

// CollectKeysWithWarnings is like CollectKeys but prints a warning to stderr
// when agents have conflicting values for the same key. Used by init wizard.
func CollectKeysWithWarnings(home string, agents []Info) map[string]string {
	keys := map[string]string{}
	if len(agents) == 0 {
		return keys
	}

	for _, a := range agents {
		dir := Dir(home, a.Name)
		doc, err := ConfigGetAll(dir)
		if err != nil {
			continue
		}
		for _, pair := range collectableKeys {
			ck, ok := LookupConfigKey(pair.configKey)
			if !ok {
				continue
			}
			val := GetPath(doc, ck.Path)
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
