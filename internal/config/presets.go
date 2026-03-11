package config

// Preset defines a named persona preset with trait description and dimension values.
type Preset struct {
	Name  string
	Trait string
	Dims  PersonaDimensions
}

// Presets is the authoritative, ordered list of persona presets.
var Presets = []Preset{
	{"professional", "precise and professional", PersonaDimensions{
		Warmth: 0.4, Humor: 0.1, Verbosity: 0.3, Proactiveness: 0.5,
		Formality: 0.9, Empathy: 0.4, Sarcasm: 0.0, Autonomy: 0.4, Interpretation: 0.1, Creativity: 0.3}},
	{"casual", "casual and approachable", PersonaDimensions{
		Warmth: 0.8, Humor: 0.7, Verbosity: 0.5, Proactiveness: 0.6,
		Formality: 0.1, Empathy: 0.7, Sarcasm: 0.3, Autonomy: 0.6, Interpretation: 0.4, Creativity: 0.6}},
	{"assistant", "helpful and balanced", PersonaDimensions{
		Warmth: 0.6, Humor: 0.3, Verbosity: 0.4, Proactiveness: 0.8,
		Formality: 0.5, Empathy: 0.5, Sarcasm: 0.0, Autonomy: 0.7, Interpretation: 0.2, Creativity: 0.4}},
	{"minimal", "terse and efficient", PersonaDimensions{
		Warmth: 0.2, Humor: 0.0, Verbosity: 0.1, Proactiveness: 0.3,
		Formality: 0.6, Empathy: 0.2, Sarcasm: 0.0, Autonomy: 0.3, Interpretation: 0.0, Creativity: 0.2}},
	{"creative", "inventive and expressive", PersonaDimensions{
		Warmth: 0.7, Humor: 0.6, Verbosity: 0.6, Proactiveness: 0.7,
		Formality: 0.2, Empathy: 0.6, Sarcasm: 0.2, Autonomy: 0.8, Interpretation: 0.5, Creativity: 0.9}},
	{"friendly", "friendly and straightforward", PersonaDimensions{
		Warmth: 0.7, Humor: 0.4, Verbosity: 0.4, Proactiveness: 0.6,
		Formality: 0.3, Empathy: 0.6, Sarcasm: 0.1, Autonomy: 0.5, Interpretation: 0.2, Creativity: 0.4}},
	{"analytical", "precise and analytical", PersonaDimensions{
		Warmth: 0.4, Humor: 0.2, Verbosity: 0.5, Proactiveness: 0.5,
		Formality: 0.6, Empathy: 0.3, Sarcasm: 0.0, Autonomy: 0.4, Interpretation: 0.1, Creativity: 0.3}},
	{"witty", "witty and concise", PersonaDimensions{
		Warmth: 0.5, Humor: 0.7, Verbosity: 0.2, Proactiveness: 0.6,
		Formality: 0.4, Empathy: 0.4, Sarcasm: 0.3, Autonomy: 0.6, Interpretation: 0.3, Creativity: 0.6}},
	{"earnest", "earnest and helpful", PersonaDimensions{
		Warmth: 0.7, Humor: 0.3, Verbosity: 0.5, Proactiveness: 0.8,
		Formality: 0.5, Empathy: 0.7, Sarcasm: 0.0, Autonomy: 0.7, Interpretation: 0.2, Creativity: 0.4}},
	{"playful", "playful and inventive", PersonaDimensions{
		Warmth: 0.6, Humor: 0.6, Verbosity: 0.3, Proactiveness: 0.7,
		Formality: 0.2, Empathy: 0.5, Sarcasm: 0.2, Autonomy: 0.7, Interpretation: 0.4, Creativity: 0.9}},
}

// PresetMap provides O(1) lookup of preset dimensions by name.
var PresetMap map[string]PersonaDimensions

// PresetTraits provides O(1) lookup of preset trait strings by name.
var PresetTraits map[string]string

// PresetNames lists preset names in order, suitable for tool enum definitions.
var PresetNames []string

func init() {
	PresetMap = make(map[string]PersonaDimensions, len(Presets))
	PresetTraits = make(map[string]string, len(Presets))
	PresetNames = make([]string, 0, len(Presets))
	for _, p := range Presets {
		PresetMap[p.Name] = p.Dims
		PresetTraits[p.Name] = p.Trait
		PresetNames = append(PresetNames, p.Name)
	}
}
