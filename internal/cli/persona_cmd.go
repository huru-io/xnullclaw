package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
)

// numDimensions is the number of personality dimensions.
const numDimensions = 10

// dimension describes a personality slider with human-readable labels.
type dimension struct {
	key  string // config key suffix and CLI flag name (e.g. "warmth")
	name string // display name (must fit in 14 chars)
	low  string // description at 0.0
	mid  string // description at 0.5
	high string // description at 1.0
}

var dimensions = []dimension{
	{"warmth", "Warmth", "clinical, matter-of-fact", "friendly but professional", "warm, caring, personal"},
	{"humor", "Humor", "never jokes", "occasional humor", "playful, jokes freely"},
	{"verbosity", "Verbosity", "extremely terse", "balanced brevity/detail", "thorough and detailed"},
	{"proactiveness", "Proactiveness", "only when asked", "suggests when relevant", "anticipates needs"},
	{"formality", "Formality", "casual, slang ok", "professional but relaxed", "formal and proper"},
	{"empathy", "Empathy", "matter-of-fact", "acknowledges feelings", "emotionally attuned"},
	{"sarcasm", "Sarcasm", "never sarcastic", "light irony", "sharp wit freely"},
	{"autonomy", "Autonomy", "always asks first", "acts on clear intent", "takes initiative"},
	{"interpretation", "Interpretation", "takes literally", "fixes obvious typos", "actively clarifies"},
	{"creativity", "Creativity", "straightforward", "balanced conventional/novel", "creative, surprising"},
}

// assistantDefaults are the fallback values for unconfigured agents.
var assistantDefaults = dimsToArray(config.PresetMap["assistant"])

// muxDefaults are derived from config.PersonaDimensions.Defaults() at init time.
var muxDefaults [numDimensions]float64

func init() {
	if len(dimensions) != numDimensions {
		// Programmer error: someone added a dimension without updating the slice.
		// Panic is intentional — this is a compile-time invariant check that fires during init().
		panic("dimensions slice length does not match numDimensions constant")
	}
	d := config.PersonaDimensions{}.Defaults()
	muxDefaults = [numDimensions]float64{
		d.Warmth, d.Humor, d.Verbosity, d.Proactiveness, d.Formality,
		d.Empathy, d.Sarcasm, d.Autonomy, d.Interpretation, d.Creativity,
	}
}

// dimsToArray converts a PersonaDimensions struct to an array in dimensions order.
func dimsToArray(d config.PersonaDimensions) [numDimensions]float64 {
	return [numDimensions]float64{
		d.Warmth, d.Humor, d.Verbosity, d.Proactiveness, d.Formality,
		d.Empathy, d.Sarcasm, d.Autonomy, d.Interpretation, d.Creativity,
	}
}

func cmdPersona(g Globals, args []string) {
	preset, _ := flagValue(&args, "--preset")
	show := hasFlag(&args, "--show")
	reset := hasFlag(&args, "--reset")
	listPresets := hasFlag(&args, "--list-presets")

	// Extract per-dimension flags (--warmth 0.8, --humor 0.3, etc.)
	// Also accept short aliases --proactive and --interpret.
	dimAliases := map[string]string{
		"proactive": "proactiveness",
		"interpret": "interpretation",
	}
	dimOverrides := map[int]float64{}
	for i, d := range dimensions {
		valStr, found := flagValue(&args, "--"+d.key)
		if !found {
			// Check aliases.
			for alias, canonical := range dimAliases {
				if canonical == d.key {
					valStr, found = flagValue(&args, "--"+alias)
					break
				}
			}
		}
		if found {
			v, err := strconv.ParseFloat(valStr, 64)
			if err != nil || v < 0 || v > 1 {
				die("--%s must be a number between 0.0 and 1.0", d.key)
			}
			dimOverrides[i] = v
		}
	}
	trait, hasTrait := flagValue(&args, "--trait")

	if listPresets {
		printPresets()
		return
	}

	names := agentNames(args)

	if len(names) == 0 {
		die("usage: xnc persona <agent|mux> [--show] [--preset NAME] [--list-presets] [--reset] [--warmth N] ...")
	}

	name := names[0]
	isMux := strings.ToLower(name) == "mux"

	var dir string
	defaults := assistantDefaults
	if isMux {
		name = "mux"
		dir = filepath.Join(g.Home, "mux")
		defaults = muxDefaults
		// Verify mux config exists.
		if _, err := os.Stat(filepath.Join(dir, "config.json")); os.IsNotExist(err) {
			die("mux not initialized — run 'xnc init' first")
		}
	} else {
		if !agent.Exists(g.Home, name) {
			die("agent %q does not exist", name)
		}
		dir = agent.Dir(g.Home, name)
	}

	if show {
		showPersona(dir, name, defaults)
		return
	}

	if reset {
		resetTrait := inferTrait(defaults)
		writePersona(dir, name, resetTrait, defaults, isMux)
		ok("reset %s to default personality", name)
		restartHint(isMux, name)
		fmt.Println()
		showPersona(dir, name, defaults)
		return
	}

	if preset != "" {
		found := findPreset(preset)
		if found == nil {
			fmt.Fprintf(os.Stderr, "unknown preset %q\n\nAvailable presets:\n", preset)
			printPresets()
			os.Exit(1)
		}
		writePersona(dir, name, found.Trait, dimsToArray(found.Dims), isMux)
		// Apply any overrides on top of the preset.
		if len(dimOverrides) > 0 || hasTrait {
			applyOverrides(dir, name, defaults, isMux, dimOverrides, trait, hasTrait)
		} else {
			ok("applied %q preset to %s", found.Name, name)
			restartHint(isMux, name)
			fmt.Println()
			showPersona(dir, name, defaults)
		}
		return
	}

	// If any dimension flags given, apply them directly (no interactive).
	if len(dimOverrides) > 0 || hasTrait {
		applyOverrides(dir, name, defaults, isMux, dimOverrides, trait, hasTrait)
		return
	}

	// Interactive mode.
	interactivePersona(dir, name, defaults, isMux)
}

// applyOverrides modifies specific dimensions on an existing persona.
func applyOverrides(dir, name string, defaults [numDimensions]float64, isMux bool, overrides map[int]float64, trait string, hasTrait bool) {
	values := readDimensionValues(dir, defaults)
	for i, v := range overrides {
		values[i] = v
	}

	// Preserve existing trait unless --trait was explicitly given.
	currentTrait := configStr(dir, "persona_trait")
	if hasTrait {
		currentTrait = trait
	}
	if currentTrait == "" {
		currentTrait = inferTrait(values)
	}

	writePersona(dir, name, currentTrait, values, isMux)
	ok("updated %s personality", name)
	restartHint(isMux, name)
	fmt.Println()
	showPersona(dir, name, defaults)
}

// showPersona displays current personality dimensions.
func showPersona(dir, name string, defaults [numDimensions]float64) {
	configured := personaConfigured(dir)
	if !configured {
		fmt.Printf("%s — no persona configured (using default system prompt)\n", name)
		fmt.Println("Run 'xnc persona " + name + "' to set one.")
		fmt.Println()
		return
	}

	values := readDimensionValues(dir, defaults)
	trait := configStr(dir, "persona_trait")
	if trait == "" {
		trait = inferTrait(values)
	}

	fmt.Printf("%s — %s\n\n", name, trait)
	for i, d := range dimensions {
		bar := renderBar(values[i])
		label := pickLabel(values[i], d.low, d.mid, d.high)
		fmt.Printf("  %-14s %s %.1f  %s\n", d.name, bar, values[i], label)
	}
	fmt.Println()
}

// findPreset looks up a preset by name (case-insensitive).
func findPreset(presetName string) *config.Preset {
	lower := strings.ToLower(presetName)
	for i := range config.Presets {
		if config.Presets[i].Name == lower {
			return &config.Presets[i]
		}
	}
	return nil
}

// interactivePersona walks the user through personality configuration.
func interactivePersona(dir, name string, defaults [numDimensions]float64, isMux bool) {
	fmt.Printf("Personality editor for %s\n\n", name)

	// Show presets.
	fmt.Println("Presets:")
	printPresets()
	fmt.Println()

	// Show current.
	configured := personaConfigured(dir)
	currentTrait := configStr(dir, "persona_trait")
	if configured {
		if currentTrait == "" {
			currentTrait = inferTrait(readDimensionValues(dir, defaults))
		}
		fmt.Printf("Current: %s\n", currentTrait)
	}

	fmt.Println()
	fmt.Println("Enter a preset name, or 'custom' to adjust each dimension.")
	if configured {
		fmt.Print("Choice [keep current]: ")
	} else {
		fmt.Print("Choice [assistant]: ")
	}

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return
	}
	choice := strings.TrimSpace(scanner.Text())

	if choice == "" {
		if configured {
			info("keeping current personality")
			return
		}
		choice = "assistant"
	}

	if strings.ToLower(choice) != "custom" {
		found := findPreset(choice)
		if found == nil {
			fmt.Fprintf(os.Stderr, "unknown preset %q\n", choice)
			return
		}
		writePersona(dir, name, found.Trait, dimsToArray(found.Dims), isMux)
		ok("applied %q preset to %s", found.Name, name)
		restartHint(isMux, name)
		fmt.Println()
		showPersona(dir, name, defaults)
		return
	}

	// Custom: walk through each dimension.
	fmt.Println()
	fmt.Println("Adjust each dimension (0.0 to 1.0). Enter to keep current.")
	fmt.Println()

	values := readDimensionValues(dir, defaults)
	for i, d := range dimensions {
		label := pickLabel(values[i], d.low, d.mid, d.high)
		fmt.Printf("  %s (%.1f = %s)\n", d.name, values[i], label)
		fmt.Printf("    0.0=%-25s 1.0=%s\n", d.low, d.high)
		fmt.Printf("    [%.1f]: ", values[i])

		if !scanner.Scan() {
			break
		}
		inp := strings.TrimSpace(scanner.Text())
		if inp != "" {
			v, err := strconv.ParseFloat(inp, 64)
			if err != nil || v < 0 || v > 1 {
				fmt.Fprintf(os.Stderr, "    invalid value, keeping %.1f\n", values[i])
			} else {
				values[i] = v
			}
		}
		fmt.Println()
	}

	// Show inferred trait as the default.
	inferred := inferTrait(values)
	fmt.Printf("Personality trait [%s]: ", inferred)
	trait := ""
	if scanner.Scan() {
		trait = strings.TrimSpace(scanner.Text())
	}
	if trait == "" {
		trait = inferred
	}

	writePersona(dir, name, trait, values, isMux)

	fmt.Println()
	ok("personality updated for %s", name)
	restartHint(isMux, name)
	fmt.Println()
	showPersona(dir, name, defaults)
}

// printPresets shows all available presets with a compact dimension summary.
func printPresets() {
	for _, p := range config.Presets {
		fmt.Printf("  %-14s %s\n", p.Name, p.Trait)
	}
}

// personaConfigured returns true if the target has persona dimensions set.
// Checks for persona.trait (agents) or any explicitly written dimension (mux).
func personaConfigured(dir string) bool {
	if configStr(dir, "persona_trait") != "" {
		return true
	}
	// Mux has dimensions without trait — check if any dimension key exists in config.
	// We check for non-empty string (not non-zero float) so that 0.0 is recognized as set.
	for _, d := range dimensions {
		if configStr(dir, "persona_"+d.key) != "" {
			return true
		}
	}
	return false
}

// readDimensionValues reads current dimension values from config.
// Returns the provided defaults if no persona has been configured.
func readDimensionValues(dir string, defaults [numDimensions]float64) [numDimensions]float64 {
	if !personaConfigured(dir) {
		return defaults
	}
	var vals [numDimensions]float64
	for i, d := range dimensions {
		vals[i] = configFloat(dir, "persona_"+d.key)
	}
	return vals
}

// writePersona stores dimensions, trait, and regenerates the system prompt.
// Calls die() on config write failure. Skips system prompt for mux.
func writePersona(dir, name, trait string, values [numDimensions]float64, isMux bool) {
	// Sanitize trait: strip control characters, cap at 200 runes.
	trait = config.SanitizeText(trait, 200)

	if err := agent.ConfigSet(dir, "persona_trait", trait); err != nil {
		die("write persona: %v", err)
	}
	for i, d := range dimensions {
		if err := agent.ConfigSet(dir, "persona_"+d.key, fmt.Sprintf("%.2f", values[i])); err != nil {
			die("write persona: %v", err)
		}
	}

	// Regenerate system prompt for agents (mux builds its own dynamically).
	if !isMux {
		var lines []string
		lines = append(lines, fmt.Sprintf("You are %s, an AI assistant.", name))
		lines = append(lines, fmt.Sprintf("Your personality: %s.", trait))
		lines = append(lines, "")
		lines = append(lines, "Communication style:")
		for i, d := range dimensions {
			label := pickLabel(values[i], d.low, d.mid, d.high)
			lines = append(lines, "- "+label)
		}
		if err := agent.ConfigSet(dir, "system_prompt", strings.Join(lines, "\n")); err != nil {
			die("write system prompt: %v", err)
		}
	}
}

// restartHint prints a reminder that the agent/mux needs a restart.
func restartHint(isMux bool, name string) {
	if isMux {
		fmt.Fprintf(os.Stderr, "hint: restart mux to apply: xnc mux stop && xnc mux\n")
	} else {
		fmt.Fprintf(os.Stderr, "hint: restart %s to apply: xnc restart %s\n", name, name)
	}
}

// inferTrait generates a trait descriptor from dimension values.
func inferTrait(values [numDimensions]float64) string {
	type scored struct {
		name string
		dist float64
	}
	adjectives := []string{"warm", "humorous", "verbose", "proactive", "formal", "empathetic", "sarcastic", "autonomous", "interpretive", "creative"}
	lowAdj := []string{"reserved", "serious", "concise", "reactive", "casual", "pragmatic", "earnest", "cautious", "literal", "conventional"}

	var scores []scored
	for i, v := range values {
		dist := v - 0.5
		if dist < 0 {
			dist = -dist
		}
		name := adjectives[i]
		if v < 0.5 {
			name = lowAdj[i]
		}
		scores = append(scores, scored{name, dist})
	}

	// Sort by distance descending (stable to break ties deterministically).
	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].dist > scores[j].dist
	})

	// If no dimension is distinctive, say balanced.
	if len(scores) < 2 || scores[0].dist < 0.1 {
		return "balanced"
	}
	return scores[0].name + " and " + scores[1].name
}

// renderBar draws a 10-char bar visualization for a 0.0-1.0 value.
func renderBar(val float64) string {
	filled := int(val*10 + 0.5)
	if filled > 10 {
		filled = 10
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
}

// pickLabel returns the appropriate description for a dimension value.
func pickLabel(val float64, low, mid, high string) string {
	switch {
	case val < 0.33:
		return low
	case val > 0.66:
		return high
	default:
		return mid
	}
}

// configStr reads a string config value, returning "" on error.
func configStr(dir, key string) string {
	val, err := agent.ConfigGet(dir, key)
	if err != nil {
		return ""
	}
	s, _ := val.(string)
	return s
}

// configFloat reads a float config value, returning 0 on error.
func configFloat(dir, key string) float64 {
	val, err := agent.ConfigGet(dir, key)
	if err != nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return v
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	}
	return 0
}
