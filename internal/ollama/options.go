package ollama

import (
	"fmt"
	"sort"
)

// Options is the part of Ollama's options object oocla can act on.
//
// Ollama's options configure a local llama.cpp runner: sampling temperature,
// context size, penalties, seeds. The claude CLI exposes none of that, so
// almost everything here is accepted and dropped. Rejecting the request instead
// would be worse: clients send a default options block whether or not they care
// about it, and Ollama itself ignores options a model does not implement.
type Options struct {
	// Stop are sequences that end generation. Nothing in the CLI can stop
	// sampling, so the answer is truncated at the first one instead.
	Stop []string
}

// ParseOptions reads the options object. It returns the options that will be
// honoured and the names of those that will not, sorted, for logging.
func ParseOptions(raw map[string]any) (Options, []string, error) {
	var opts Options
	var ignored []string
	for k, v := range raw {
		switch k {
		case "stop":
			stop, err := stringOrStrings(v)
			if err != nil {
				return Options{}, nil, fmt.Errorf("options.stop: %w", err)
			}
			opts.Stop = stop
		default:
			ignored = append(ignored, k)
		}
	}
	sort.Strings(ignored)
	return opts, ignored, nil
}

// stringOrStrings accepts Ollama's two spellings of a stop list.
func stringOrStrings(v any) ([]string, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case string:
		if t == "" {
			return nil, nil
		}
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("want strings, got %T", e)
			}
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("want a string or an array of strings, got %T", v)
	}
}
