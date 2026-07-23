package ollama

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kazufusa/oocla/internal/core"
)

// ErrUnsupportedFormat is returned for a format the backend cannot honour.
var ErrUnsupportedFormat = errors.New(`format must be "json" or a JSON Schema object`)

// Format re-exports the neutral answer shape; parsing the wire field is the
// part that is Ollama's.
type Format = core.Format

// ParseFormat interprets Ollama's format field, which is either the string
// "json" or a JSON Schema object.
func ParseFormat(raw json.RawMessage) (Format, error) {
	if len(raw) == 0 {
		return Format{}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "":
			return Format{}, nil
		case "json":
			return Format{JSONOnly: true}, nil
		default:
			return Format{}, fmt.Errorf("%w: got %q", ErrUnsupportedFormat, s)
		}
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return Format{}, ErrUnsupportedFormat
	}
	if len(obj) == 0 {
		return Format{}, nil
	}
	return Format{Schema: core.CompactJSON(raw)}, nil
}
