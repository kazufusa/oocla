package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kazufusa/oocla/internal/core"
)

// fakeGen records the last input and replays a canned output.
type fakeGen struct {
	last   core.GenerateInput
	out    core.GenerateOutput
	err    error
	chunks []core.StreamChunk // replayed through emit when the caller wants a stream
	// sawEmit records whether the server asked for chunks.
	sawEmit bool
}

func (f *fakeGen) Generate(_ context.Context, in core.GenerateInput, emit func(core.StreamChunk) error) (core.GenerateOutput, error) {
	f.last = in
	f.sawEmit = emit != nil
	if emit != nil {
		for _, c := range f.chunks {
			if err := emit(c); err != nil {
				return core.GenerateOutput{}, err
			}
		}
	}
	return f.out, f.err
}

func newChatServer(t *testing.T, gen core.Generator) http.Handler {
	t.Helper()
	eng := core.NewEngine(core.NewRegistry(testTime()), gen)
	eng.NowFunc = testTime
	return NewServer(eng)
}

func TestChatNonStreaming(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{
		Text:         "The sky is blue.",
		StopReason:   "end_turn",
		InputTokens:  12,
		OutputTokens: 34,
		Total:        2 * time.Second,
		API:          1500 * time.Millisecond,
	}}
	h := newChatServer(t, gen)

	w := do(t, h, "POST", "/api/chat", `{"model":"opus","stream":false,
		"messages":[{"role":"user","content":"why is the sky blue?"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}

	var got ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "opus:latest" {
		t.Errorf("model = %q, want the tagged name", got.Model)
	}
	if got.Message.Role != RoleAssistant || got.Message.Content != "The sky is blue." {
		t.Errorf("message = %+v", got.Message)
	}
	if !got.Done || got.DoneReason != DoneReasonStop {
		t.Errorf("done = %v, done_reason = %q", got.Done, got.DoneReason)
	}
	if got.PromptEvalCount != 12 || got.EvalCount != 34 {
		t.Errorf("token counts = %d/%d", got.PromptEvalCount, got.EvalCount)
	}
	if got.TotalDuration != int64(2*time.Second) {
		t.Errorf("total_duration = %d ns, want nanoseconds", got.TotalDuration)
	}
	if !got.CreatedAt.Equal(testTime()) {
		t.Errorf("created_at = %v", got.CreatedAt)
	}
	// The backend must receive the CLI model name, not the Ollama tag.
	if gen.last.Model != "opus" {
		t.Errorf("backend model = %q, want %q", gen.last.Model, "opus")
	}
	if gen.last.Prompt.User != "why is the sky blue?" {
		t.Errorf("backend prompt = %q", gen.last.Prompt.User)
	}
}

func TestChatPassesSystemAndTools(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "ok", StopReason: "end_turn"}}
	h := newChatServer(t, gen)

	w := do(t, h, "POST", "/api/chat", `{"model":"haiku","stream":false,
		"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"d",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	if gen.last.Prompt.System != "be terse" {
		t.Errorf("system = %q", gen.last.Prompt.System)
	}
	if len(gen.last.Tools) != 1 || gen.last.Tools[0].Function.Name != "get_weather" {
		t.Errorf("tools = %+v", gen.last.Tools)
	}
}

func TestChatReturnsToolCalls(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{
		StopReason: "tool_use",
		ToolCalls: []ToolCall{{Function: ToolCallFunction{
			Name: "get_weather", Arguments: map[string]any{"city": "Tokyo"},
		}}},
	}}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat", `{"model":"opus","stream":false,
		"messages":[{"role":"user","content":"weather?"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	var got ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %+v", got.Message.ToolCalls)
	}
	if got.Message.ToolCalls[0].Function.Arguments["city"] != "Tokyo" {
		t.Errorf("arguments = %+v", got.Message.ToolCalls[0].Function.Arguments)
	}
}

func TestChatMaxTokensBecomesLength(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "trunc", StopReason: "max_tokens"}}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"messages":[{"role":"user","content":"x"}]}`)
	var got ChatResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.DoneReason != DoneReasonLength {
		t.Errorf("done_reason = %q, want %q", got.DoneReason, DoneReasonLength)
	}
}

// An empty conversation is Ollama's preload call, answered without invoking
// the backend; keep_alive 0 turns the acknowledgement into an unload.
func TestChatEmptyMessagesLoadsTheModel(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "should not be called"}}
	h := newChatServer(t, gen)

	w := do(t, h, "POST", "/api/chat", `{"model":"opus","messages":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	var got ChatChunk
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Done || got.DoneReason != DoneReasonLoad {
		t.Errorf("acknowledgement = %+v", got)
	}
	if got.Message.Role != RoleAssistant || got.Message.Content != "" {
		t.Errorf("message = %+v", got.Message)
	}
	if strings.Contains(w.Body.String(), "eval_count") {
		t.Errorf("acknowledgement carries statistics: %s", w.Body)
	}
	if gen.last.Model != "" {
		t.Error("the backend was called for an empty conversation")
	}

	for _, keepAlive := range []string{"0", `"0"`, `"0s"`} {
		w := do(t, h, "POST", "/api/chat", `{"model":"opus","messages":[],"keep_alive":`+keepAlive+`}`)
		var got ChatChunk
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if got.DoneReason != DoneReasonUnload {
			t.Errorf("keep_alive %s: done_reason = %q, want %q", keepAlive, got.DoneReason, DoneReasonUnload)
		}
	}

	if w := do(t, h, "POST", "/api/chat", `{"model":"llama3","messages":[]}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown model: status = %d, want 404", w.Code)
	}
}

func TestChatUnknownModelIs404(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/api/chat",
		`{"model":"llama3","stream":false,"messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestChatRejectsBadRequests(t *testing.T) {
	h := newChatServer(t, &fakeGen{})
	cases := map[string]string{
		"bad json":     `{`,
		"unknown role": `{"model":"opus","stream":false,"messages":[{"role":"wizard","content":"x"}]}`,
	}
	for name, body := range cases {
		w := do(t, h, "POST", "/api/chat", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body %s)", name, w.Code, w.Body)
		}
	}
}

func TestChatBackendErrorIs500(t *testing.T) {
	gen := &fakeGen{err: errors.New("claude exited 1")}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
	if msg, _ := decode(t, w)["error"].(string); !strings.Contains(msg, "claude exited 1") {
		t.Errorf("error = %q", msg)
	}
}

func TestChatCancelledRequestIsNot500(t *testing.T) {
	gen := &fakeGen{err: context.Canceled}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"messages":[{"role":"user","content":"x"}]}`)
	if w.Code == http.StatusInternalServerError {
		t.Errorf("status = %d, want a client-side status for a cancelled request", w.Code)
	}
}

// Ollama streams unless the client opts out, so an absent "stream" field must
// not be read as stream:false.
func TestChatDefaultsToStreaming(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{StopReason: "end_turn"}}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	if !gen.sawEmit {
		t.Error("absent stream field was treated as stream:false")
	}
}

func TestChatNonStreamingDoesNotAskForChunks(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "x", StopReason: "end_turn"}}
	do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"messages":[{"role":"user","content":"x"}]}`)
	if gen.sawEmit {
		t.Error("a non-streaming request must not ask the backend for chunks")
	}
}

// streamLines decodes an NDJSON body.
func streamLines(t *testing.T, body string) []ChatResponse {
	t.Helper()
	var out []ChatResponse
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		var c ChatResponse
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, c)
	}
	return out
}

func TestChatStreaming(t *testing.T) {
	gen := &fakeGen{
		chunks: []core.StreamChunk{{Text: "The "}, {Text: "sky "}, {Text: "is blue."}},
		out: core.GenerateOutput{
			Text: "The sky is blue.", StopReason: "end_turn",
			InputTokens: 12, OutputTokens: 34, Total: 2 * time.Second,
		},
	}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":true,"messages":[{"role":"user","content":"why?"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != ndjsonContentType {
		t.Errorf("Content-Type = %q, want %q", ct, ndjsonContentType)
	}

	lines := streamLines(t, w.Body.String())
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 3 chunks and a terminator:\n%s", len(lines), w.Body)
	}

	var joined string
	for _, c := range lines[:3] {
		if c.Done {
			t.Error("a content chunk is marked done")
		}
		if c.Message.Role != RoleAssistant {
			t.Errorf("chunk role = %q", c.Message.Role)
		}
		if c.Model != "opus:latest" {
			t.Errorf("chunk model = %q", c.Model)
		}
		joined += c.Message.Content
	}
	if joined != "The sky is blue." {
		t.Errorf("concatenated chunks = %q", joined)
	}

	last := lines[3]
	if !last.Done || last.DoneReason != DoneReasonStop {
		t.Errorf("terminator = %+v", last)
	}
	// Repeating the answer in the terminator would double it for any client
	// that concatenates content.
	if last.Message.Content != "" {
		t.Errorf("terminator content = %q, want empty", last.Message.Content)
	}
	if last.PromptEvalCount != 12 || last.EvalCount != 34 || last.TotalDuration != int64(2*time.Second) {
		t.Errorf("terminator statistics = %+v", last)
	}
}

// Clients (strands among them) read the terminator's statistics without a
// presence check, so every field must be written even when its value is zero,
// as on a tool-call turn that never reports an output count.
func TestChatFinalAlwaysCarriesStatistics(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{
		StopReason: "tool_use",
		ToolCalls:  []ToolCall{{Function: ToolCallFunction{Name: "f", Arguments: map[string]any{}}}},
	}}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"messages":[{"role":"user","content":"x"}]}`)
	for _, key := range []string{
		"done_reason", "total_duration", "load_duration", "prompt_eval_count",
		"prompt_eval_duration", "eval_count", "eval_duration",
	} {
		if !strings.Contains(w.Body.String(), `"`+key+`"`) {
			t.Errorf("terminator lacks %q:\n%s", key, w.Body)
		}
	}
}

// Content chunks carry no statistics fields on a real Ollama; only the
// terminator does.
func TestChatStreamChunksCarryNoStatistics(t *testing.T) {
	gen := &fakeGen{
		chunks: []core.StreamChunk{{Text: "hi"}},
		out:    core.GenerateOutput{Text: "hi", StopReason: "end_turn", InputTokens: 1, OutputTokens: 2},
	}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":true,"messages":[{"role":"user","content":"x"}]}`)
	raw := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(raw) != 2 {
		t.Fatalf("got %d lines, want a chunk and a terminator:\n%s", len(raw), w.Body)
	}
	if strings.Contains(raw[0], "eval_count") {
		t.Errorf("content chunk carries statistics: %s", raw[0])
	}
	if !strings.Contains(raw[1], `"eval_count":2`) || !strings.Contains(raw[1], `"load_duration":0`) {
		t.Errorf("terminator lacks statistics: %s", raw[1])
	}
}

func TestChatStreamingSeparatesThinking(t *testing.T) {
	gen := &fakeGen{
		chunks: []core.StreamChunk{{Thinking: "hmm"}, {Text: "42"}},
		out:    core.GenerateOutput{Text: "42", Thinking: "hmm", StopReason: "end_turn"},
	}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":true,"messages":[{"role":"user","content":"x"}]}`)
	lines := streamLines(t, w.Body.String())
	if lines[0].Message.Thinking != "hmm" || lines[0].Message.Content != "" {
		t.Errorf("thinking chunk = %+v", lines[0].Message)
	}
	if lines[1].Message.Content != "42" || lines[1].Message.Thinking != "" {
		t.Errorf("text chunk = %+v", lines[1].Message)
	}
}

func TestChatStreamingToolCalls(t *testing.T) {
	tc := ToolCall{Function: ToolCallFunction{Name: "get_weather", Arguments: map[string]any{"city": "Tokyo"}}}
	gen := &fakeGen{
		chunks: []core.StreamChunk{{ToolCalls: []ToolCall{tc}}},
		out:    core.GenerateOutput{ToolCalls: []ToolCall{tc}, StopReason: "tool_use"},
	}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":true,"messages":[{"role":"user","content":"weather?"}]}`)
	lines := streamLines(t, w.Body.String())
	if len(lines[0].Message.ToolCalls) != 1 {
		t.Fatalf("first line = %+v", lines[0])
	}
	if lines[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool call = %+v", lines[0].Message.ToolCalls[0])
	}
}

// The status line is already on the wire when a stream fails, so the error has
// to be reported in-band.
func TestChatStreamingErrorIsReportedInBand(t *testing.T) {
	gen := &fakeGen{chunks: []core.StreamChunk{{Text: "partial"}}, err: errors.New("claude exited 1")}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":true,"messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want the stream to have already started", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "claude exited 1") {
		t.Errorf("body does not carry the error:\n%s", body)
	}
	last := strings.TrimSpace(body[strings.LastIndex(strings.TrimSpace(body), "\n")+1:])
	var errLine map[string]string
	if err := json.Unmarshal([]byte(last), &errLine); err != nil || errLine["error"] == "" {
		t.Errorf("last line = %q, want a JSON error object", last)
	}
}
