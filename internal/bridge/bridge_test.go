package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazufusa/oocla/internal/claudecli"
	"github.com/kazufusa/oocla/internal/core"
)

// stubBridge builds a Bridge backed by a shell script standing in for claude.
func stubBridge(t *testing.T, script string) *Bridge {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat >/dev/null\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &Bridge{Runner: &claudecli.Runner{Bin: path}}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

func input() core.GenerateInput {
	return core.GenerateInput{Model: "haiku", Prompt: core.Prompt{User: "hi"}}
}

// thinkingInput is a request that asked for the model's reasoning back.
func thinkingInput() core.GenerateInput {
	in := input()
	in.IncludeThinking = true
	return in
}

func TestGenerateCollectsAnswer(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"assistant","session_id":"sess-1","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Hello"},{"type":"text","text":", world"}]}}'
echo '{"type":"result","subtype":"success","session_id":"sess-1","result":"Hello, world","stop_reason":"end_turn","duration_ms":2000,"duration_api_ms":1500,"usage":{"input_tokens":12,"output_tokens":34}}'
`)
	out, err := b.Generate(context.Background(), thinkingInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "Hello, world" {
		t.Errorf("Text = %q", out.Text)
	}
	if out.Thinking != "hmm" {
		t.Errorf("Thinking = %q", out.Thinking)
	}
	if out.StopReason != "end_turn" {
		t.Errorf("StopReason = %q", out.StopReason)
	}
	if out.InputTokens != 12 || out.OutputTokens != 34 {
		t.Errorf("tokens = %d/%d", out.InputTokens, out.OutputTokens)
	}
	if out.Total != 2*time.Second || out.API != 1500*time.Millisecond {
		t.Errorf("durations = %v/%v", out.Total, out.API)
	}
}

// Ollama omits thinking unless the client asked for it.
func TestGenerateDropsThinkingUnlessAsked(t *testing.T) {
	script := `
echo '{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"42"}]}}'
echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'
`
	out, err := stubBridge(t, script).Generate(context.Background(), input(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Thinking != "" {
		t.Errorf("Thinking = %q, want it withheld", out.Thinking)
	}
	if out.Text != "42" {
		t.Errorf("Text = %q", out.Text)
	}
}

func TestGenerateDropsThinkingChunksUnlessAsked(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"42"}}}'
echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'
`)
	var chunks []core.StreamChunk
	if _, err := b.Generate(context.Background(), input(), func(c core.StreamChunk) error {
		chunks = append(chunks, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Text != "42" {
		t.Errorf("chunks = %+v, want only the answer", chunks)
	}
}

func TestGenerateStripsToolNamePrefix(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__oocla__get_weather","input":{"city":"Tokyo"}}]}}'
echo '{"type":"result","subtype":"success","stop_reason":"tool_use"}'
`)
	out, err := b.Generate(context.Background(), input(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
	fn := out.ToolCalls[0].Function
	if fn.Name != "get_weather" {
		t.Errorf("Name = %q, want the client-facing name", fn.Name)
	}
	if fn.Arguments["city"] != "Tokyo" {
		t.Errorf("Arguments = %+v", fn.Arguments)
	}
}

func TestGenerateReportsErrorResult(t *testing.T) {
	b := stubBridge(t, `echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"rate limited"}'`)
	_, err := b.Generate(context.Background(), input(), nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err = %v", err)
	}
}

// A failing turn explains itself in the result event and also exits non-zero.
// Reporting the exit status would throw the explanation away.
func TestGenerateErrorResultBeatsExitStatus(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"result","subtype":"success","is_error":true,"result":"Not logged in"}'
exit 1
`)
	_, err := b.Generate(context.Background(), input(), nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("err = %v, want the reason from the result event", err)
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Errorf("err = %v, want the exit status not to mask the reason", err)
	}
}

func TestGenerateRequiresResultEvent(t *testing.T) {
	b := stubBridge(t, `echo '{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}'`)
	if _, err := b.Generate(context.Background(), input(), nil); err == nil {
		t.Fatal("want an error when the stream ends without a result")
	}
}

// With --include-partial-messages the CLI sends deltas and then the assembled
// message. Counting both would double the answer.
func TestGenerateStreamingDoesNotDoubleCount(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}'
echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'
`)
	var chunks []core.StreamChunk
	out, err := b.Generate(context.Background(), input(), func(c core.StreamChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "Hello" {
		t.Errorf("Text = %q, want the answer counted once", out.Text)
	}
	var joined string
	for _, c := range chunks {
		joined += c.Text
	}
	if joined != "Hello" {
		t.Errorf("emitted chunks = %q", joined)
	}
}

func TestGenerateStreamingEmitsThinkingAndToolCalls(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__oocla__f","input":{"a":1}}]}}'
echo '{"type":"result","subtype":"success","stop_reason":"tool_use"}'
`)
	var chunks []core.StreamChunk
	if _, err := b.Generate(context.Background(), thinkingInput(), func(c core.StreamChunk) error {
		chunks = append(chunks, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %+v", chunks)
	}
	if chunks[0].Thinking != "hmm" {
		t.Errorf("first chunk = %+v", chunks[0])
	}
	if len(chunks[1].ToolCalls) != 1 || chunks[1].ToolCalls[0].Function.Name != "f" {
		t.Errorf("second chunk = %+v", chunks[1])
	}
}

// A failing emit means the client hung up; the turn must stop rather than run
// to completion against a dead connection.
func TestGenerateStopsWhenEmitFails(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"}}}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"b"}}}'
echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'
`)
	want := errors.New("client gone")
	calls := 0
	_, err := b.Generate(context.Background(), input(), func(core.StreamChunk) error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if calls != 1 {
		t.Errorf("emit called %d times, want 1", calls)
	}
}

// A tool call must end the turn: Ollama's client runs the tool, so the CLI must
// not be allowed to run it and answer from the result.
func TestGenerateStopsTheTurnOnAToolCall(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"looking it up"},{"type":"tool_use","id":"t1","name":"mcp__oocla__get_weather","input":{"city":"Tokyo"}}]}}'
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"the weather is fine"}]}}'
echo '{"type":"result","subtype":"success","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}'
sleep 5
`)
	out, err := b.Generate(context.Background(), input(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
	if out.StopReason != stopReasonToolUse {
		t.Errorf("StopReason = %q, want %q", out.StopReason, stopReasonToolUse)
	}
	if out.Text != "looking it up" {
		t.Errorf("Text = %q, want only what preceded the call", out.Text)
	}
}

// Several calls can share one message; stopping at the first must not drop the
// rest.
func TestGenerateKeepsSiblingToolCalls(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__oocla__a","input":{}},{"type":"tool_use","id":"t2","name":"mcp__oocla__b","input":{"x":1}}]}}'
sleep 5
`)
	out, err := b.Generate(context.Background(), input(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
	if out.ToolCalls[0].Function.Name != "a" || out.ToolCalls[1].Function.Name != "b" {
		t.Errorf("ToolCalls = %+v", out.ToolCalls)
	}
}

// The CLI is told about the tools through an MCP server that is oocla itself.
func TestGenerateConfiguresTheToolServer(t *testing.T) {
	b := stubBridge(t, `printf '%s\n' "$@" >&2; echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'`)
	b.Exe = "/opt/oocla"

	in := input()
	in.Tools = []core.Tool{{Function: core.ToolFunction{
		Name: "get_weather", Parameters: json.RawMessage(`{"type":"object"}`),
	}}}
	if _, err := b.Generate(context.Background(), in, nil); err != nil {
		t.Fatal(err)
	}
	// Nothing to assert on stderr here beyond the call succeeding; the argv
	// contents are covered by claudecli's own tests. What matters is that a
	// tool definition that cannot be represented is rejected rather than
	// silently dropped.
	in.Tools = []core.Tool{{Function: core.ToolFunction{Name: "bad name"}}}
	if _, err := b.Generate(context.Background(), in, nil); err == nil {
		t.Error("want an error for an unusable tool name")
	}
}

// --json-schema is implemented as a built-in StructuredOutput tool. Its call is
// the answer, not a tool call for the API client to run.
func TestGenerateStructuredOutputBecomesTheAnswer(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Here is what I found about Tokyo."}]}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{"city":"Tokyo","population":13960000}}]}}'
echo '{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":124},"delta":{"stop_reason":"tool_use"}}}'
sleep 5
`)
	in := input()
	in.JSONSchema = `{"type":"object"}`

	out, err := b.Generate(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %+v, want the structured call kept out of them", out.ToolCalls)
	}
	if out.Text != `{"city":"Tokyo","population":13960000}` {
		t.Errorf("Text = %q, want the structured answer alone", out.Text)
	}
	if out.StopReason == stopReasonToolUse {
		t.Errorf("StopReason = %q, want a normal completion", out.StopReason)
	}
}

// The prose leading up to a structured answer is not part of the answer, so it
// must not reach a streaming client either.
func TestGenerateStructuredOutputStreamsOnlyTheAnswer(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me look that up."}}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{"ok":true}}]}}'
echo '{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":124},"delta":{"stop_reason":"tool_use"}}}'
sleep 5
`)
	in := input()
	in.JSONSchema = `{"type":"object"}`

	var chunks []core.StreamChunk
	out, err := b.Generate(context.Background(), in, func(c core.StreamChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Text != `{"ok":true}` {
		t.Errorf("chunks = %+v, want only the structured answer", chunks)
	}
	if out.Text != `{"ok":true}` {
		t.Errorf("Text = %q", out.Text)
	}
}

// Without a schema there is no structured output, so a tool by that name is
// just a tool call.
func TestGenerateStructuredOutputToolIsOrdinaryWithoutASchema(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{"a":1}}]}}'
sleep 5
`)
	out, err := b.Generate(context.Background(), input(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
}

// fenced substitutes real backticks for FENCE, which cannot appear literally
// inside a Go raw string.
func fenced(script string) string {
	return strings.ReplaceAll(script, "FENCE", "```")
}

// In bare JSON mode the model is only instructed, not constrained, so it tends
// to wrap the answer in a Markdown fence that Ollama would never produce.
func TestGenerateJSONModeStripsCodeFence(t *testing.T) {
	b := stubBridge(t, fenced(`
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"FENCEjson\n[\"blue\", \"orange\"]\nFENCE"}]}}'
echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'
`))
	in := input()
	in.UnwrapJSON = true

	out, err := b.Generate(context.Background(), in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != `["blue", "orange"]` {
		t.Errorf("Text = %q, want the fence removed", out.Text)
	}
}

func TestGenerateJSONModeStreamsTheCleanedAnswerOnce(t *testing.T) {
	b := stubBridge(t, fenced(`
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"FENCEjson\n{\"a\":"}}}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"1}\nFENCE"}}}'
echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'
`))
	in := input()
	in.UnwrapJSON = true

	var chunks []core.StreamChunk
	out, err := b.Generate(context.Background(), in, func(c core.StreamChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Text != `{"a":1}` {
		t.Errorf("chunks = %+v, want one cleaned chunk", chunks)
	}
	if out.Text != `{"a":1}` {
		t.Errorf("Text = %q", out.Text)
	}
}

func TestGenerateReportsProcessFailure(t *testing.T) {
	b := stubBridge(t, `echo 'not authenticated' >&2; exit 1`)
	_, err := b.Generate(context.Background(), input(), nil)
	if err == nil {
		t.Fatal("want an error for a non-zero exit")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Errorf("err = %v, want stderr surfaced", err)
	}
}

// mcpAwareStub is a stub claude that reports no MCP servers when one was
// configured (a managed policy blocking it) and answers the fallback rerun,
// which is recognizable by its --json-schema and absent --mcp-config.
func mcpAwareStub(t *testing.T, fallbackScript string) *Bridge {
	t.Helper()
	return stubBridge(t, `
case "$*" in
*--mcp-config*)
  echo '{"type":"system","subtype":"init","session_id":"s","mcp_servers":[]}'
  sleep 5
  ;;
*)
`+fallbackScript+`
  echo '{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":124},"delta":{"stop_reason":"tool_use"}}}'
  sleep 5
  ;;
esac`)
}

func toolInput() core.GenerateInput {
	in := input()
	in.Tools = []core.Tool{{Type: "function", Function: core.ToolFunction{
		Name: "get_weather", Parameters: json.RawMessage(`{"type":"object"}`),
	}}}
	return in
}

// A managed policy can block oocla's MCP server. The turn must then fall back
// to prompting, and the client still gets tool calls.
func TestGenerateFallsBackToPromptToolsWhenMCPIsBlocked(t *testing.T) {
	b := mcpAwareStub(t, `
  echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{"content":"","tool_calls":[{"name":"get_weather","arguments":{"city":"Tokyo"}}]}}]}}'`)
	out, err := b.Generate(context.Background(), toolInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
	if out.ToolCalls[0].Function.Arguments["city"] != "Tokyo" {
		t.Errorf("Arguments = %+v", out.ToolCalls[0].Function.Arguments)
	}
	if out.StopReason != stopReasonToolUse {
		t.Errorf("StopReason = %q", out.StopReason)
	}
}

// The fallback envelope can also carry a plain answer when the model decides
// not to call any tool.
func TestGenerateFallbackPlainAnswer(t *testing.T) {
	b := mcpAwareStub(t, `
  echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{"content":"It is sunny.","tool_calls":[]}}]}}'`)
	out, err := b.Generate(context.Background(), toolInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %+v", out.ToolCalls)
	}
	if out.Text != "It is sunny." {
		t.Errorf("Text = %q", out.Text)
	}
	if out.StopReason != stopReasonEndTurn {
		t.Errorf("StopReason = %q", out.StopReason)
	}
}

// A streaming client gets the fallback's outcome as chunks.
func TestGenerateFallbackStreamsToolCalls(t *testing.T) {
	b := mcpAwareStub(t, `
  echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{"content":"","tool_calls":[{"name":"f","arguments":{}}]}}]}}'`)
	var chunks []core.StreamChunk
	out, err := b.Generate(context.Background(), toolInput(), func(c core.StreamChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
	if len(chunks) != 1 || len(chunks[0].ToolCalls) != 1 {
		t.Errorf("chunks = %+v, want the tool call emitted", chunks)
	}
}

// Tools plus a format constraint cannot share the single schema slot, so the
// combination fails loudly instead of answering without the tools.
func TestGenerateFallbackRefusesToolsWithFormat(t *testing.T) {
	b := mcpAwareStub(t, `
  echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'`)
	in := toolInput()
	in.JSONSchema = `{"type":"object"}`
	_, err := b.Generate(context.Background(), in, nil)
	if err == nil {
		t.Fatal("want an error for tools + format under a blocked MCP server")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("err = %v, want it to explain the conflict", err)
	}
}

// A connected MCP server must not trigger the fallback.
func TestGenerateNoFallbackWhenMCPIsConnected(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"system","subtype":"init","session_id":"s","mcp_servers":[{"name":"oocla","status":"connected"}]}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__oocla__get_weather","input":{"city":"Tokyo"}}]}}'
echo '{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":124},"delta":{"stop_reason":"tool_use"}}}'
sleep 5
`)
	out, err := b.Generate(context.Background(), toolInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCalls = %+v, want the MCP path used as is", out.ToolCalls)
	}
}

// With an admin-issued shim name, the MCP check and the prefix stripping both
// follow the configured name.
func TestGenerateHonorsCustomShimName(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"system","subtype":"init","session_id":"s","mcp_servers":[{"name":"corp-bridge","status":"connected"}]}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__corp-bridge__get_weather","input":{"city":"Tokyo"}}]}}'
echo '{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":124},"delta":{"stop_reason":"tool_use"}}}'
sleep 5
`)
	b.ShimName = "corp-bridge"
	out, err := b.Generate(context.Background(), toolInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCalls = %+v, want the custom prefix stripped", out.ToolCalls)
	}
}

// The default name being absent still triggers the fallback when a custom name
// was configured but not started.
func TestGenerateCustomShimNameStillDetectsBlocking(t *testing.T) {
	b := mcpAwareStub(t, `
  echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{"content":"ok","tool_calls":[]}}]}}'`)
	b.ShimName = "corp-bridge"
	out, err := b.Generate(context.Background(), toolInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "ok" {
		t.Errorf("Text = %q, want the fallback answer", out.Text)
	}
}

// PromptTools skips the MCP shim entirely: the CLI must never see an MCP
// config, and tools still round-trip through the prompt fallback.
func TestGeneratePromptToolsModeSkipsMCP(t *testing.T) {
	b := stubBridge(t, `
case "$*" in *--mcp-config*) echo 'mcp config passed' >&2; exit 1 ;; esac
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"StructuredOutput","input":{"content":"","tool_calls":[{"name":"get_weather","arguments":{"city":"Tokyo"}}]}}]}}'
echo '{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":124},"delta":{"stop_reason":"tool_use"}}}'
sleep 5
`)
	b.PromptTools = true
	out, err := b.Generate(context.Background(), toolInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
}

// Without tools the flag changes nothing.
func TestGeneratePromptToolsModeWithoutTools(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}'
echo '{"type":"result","subtype":"success","stop_reason":"end_turn"}'
`)
	b.PromptTools = true
	out, err := b.Generate(context.Background(), input(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "hi" {
		t.Errorf("Text = %q", out.Text)
	}
}

// Some models answer the fallback by emitting a plain tool_use for the
// prompted tool instead of filling the structured answer. That is still a
// valid request for the same call.
func TestGenerateFallbackAcceptsDirectToolUse(t *testing.T) {
	b := mcpAwareStub(t, `
  echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"get_weather","input":{"city":"Tokyo"}}]}}'`)
	out, err := b.Generate(context.Background(), toolInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
	if out.StopReason != stopReasonToolUse {
		t.Errorf("StopReason = %q", out.StopReason)
	}
}

// A turn cut short by a tool call still knows its input size from the
// assistant event's usage snapshot. The output count in the snapshot is an
// undercount, so it stays unreported.
func TestGenerateCutShortTurnReportsInputTokens(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"assistant","message":{"usage":{"input_tokens":433,"output_tokens":4},"content":[{"type":"tool_use","id":"t1","name":"mcp__oocla__get_weather","input":{"city":"Tokyo"}}]}}'
sleep 5
`)
	out, err := b.Generate(context.Background(), input(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.InputTokens != 433 {
		t.Errorf("InputTokens = %d, want the snapshot's input count", out.InputTokens)
	}
	if out.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want the undercounting snapshot withheld", out.OutputTokens)
	}
}

// In partial mode the turn is cut at message_delta rather than at tool_use,
// which is what makes the final output token count available.
func TestGenerateCutShortTurnGetsFinalOutputTokens(t *testing.T) {
	b := stubBridge(t, `
echo '{"type":"system","subtype":"init","session_id":"s","mcp_servers":[{"name":"oocla","status":"connected"}]}'
echo '{"type":"assistant","message":{"usage":{"input_tokens":433,"output_tokens":4},"content":[{"type":"tool_use","id":"t1","name":"mcp__oocla__get_weather","input":{"city":"Tokyo"}}]}}'
echo '{"type":"stream_event","event":{"type":"message_delta","usage":{"output_tokens":124},"delta":{"stop_reason":"tool_use"}}}'
sleep 5
`)
	out, err := b.Generate(context.Background(), toolInput(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", out.ToolCalls)
	}
	if out.InputTokens != 433 || out.OutputTokens != 124 {
		t.Errorf("tokens = %d/%d, want 433/124", out.InputTokens, out.OutputTokens)
	}
}
