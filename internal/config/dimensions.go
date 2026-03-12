// Package config — dimensions.go defines the shared persona dimension descriptors
// used by both the mux prompt builder and agent system prompt generator.
package config

// DimensionDesc holds the low / mid / high text descriptions for a single dimension.
type DimensionDesc struct {
	Name string
	Low  string
	Mid  string
	High string
}

// DimensionDescriptors defines the 3-tier description for each persona dimension.
// Order matches PersonaDimensions field order.
var DimensionDescriptors = []DimensionDesc{
	{"warmth", "Be clinical and matter-of-fact", "Be friendly but professional", "Be warm, caring, and personal"},
	{"humor", "Never joke or use humor", "Use occasional humor when appropriate", "Be playful, use jokes and wit freely"},
	{"verbosity", "Be extremely terse — minimum words", "Balance brevity and detail", "Be thorough and detailed in explanations"},
	{"proactiveness", "Only respond when explicitly asked", "Suggest actions when clearly relevant", "Actively anticipate needs and volunteer information"},
	{"formality", "Be casual, slang is fine", "Professional but relaxed", "Be formal and proper at all times"},
	{"empathy", "Be matter-of-fact, skip emotional acknowledgment", "Acknowledge feelings when relevant", "Be emotionally attuned and supportive"},
	{"sarcasm", "Never be sarcastic", "Light irony occasionally", "Use sharp wit and sarcasm freely"},
	{"autonomy", "Always ask before taking action", "Act on clear intent, ask when ambiguous", "Take initiative freely, act first"},
	{"interpretation", "Pass user messages through completely raw", "Fix obvious typos silently", "Actively refine and clarify messages before forwarding"},
	{"creativity", "Be straightforward and predictable", "Balance conventional and novel approaches", "Prefer creative and surprising solutions"},
}

// PickDescription selects the low / mid / high description based on value thresholds.
// low: < 0.33, mid: 0.33-0.66, high: > 0.66.
func PickDescription(value float64, desc DimensionDesc) string {
	switch {
	case value < 0.33:
		return desc.Low
	case value > 0.66:
		return desc.High
	default:
		return desc.Mid
	}
}

// DimensionValues returns the dimension values in the same order as DimensionDescriptors.
func DimensionValues(d PersonaDimensions) []float64 {
	return []float64{
		d.Warmth,
		d.Humor,
		d.Verbosity,
		d.Proactiveness,
		d.Formality,
		d.Empathy,
		d.Sarcasm,
		d.Autonomy,
		d.Interpretation,
		d.Creativity,
	}
}
