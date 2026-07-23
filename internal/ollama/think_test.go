package ollama

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/kazufusa/oocla/internal/core"
)

func TestParseThink(t *testing.T) {
	cases := map[string]struct {
		in   string
		want Think
	}{
		"absent":       {"", Think{}},
		"false":        {`false`, Think{}},
		"true":         {`true`, Think{Enabled: true}},
		"empty string": {`""`, Think{}},
		"low":          {`"low"`, Think{Enabled: true, Effort: "low"}},
		"high":         {`"high"`, Think{Enabled: true, Effort: "high"}},
		"max":          {`"max"`, Think{Enabled: true, Effort: "max"}},
	}
	for name, c := range cases {
		got, err := ParseThink(json.RawMessage(c.in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: ParseThink(%s) = %+v, want %+v", name, c.in, got, c.want)
		}
	}
}

func TestParseThinkRejectsUnknownLevels(t *testing.T) {
	for _, in := range []string{`"turbo"`, `"LOW"`, `3`, `{"level":"low"}`} {
		if _, err := ParseThink(json.RawMessage(in)); !errors.Is(err, ErrUnsupportedThink) {
			t.Errorf("ParseThink(%s) err = %v, want ErrUnsupportedThink", in, err)
		}
	}
}

func TestChatThinkIsOffByDefault(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "x", StopReason: "end_turn"}}
	do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"messages":[{"role":"user","content":"x"}]}`)
	if gen.last.IncludeThinking {
		t.Error("thinking was requested without the client asking")
	}
	if gen.last.Effort != "" {
		t.Errorf("Effort = %q, want the model's default", gen.last.Effort)
	}
}

func TestChatThinkEnables(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "x", Thinking: "hmm", StopReason: "end_turn"}}
	h := newChatServer(t, gen)

	w := do(t, h, "POST", "/api/chat",
		`{"model":"opus","stream":false,"think":true,"messages":[{"role":"user","content":"x"}]}`)
	if !gen.last.IncludeThinking {
		t.Error("think:true did not reach the backend")
	}
	var got ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Message.Thinking != "hmm" {
		t.Errorf("thinking = %q", got.Message.Thinking)
	}
}

func TestChatThinkLevelSetsEffort(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "x", StopReason: "end_turn"}}
	do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"think":"high","messages":[{"role":"user","content":"x"}]}`)
	if !gen.last.IncludeThinking || gen.last.Effort != "high" {
		t.Errorf("IncludeThinking = %v Effort = %q", gen.last.IncludeThinking, gen.last.Effort)
	}
}

func TestChatRejectsUnknownThink(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/api/chat",
		`{"model":"opus","stream":false,"think":"turbo","messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGenerateThink(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "x", Thinking: "hmm", StopReason: "end_turn"}}
	h := newChatServer(t, gen)

	do(t, h, "POST", "/api/generate", `{"model":"opus","stream":false,"prompt":"x"}`)
	if gen.last.IncludeThinking {
		t.Error("thinking was requested without the client asking")
	}

	w := do(t, h, "POST", "/api/generate",
		`{"model":"opus","stream":false,"think":"low","prompt":"x"}`)
	if !gen.last.IncludeThinking || gen.last.Effort != "low" {
		t.Errorf("IncludeThinking = %v Effort = %q", gen.last.IncludeThinking, gen.last.Effort)
	}
	var got GenerateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Thinking != "hmm" {
		t.Errorf("thinking = %q", got.Thinking)
	}
}

func TestGenerateRejectsUnknownThink(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/api/generate",
		`{"model":"opus","stream":false,"think":"turbo","prompt":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
