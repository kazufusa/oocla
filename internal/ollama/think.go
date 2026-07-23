package ollama

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kazufusa/oocla/internal/core"
)

// ErrUnsupportedThink is returned for a think value the backend cannot honour.
var ErrUnsupportedThink = errors.New(`think must be a boolean or one of "low", "medium", "high"`)

// Think re-exports the neutral request; parsing the wire field is the part
// that is Ollama's.
type Think = core.Think

// ParseThink interprets Ollama's think field, which is either a boolean or an
// effort level.
func ParseThink(raw json.RawMessage) (Think, error) {
	if len(raw) == 0 {
		return Think{}, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return Think{Enabled: b}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return Think{}, ErrUnsupportedThink
	}
	if s == "" {
		return Think{}, nil
	}
	if !core.ValidEffort(s) {
		return Think{}, fmt.Errorf("%w: got %q", ErrUnsupportedThink, s)
	}
	return Think{Enabled: true, Effort: s}, nil
}
