package ollama

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kazufusa/oocla/internal/core"
	"time"
)

func generateLines(t *testing.T, body string) []GenerateResponse {
	t.Helper()
	var out []GenerateResponse
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		var g GenerateResponse
		if err := json.Unmarshal([]byte(line), &g); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, g)
	}
	return out
}

func TestGenerateNonStreaming(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{
		Text: "Because of Rayleigh scattering.", StopReason: "end_turn",
		InputTokens: 9, OutputTokens: 21, Total: time.Second,
	}}
	w := do(t, newChatServer(t, gen), "POST", "/api/generate",
		`{"model":"opus","stream":false,"system":"be terse","prompt":"why is the sky blue?"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}

	var got GenerateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Response != "Because of Rayleigh scattering." {
		t.Errorf("response = %q", got.Response)
	}
	if got.Model != "opus:latest" || !got.Done || got.DoneReason != DoneReasonStop {
		t.Errorf("response envelope = %+v", got)
	}
	if got.PromptEvalCount != 9 || got.EvalCount != 21 {
		t.Errorf("token counts = %d/%d", got.PromptEvalCount, got.EvalCount)
	}
	if gen.last.Prompt.System != "be terse" || gen.last.Prompt.User != "why is the sky blue?" {
		t.Errorf("backend prompt = %+v", gen.last.Prompt)
	}
	if gen.sawEmit {
		t.Error("a non-streaming request asked the backend for chunks")
	}
}

func TestGenerateStreaming(t *testing.T) {
	gen := &fakeGen{
		chunks: []core.StreamChunk{{Text: "Because "}, {Text: "of physics."}},
		out:    core.GenerateOutput{Text: "Because of physics.", StopReason: "end_turn", OutputTokens: 5},
	}
	w := do(t, newChatServer(t, gen), "POST", "/api/generate",
		`{"model":"opus","prompt":"why?"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != ndjsonContentType {
		t.Errorf("Content-Type = %q", ct)
	}

	lines := generateLines(t, w.Body.String())
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 2 chunks and a terminator:\n%s", len(lines), w.Body)
	}
	var joined string
	for _, l := range lines[:2] {
		if l.Done {
			t.Error("a content chunk is marked done")
		}
		joined += l.Response
	}
	if joined != "Because of physics." {
		t.Errorf("concatenated chunks = %q", joined)
	}
	last := lines[2]
	if !last.Done || last.Response != "" || last.EvalCount != 5 {
		t.Errorf("terminator = %+v", last)
	}
}

// Tool calls belong to /api/chat; the generate stream has no field for them.
func TestGenerateStreamingSkipsEmptyChunks(t *testing.T) {
	tc := ToolCall{Function: ToolCallFunction{Name: "f"}}
	gen := &fakeGen{
		chunks: []core.StreamChunk{{ToolCalls: []ToolCall{tc}}, {Text: "hi"}},
		out:    core.GenerateOutput{Text: "hi", StopReason: "end_turn"},
	}
	w := do(t, newChatServer(t, gen), "POST", "/api/generate", `{"model":"opus","prompt":"x"}`)
	lines := generateLines(t, w.Body.String())
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want the tool-call chunk dropped:\n%s", len(lines), w.Body)
	}
}

// An empty prompt is Ollama's preload call, not a generation request.
func TestGenerateEmptyPromptLoadsTheModel(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "should not be called"}}
	w := do(t, newChatServer(t, gen), "POST", "/api/generate", `{"model":"opus"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got GenerateResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Done || got.DoneReason != DoneReasonLoad || got.Response != "" {
		t.Errorf("response = %+v", got)
	}
	if gen.last.Model != "" {
		t.Error("the backend was called for an empty prompt")
	}
}

func TestGenerateUnknownModelIs404(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/api/generate",
		`{"model":"llama3","stream":false,"prompt":"x"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestGenerateBackendErrorIs500(t *testing.T) {
	gen := &fakeGen{err: errors.New("boom")}
	w := do(t, newChatServer(t, gen), "POST", "/api/generate",
		`{"model":"opus","stream":false,"prompt":"x"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
}

// Ollama's continuation handle cannot be honoured without keeping sessions,
// so it is accepted and ignored.
func TestGenerateContextIsIgnored(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "ok", StopReason: "end_turn"}}
	w := do(t, newChatServer(t, gen), "POST", "/api/generate",
		`{"model":"opus","stream":false,"prompt":"b","context":[999]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	if gen.last.Prompt.User != "b" {
		t.Errorf("prompt = %q", gen.last.Prompt.User)
	}
}
