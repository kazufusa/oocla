package core

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"time"
)

// Model is one entry of the catalog oocla advertises. There is no model file
// on disk, so Size and Digest are synthesized deterministically from the name:
// Ollama clients expect both fields to be present and stable.
type Model struct {
	// Name is the Ollama-facing name, always "<base>:<tag>".
	Name string
	// CLIName is what gets passed to `claude --model`.
	CLIName string
	// Description is shown by some clients in place of a model card.
	Description string
	// ContextLength is advertised through model_info.
	ContextLength int
	// Capabilities is the Ollama capability list, e.g. "completion", "tools".
	Capabilities []string

	Size       int64
	Digest     string
	ModifiedAt time.Time
}

const (
	defaultTag = "latest"
	// passthroughPrefix lets callers name an exact Claude model id instead of
	// an alias, e.g. "claude-haiku-4-5-20251001".
	passthroughPrefix = "claude-"
)

// baseCapabilities is what every Claude model behind oocla can do. Tool use is
// included because JSON tools are the one tool mechanism oocla exposes;
// thinking because the models reason and the reasoning can be reported.
var baseCapabilities = []string{"completion", "tools", "thinking"}

// aliases are the model names oocla advertises in /api/tags, in list order.
var aliases = []struct {
	name        string
	description string
	context     int
}{
	{"opus", "Claude Opus via the claude CLI", 200000},
	{"sonnet", "Claude Sonnet via the claude CLI", 200000},
	{"haiku", "Claude Haiku via the claude CLI", 200000},
	{"fable", "Claude Fable via the claude CLI", 200000},
}

// Registry resolves Ollama model names to claude CLI model arguments.
type Registry struct {
	models   []Model
	byCLI    map[string]Model
	modified time.Time
}

// NewRegistry builds the catalog. modified is reported as each model's
// modified_at; callers pass a fixed time so the catalog does not appear to
// change on every request.
func NewRegistry(modified time.Time) *Registry {
	r := &Registry{
		byCLI:    make(map[string]Model, len(aliases)),
		modified: modified.UTC(),
	}
	for _, a := range aliases {
		m := newModel(a.name, a.description, a.context, r.modified)
		r.models = append(r.models, m)
		r.byCLI[a.name] = m
	}
	return r
}

func newModel(cliName, description string, context int, modified time.Time) Model {
	digest := synthDigest(cliName)
	return Model{
		Name:          cliName + ":" + defaultTag,
		CLIName:       cliName,
		Description:   description,
		ContextLength: context,
		Capabilities:  baseCapabilities,
		Size:          synthSize(digest),
		Digest:        digest,
		ModifiedAt:    modified,
	}
}

// List returns the advertised catalog in a stable order.
func (r *Registry) List() []Model {
	out := make([]Model, len(r.models))
	copy(out, r.models)
	return out
}

// Lookup resolves an Ollama model name. It accepts the bare alias ("opus"),
// the tagged form ("opus:latest"), and exact Claude model ids
// ("claude-haiku-4-5-20251001"), case-insensitively. Any other tag than
// "latest" is rejected: oocla has no way to serve a pinned revision.
func (r *Registry) Lookup(name string) (Model, bool) {
	base, tag, ok := splitTag(name)
	if !ok {
		return Model{}, false
	}
	if tag != defaultTag {
		return Model{}, false
	}
	if m, ok := r.byCLI[base]; ok {
		return m, true
	}
	if isPassthrough(base) {
		return newModel(base, "Claude model passed through to the claude CLI", 200000, r.modified), true
	}
	return Model{}, false
}

// splitTag lowercases name and splits it into base and tag, defaulting the tag
// to "latest". It reports false for names that cannot be a model reference.
func splitTag(name string) (base, tag string, ok bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", "", false
	}
	base, tag = name, defaultTag
	if i := strings.LastIndex(name, ":"); i >= 0 {
		base, tag = name[:i], name[i+1:]
	}
	if base == "" || tag == "" {
		return "", "", false
	}
	return base, tag, true
}

// isPassthrough reports whether base names an exact Claude model id. The name
// ends up in an exec argv, never a shell, but the charset is still restricted
// so a hostile model name cannot look like a flag.
func isPassthrough(base string) bool {
	if !strings.HasPrefix(base, passthroughPrefix) || len(base) <= len(passthroughPrefix) {
		return false
	}
	for _, c := range base {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// synthDigest gives each model a stable 64 hex char digest. Clients use it as
// an identity key and some refuse entries without one.
func synthDigest(cliName string) string {
	sum := sha256.Sum256([]byte("oocla/model/" + cliName))
	return hex.EncodeToString(sum[:])
}

// synthSize gives each model a stable plausible byte size in the 4-8 GiB range.
// Nothing is on disk; the field exists only because clients render it.
func synthSize(digest string) int64 {
	raw, err := hex.DecodeString(digest[:8])
	if err != nil {
		return 4 << 30
	}
	return int64(4<<30) + int64(binary.BigEndian.Uint32(raw))
}
