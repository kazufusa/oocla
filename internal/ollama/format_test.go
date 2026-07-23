package ollama

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kazufusa/oocla/internal/core"
)

func TestParseFormat(t *testing.T) {
	cases := map[string]struct {
		in   string
		want Format
	}{
		"absent":            {"", Format{}},
		"empty string":      {`""`, Format{}},
		"json mode":         {`"json"`, Format{JSONOnly: true}},
		"empty object":      {`{}`, Format{}},
		"schema":            {`{"type":"object","properties":{"age":{"type":"integer"}}}`, Format{Schema: `{"type":"object","properties":{"age":{"type":"integer"}}}`}},
		"schema whitespace": {"{\n  \"type\": \"object\"\n}", Format{Schema: `{"type":"object"}`}},
	}
	for name, c := range cases {
		got, err := ParseFormat(json.RawMessage(c.in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: ParseFormat(%s) = %+v, want %+v", name, c.in, got, c.want)
		}
	}
}

func TestParseFormatRejectsUnsupported(t *testing.T) {
	for _, in := range []string{`"yaml"`, `"xml"`, `[1,2]`, `42`} {
		if _, err := ParseFormat(json.RawMessage(in)); !errors.Is(err, ErrUnsupportedFormat) {
			t.Errorf("ParseFormat(%s) err = %v, want ErrUnsupportedFormat", in, err)
		}
	}
}

func TestChatPassesSchemaToBackend(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: `{"age":3}`, StopReason: "end_turn"}}
	do(t, newChatServer(t, gen), "POST", "/api/chat", `{"model":"opus","stream":false,
		"format":{"type":"object","properties":{"age":{"type":"integer"}}},
		"messages":[{"role":"user","content":"how old is the dog?"}]}`)
	if gen.last.JSONSchema != `{"type":"object","properties":{"age":{"type":"integer"}}}` {
		t.Errorf("JSONSchema = %q", gen.last.JSONSchema)
	}
}

func TestChatJSONModeInstructsInsteadOfConstraining(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "{}", StopReason: "end_turn"}}
	do(t, newChatServer(t, gen), "POST", "/api/chat", `{"model":"opus","stream":false,"format":"json",
		"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"x"}]}`)
	if gen.last.JSONSchema != "" {
		t.Errorf("JSONSchema = %q, want json mode to avoid the schema path", gen.last.JSONSchema)
	}
	if !strings.Contains(gen.last.Prompt.System, "be terse") {
		t.Errorf("System = %q, want the client's system prompt kept", gen.last.Prompt.System)
	}
	if !strings.Contains(gen.last.Prompt.System, "JSON") {
		t.Errorf("System = %q, want a JSON instruction", gen.last.Prompt.System)
	}
}

// The directive is only ever added when the client asks for JSON mode.
func TestChatAddsNothingToThePromptByDefault(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "x", StopReason: "end_turn"}}
	do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"messages":[{"role":"user","content":"x"}]}`)
	if gen.last.Prompt.System != "" {
		t.Errorf("System = %q, want nothing added", gen.last.Prompt.System)
	}
}

func TestChatRejectsUnsupportedFormat(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/api/chat",
		`{"model":"opus","stream":false,"format":"yaml","messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGeneratePassesFormatToBackend(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "{}", StopReason: "end_turn"}}
	h := newChatServer(t, gen)

	do(t, h, "POST", "/api/generate",
		`{"model":"opus","stream":false,"format":{"type":"object"},"prompt":"x"}`)
	if gen.last.JSONSchema != `{"type":"object"}` {
		t.Errorf("JSONSchema = %q", gen.last.JSONSchema)
	}

	do(t, h, "POST", "/api/generate",
		`{"model":"opus","stream":false,"format":"json","system":"be terse","prompt":"x"}`)
	if gen.last.JSONSchema != "" || !strings.Contains(gen.last.Prompt.System, "be terse") {
		t.Errorf("json mode = %+v", gen.last)
	}
}

func TestGenerateRejectsUnsupportedFormat(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/api/generate",
		`{"model":"opus","stream":false,"format":"yaml","prompt":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
