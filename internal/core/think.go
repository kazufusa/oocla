package core

// Think says whether the client wants the model's reasoning back, and how much
// of it to spend. Each dialect produces one from its own wire fields: Ollama
// from `think`, OpenAI from `reasoning_effort`.
//
// The CLI always reasons; there is no way to turn it off. What the flag really
// controls, then, is whether the reasoning is reported. Ollama's contract is
// the same: thinking is absent from the response unless it was asked for.
type Think struct {
	Enabled bool
	// Effort is the claude CLI effort level, empty for the model's default.
	Effort string
}

// effortLevels are the values the CLI accepts for --effort. Ollama only defines
// the first three; the rest are accepted so a Claude-aware client can reach
// them.
var effortLevels = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

// ValidEffort reports whether s is an effort level the CLI accepts.
func ValidEffort(s string) bool { return effortLevels[s] }

// Apply folds the request into the backend input.
func (t Think) Apply(in *GenerateInput) {
	in.IncludeThinking = t.Enabled
	in.Effort = t.Effort
}
