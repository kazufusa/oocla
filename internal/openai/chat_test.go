package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kazufusa/oocla/internal/core"
)

// sseData returns the payloads of an SSE body, in order.
func sseData(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			out = append(out, after)
		}
	}
	return out
}

func TestOpenAIChatCompletion(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{
		Text: "Tokyo", StopReason: "end_turn", InputTokens: 10, OutputTokens: 2,
	}}
	w := do(t, newChatServer(t, gen), "POST", "/v1/chat/completions",
		`{"model":"opus","messages":[{"role":"user","content":"capital of Japan?"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}

	var got oaiChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Object != "chat.completion" || !strings.HasPrefix(got.ID, "chatcmpl-") {
		t.Errorf("envelope = %+v", got)
	}
	if got.Model != "opus:latest" {
		t.Errorf("model = %q", got.Model)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %+v", got.Choices)
	}
	c := got.Choices[0]
	if c.Message == nil || c.Message.Content != "Tokyo" || c.Message.Role != core.RoleAssistant {
		t.Errorf("message = %+v", c.Message)
	}
	if c.FinishReason == nil || *c.FinishReason != finishStop {
		t.Errorf("finish_reason = %v", c.FinishReason)
	}
	if got.Usage == nil || got.Usage.PromptTokens != 10 || got.Usage.TotalTokens != 12 {
		t.Errorf("usage = %+v", got.Usage)
	}
	// stream defaults to false here, unlike the Ollama endpoint.
	if gen.sawEmit {
		t.Error("a non-streaming OpenAI request asked for chunks")
	}
}

// OpenAI encodes tool call arguments as a JSON string, not an object.
func TestOpenAIToolCalls(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{
		StopReason: "tool_use",
		ToolCalls: []core.ToolCall{{Function: core.ToolCallFunction{
			Name: "get_weather", Arguments: map[string]any{"city": "Tokyo"},
		}}},
	}}
	w := do(t, newChatServer(t, gen), "POST", "/v1/chat/completions",
		`{"model":"opus","messages":[{"role":"user","content":"weather?"}],
		 "tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`)

	var got oaiChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	calls := got.Choices[0].Message.ToolCalls
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %+v", calls)
	}
	if calls[0].Type != "function" || calls[0].ID == "" {
		t.Errorf("tool call = %+v", calls[0])
	}
	if calls[0].Function.Arguments != `{"city":"Tokyo"}` {
		t.Errorf("arguments = %q, want a JSON string", calls[0].Function.Arguments)
	}
	if *got.Choices[0].FinishReason != finishToolCalls {
		t.Errorf("finish_reason = %q", *got.Choices[0].FinishReason)
	}
}

// A tool result names the call it answers; the tool's own name has to be
// recovered from the call it refers to.
func TestOpenAIToolResultResolvesName(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "22C", StopReason: "end_turn"}}
	do(t, newChatServer(t, gen), "POST", "/v1/chat/completions", `{"model":"opus","messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","tool_calls":[{"id":"call_abc","type":"function",
			"function":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}]},
		{"role":"tool","tool_call_id":"call_abc","content":"{\"temp\":22}"}]}`)

	prompt := gen.last.Prompt.User
	for _, want := range []string{"get_weather", `{"city":"Tokyo"}`, `{"temp":22}`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
}

func TestOpenAIContentParts(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "ok", StopReason: "end_turn"}}
	w := do(t, newChatServer(t, gen), "POST", "/v1/chat/completions", `{"model":"opus","messages":[
		{"role":"user","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	if gen.last.Prompt.User != "hello world" {
		t.Errorf("prompt = %q", gen.last.Prompt.User)
	}
}

func TestOpenAIRejectsNonTextParts(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/v1/chat/completions", `{"model":"opus","messages":[
		{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://x/y.png"}}]}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestOpenAIDeveloperRoleIsSystem(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "ok", StopReason: "end_turn"}}
	do(t, newChatServer(t, gen), "POST", "/v1/chat/completions", `{"model":"opus","messages":[
		{"role":"developer","content":"be terse"},{"role":"user","content":"x"}]}`)
	if gen.last.Prompt.System != "be terse" {
		t.Errorf("System = %q", gen.last.Prompt.System)
	}
}

func TestOpenAIResponseFormat(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "{}", StopReason: "end_turn"}}
	h := newChatServer(t, gen)

	do(t, h, "POST", "/v1/chat/completions", `{"model":"opus","response_format":{"type":"json_object"},
		"messages":[{"role":"user","content":"x"}]}`)
	if gen.last.JSONSchema != "" || !strings.Contains(gen.last.Prompt.System, "JSON") {
		t.Errorf("json_object mode = %+v", gen.last)
	}

	do(t, h, "POST", "/v1/chat/completions", `{"model":"opus",
		"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object"}}},
		"messages":[{"role":"user","content":"x"}]}`)
	if gen.last.JSONSchema != `{"type":"object"}` {
		t.Errorf("JSONSchema = %q", gen.last.JSONSchema)
	}
}

func TestOpenAIRejectsBadResponseFormat(t *testing.T) {
	h := newChatServer(t, &fakeGen{})
	for _, body := range []string{
		`{"model":"opus","response_format":{"type":"yaml"},"messages":[{"role":"user","content":"x"}]}`,
		`{"model":"opus","response_format":{"type":"json_schema"},"messages":[{"role":"user","content":"x"}]}`,
	} {
		if w := do(t, h, "POST", "/v1/chat/completions", body); w.Code != http.StatusBadRequest {
			t.Errorf("status = %d for %s", w.Code, body)
		}
	}
}

func TestOpenAIStopAndReasoningEffort(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "keep END drop", StopReason: "end_turn"}}
	h := newChatServer(t, gen)

	w := do(t, h, "POST", "/v1/chat/completions",
		`{"model":"opus","stop":"END","reasoning_effort":"high","messages":[{"role":"user","content":"x"}]}`)
	if !gen.last.IncludeThinking || gen.last.Effort != "high" {
		t.Errorf("effort = %+v", gen.last)
	}
	var got oaiChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Choices[0].Message.Content != "keep " {
		t.Errorf("content = %v, want it cut at the stop sequence", got.Choices[0].Message.Content)
	}

	do(t, h, "POST", "/v1/chat/completions",
		`{"model":"opus","stop":["A","B"],"messages":[{"role":"user","content":"x"}]}`)
}

func TestOpenAIStreaming(t *testing.T) {
	gen := &fakeGen{
		chunks: []core.StreamChunk{{Text: "To"}, {Text: "kyo"}},
		out:    core.GenerateOutput{Text: "Tokyo", StopReason: "end_turn", InputTokens: 3, OutputTokens: 2},
	}
	w := do(t, newChatServer(t, gen), "POST", "/v1/chat/completions",
		`{"model":"opus","stream":true,"stream_options":{"include_usage":true},
		 "messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != sseContentType {
		t.Errorf("Content-Type = %q, want %q", ct, sseContentType)
	}

	events := sseData(t, w.Body.String())
	if len(events) == 0 {
		t.Fatalf("no events:\n%s", w.Body)
	}
	if events[len(events)-1] != "[DONE]" {
		t.Errorf("last event = %q, want the [DONE] sentinel", events[len(events)-1])
	}

	var joined string
	var finish string
	var usage *oaiUsage
	for _, e := range events[:len(events)-1] {
		var c oaiChatResponse
		if err := json.Unmarshal([]byte(e), &c); err != nil {
			t.Fatalf("decode %q: %v", e, err)
		}
		if c.Object != "chat.completion.chunk" {
			t.Errorf("object = %q", c.Object)
		}
		d := c.Choices[0].Delta
		if d == nil {
			t.Fatalf("chunk has no delta: %s", e)
		}
		if text, ok := d.Content.(string); ok {
			joined += text
		}
		if c.Choices[0].FinishReason != nil {
			finish = *c.Choices[0].FinishReason
			usage = c.Usage
		}
	}
	if joined != "Tokyo" {
		t.Errorf("concatenated deltas = %q", joined)
	}
	if finish != finishStop {
		t.Errorf("finish_reason = %q", finish)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Errorf("usage = %+v, want it included on request", usage)
	}
}

func TestOpenAIStreamingOmitsUsageByDefault(t *testing.T) {
	gen := &fakeGen{chunks: []core.StreamChunk{{Text: "x"}}, out: core.GenerateOutput{Text: "x", StopReason: "end_turn"}}
	w := do(t, newChatServer(t, gen), "POST", "/v1/chat/completions",
		`{"model":"opus","stream":true,"messages":[{"role":"user","content":"x"}]}`)
	if strings.Contains(w.Body.String(), "total_tokens") {
		t.Errorf("usage was sent without include_usage:\n%s", w.Body)
	}
}

func TestOpenAIStreamingErrorGoesInBand(t *testing.T) {
	gen := &fakeGen{err: errors.New("claude exited 1")}
	w := do(t, newChatServer(t, gen), "POST", "/v1/chat/completions",
		`{"model":"opus","stream":true,"messages":[{"role":"user","content":"x"}]}`)
	body := w.Body.String()
	if !strings.Contains(body, "claude exited 1") {
		t.Errorf("body does not carry the error:\n%s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("stream was not terminated:\n%s", body)
	}
}

// The OpenAI SDKs parse a nested error object, not Ollama's flat one.
func TestOpenAIErrorShape(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/v1/chat/completions",
		`{"model":"llama3","messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
	var got struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Error.Message, "llama3") || got.Error.Type == "" {
		t.Errorf("error = %+v", got.Error)
	}
}

func TestOpenAIModels(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "GET", "/v1/models", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got struct {
		Object string     `json:"object"`
		Data   []oaiModel `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Object != "list" || len(got.Data) == 0 {
		t.Fatalf("got = %+v", got)
	}
	for _, m := range got.Data {
		if m.Object != "model" || m.ID == "" || m.OwnedBy == "" {
			t.Errorf("model = %+v", m)
		}
	}
}

func TestOpenAIModel(t *testing.T) {
	h := newChatServer(t, &fakeGen{})
	w := do(t, h, "GET", "/v1/models/opus", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	var got oaiModel
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "opus:latest" {
		t.Errorf("id = %q", got.ID)
	}

	if w := do(t, h, "GET", "/v1/models/llama3", ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown model status = %d", w.Code)
	}
}
