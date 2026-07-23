package ollama

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kazufusa/oocla/internal/core"
)

func TestParseOptionsStop(t *testing.T) {
	cases := map[string]struct {
		in   map[string]any
		want []string
	}{
		"absent":        {nil, nil},
		"single":        {map[string]any{"stop": "END"}, []string{"END"}},
		"list":          {map[string]any{"stop": []any{"END", "\n\n"}}, []string{"END", "\n\n"}},
		"empty string":  {map[string]any{"stop": ""}, nil},
		"null":          {map[string]any{"stop": nil}, nil},
		"drops empties": {map[string]any{"stop": []any{"", "END"}}, []string{"END"}},
	}
	for name, c := range cases {
		got, _, err := ParseOptions(c.in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !slices.Equal(got.Stop, c.want) {
			t.Errorf("%s: Stop = %q, want %q", name, got.Stop, c.want)
		}
	}
}

func TestParseOptionsRejectsBadStop(t *testing.T) {
	for _, in := range []map[string]any{
		{"stop": 3},
		{"stop": []any{"a", 4}},
	} {
		if _, _, err := ParseOptions(in); err == nil {
			t.Errorf("ParseOptions(%v): want an error", in)
		}
	}
}

// Ollama clients send a full options block whether or not they care about it,
// so unsupported keys are reported and dropped rather than refused.
func TestParseOptionsReportsIgnoredKeys(t *testing.T) {
	_, ignored, err := ParseOptions(map[string]any{
		"temperature": 0.7, "num_ctx": 4096, "seed": 42, "stop": "END",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"num_ctx", "seed", "temperature"}
	if !slices.Equal(ignored, want) {
		t.Errorf("ignored = %v, want %v (sorted, stop excluded)", ignored, want)
	}
}

func TestChatLogsIgnoredOptions(t *testing.T) {
	var buf bytes.Buffer
	eng := core.NewEngine(core.NewRegistry(testTime()), &fakeGen{out: core.GenerateOutput{Text: "x", StopReason: "end_turn"}})
	eng.NowFunc = testTime
	eng.Logger = slog.New(slog.NewTextHandler(&buf, nil))

	do(t, NewServer(eng), "POST", "/api/chat", `{"model":"opus","stream":false,
		"options":{"temperature":0.7,"num_ctx":4096},
		"messages":[{"role":"user","content":"x"}]}`)

	logged := buf.String()
	for _, want := range []string{"temperature", "num_ctx"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log does not mention %q:\n%s", want, logged)
		}
	}
}

func TestChatDoesNotLogSupportedOptions(t *testing.T) {
	var buf bytes.Buffer
	eng := core.NewEngine(core.NewRegistry(testTime()), &fakeGen{out: core.GenerateOutput{Text: "x", StopReason: "end_turn"}})
	eng.NowFunc = testTime
	eng.Logger = slog.New(slog.NewTextHandler(&buf, nil))

	do(t, NewServer(eng), "POST", "/api/chat",
		`{"model":"opus","stream":false,"options":{"stop":["END"]},"messages":[{"role":"user","content":"x"}]}`)
	if buf.Len() != 0 {
		t.Errorf("logged for a supported option:\n%s", buf.String())
	}
}

func TestChatRejectsBadOptions(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/api/chat",
		`{"model":"opus","stream":false,"options":{"stop":3},"messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestChatTruncatesAtStop(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "keep this\nEND\nand drop this", StopReason: "end_turn"}}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","stream":false,"options":{"stop":["END"]},"messages":[{"role":"user","content":"x"}]}`)
	var got ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Message.Content != "keep this\n" {
		t.Errorf("content = %q", got.Message.Content)
	}
}

func TestChatStreamStopsAtStopSequence(t *testing.T) {
	gen := &fakeGen{
		chunks: []core.StreamChunk{{Text: "keep "}, {Text: "this EN"}, {Text: "D and drop this"}, {Text: "never"}},
		out:    core.GenerateOutput{Text: "keep this END and drop this never", StopReason: "end_turn"},
	}
	w := do(t, newChatServer(t, gen), "POST", "/api/chat",
		`{"model":"opus","options":{"stop":["END"]},"messages":[{"role":"user","content":"x"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}

	lines := streamLines(t, w.Body.String())
	var joined string
	for _, l := range lines {
		joined += l.Message.Content
	}
	if joined != "keep this " {
		t.Errorf("streamed content = %q, want the text before the stop sequence", joined)
	}
	last := lines[len(lines)-1]
	if !last.Done {
		t.Errorf("stream did not terminate: %+v", last)
	}
}

func TestGenerateTruncatesAtStop(t *testing.T) {
	gen := &fakeGen{out: core.GenerateOutput{Text: "answer<|end|>trailing", StopReason: "end_turn"}}
	w := do(t, newChatServer(t, gen), "POST", "/api/generate",
		`{"model":"opus","stream":false,"options":{"stop":["<|end|>"]},"prompt":"x"}`)
	var got GenerateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Response != "answer" {
		t.Errorf("response = %q", got.Response)
	}
}
