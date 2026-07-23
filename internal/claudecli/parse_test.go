package claudecli

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// collect drains a decoder.
func collect(t *testing.T, r io.Reader) []Event {
	t.Helper()
	d := NewDecoder(r)
	var out []Event
	for {
		e, err := d.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, e)
	}
}

// TestDecodeCapturedSession runs the decoder over real `claude` output.
func TestDecodeCapturedSession(t *testing.T) {
	f, err := os.Open("testdata/simple.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	events := collect(t, f)

	if len(events) == 0 {
		t.Fatal("no events decoded")
	}
	if events[0].Kind != KindInit {
		t.Fatalf("first event kind = %q, want %q", events[0].Kind, KindInit)
	}
	if events[0].SessionID == "" {
		t.Error("init event has no session id")
	}

	var text, thinking int
	for _, e := range events {
		switch e.Kind {
		case KindText:
			text++
		case KindThinking:
			thinking++
		}
	}
	if text == 0 {
		t.Error("no text events decoded")
	}
	if thinking == 0 {
		t.Error("no thinking events decoded")
	}

	last := events[len(events)-1]
	if last.Kind != KindResult {
		t.Fatalf("last event kind = %q, want %q", last.Kind, KindResult)
	}
	res := last.Result
	if res.Text != "Hello there, friend!" {
		t.Errorf("result text = %q", res.Text)
	}
	if res.StopReason != "end_turn" {
		t.Errorf("stop reason = %q", res.StopReason)
	}
	if res.IsError {
		t.Error("result marked as error")
	}
	if res.Usage.InputTokens == 0 || res.Usage.OutputTokens == 0 {
		t.Errorf("usage not decoded: %+v", res.Usage)
	}
}

func TestDecodeToolUse(t *testing.T) {
	line := `{"type":"assistant","session_id":"s1","message":{"content":[` +
		`{"type":"text","text":"checking"},` +
		`{"type":"tool_use","id":"toolu_1","name":"mcp__oocla__get_weather","input":{"city":"Tokyo"}}` +
		`]}}`
	events := collect(t, strings.NewReader(line))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(events), events)
	}
	if events[0].Kind != KindText || events[0].Text != "checking" {
		t.Errorf("first event = %+v", events[0])
	}
	tu := events[1].ToolUse
	if events[1].Kind != KindToolUse || tu == nil {
		t.Fatalf("second event = %+v", events[1])
	}
	if tu.ID != "toolu_1" || tu.Name != "mcp__oocla__get_weather" {
		t.Errorf("tool use = %+v", tu)
	}
	if string(tu.Input) != `{"city":"Tokyo"}` {
		t.Errorf("tool input = %s", tu.Input)
	}
}

func TestDecodePartialMessageDeltas(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"stream_event","session_id":"s1","event":{"type":"message_start"}}`,
		`{"type":"stream_event","session_id":"s1","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}}`,
		`{"type":"stream_event","session_id":"s1","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}}`,
		`{"type":"stream_event","session_id":"s1","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc"}}}`,
		`{"type":"stream_event","session_id":"s1","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"1\n2"}}}`,
		`{"type":"stream_event","session_id":"s1","event":{"type":"message_stop"}}`,
	}, "\n")

	var got []Event
	for _, e := range collect(t, strings.NewReader(in)) {
		if e.Kind != KindOther {
			got = append(got, e)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d interesting events, want 2: %+v", len(got), got)
	}
	if got[0].Kind != KindThinkingDelta || got[0].Text != "hmm" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Kind != KindTextDelta || got[1].Text != "1\n2" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestDecodeErrorResult(t *testing.T) {
	line := `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom","session_id":"s1"}`
	events := collect(t, strings.NewReader(line))
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	res := events[0].Result
	if res == nil || !res.IsError || res.Text != "boom" || res.Subtype != "error_during_execution" {
		t.Errorf("result = %+v", res)
	}
}

func TestDecodeUnknownTypeBecomesOther(t *testing.T) {
	events := collect(t, strings.NewReader(`{"type":"rate_limit_event","session_id":"s1"}`))
	if len(events) != 1 || events[0].Kind != KindOther {
		t.Fatalf("events = %+v", events)
	}
	if len(events[0].Raw) == 0 {
		t.Error("raw line not preserved")
	}
}

func TestDecodeSkipsBlankLines(t *testing.T) {
	in := "\n\n" + `{"type":"result","result":"ok"}` + "\n\n"
	events := collect(t, strings.NewReader(in))
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
}

func TestDecodeHandlesFinalLineWithoutNewline(t *testing.T) {
	events := collect(t, strings.NewReader(`{"type":"result","result":"ok"}`))
	if len(events) != 1 || events[0].Result.Text != "ok" {
		t.Fatalf("events = %+v", events)
	}
}

// A thinking block carries a multi-kilobyte signature; a fixed scanner buffer
// would truncate the line and corrupt the stream.
func TestDecodeHandlesVeryLongLines(t *testing.T) {
	long := strings.Repeat("x", 512*1024)
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + long + `"}]}}`
	events := collect(t, strings.NewReader(line))
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	if len(events[0].Text) != len(long) {
		t.Errorf("text length = %d, want %d", len(events[0].Text), len(long))
	}
}

func TestDecodeMalformedLineIsAnError(t *testing.T) {
	d := NewDecoder(strings.NewReader("{not json}\n"))
	if _, err := d.Next(); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want a decode error", err)
	}
}
