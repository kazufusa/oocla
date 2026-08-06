package claudecli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Kind classifies a decoded stream-json event.
type Kind string

const (
	// KindInit is the session announcement; SessionID is set.
	KindInit Kind = "init"
	// KindText is an assistant text block.
	KindText Kind = "text"
	// KindThinking is an assistant thinking block.
	KindThinking Kind = "thinking"
	// KindTextDelta is a token-level piece of an assistant text block. It only
	// appears when Options.Partial is set.
	KindTextDelta Kind = "text_delta"
	// KindThinkingDelta is a token-level piece of a thinking block.
	KindThinkingDelta Kind = "thinking_delta"
	// KindToolUse is an assistant tool call.
	KindToolUse Kind = "tool_use"
	// KindMessageDelta closes a message in partial mode and carries the
	// message's final usage.
	KindMessageDelta Kind = "message_delta"
	// KindMessageStop ends a message in partial mode.
	KindMessageStop Kind = "message_stop"
	// KindResult ends a turn.
	KindResult Kind = "result"
	// KindOther is an event oocla does not interpret, kept for logging.
	KindOther Kind = "other"
)

// ToolUse is one tool call emitted by the model.
type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Usage is the token accounting for a turn.
type Usage struct {
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

// Result is the terminating event of a turn.
type Result struct {
	Subtype       string
	Text          string
	StopReason    string
	IsError       bool
	NumTurns      int
	DurationMS    int64
	DurationAPIMS int64
	TTFTMS        int64
	TotalCostUSD  float64
	Usage         Usage
}

// Event is one decoded item of the CLI's stream-json output.
type Event struct {
	Kind      Kind
	SessionID string
	// Text carries the block body for KindText and KindThinking.
	Text    string
	ToolUse *ToolUse
	Result  *Result
	// Model is the exact model id the session runs, set for KindInit. The CLI
	// resolves aliases like "opus" locally, so this is known before any API
	// call is made.
	Model string
	// MCPServers lists the connected MCP servers, set for KindInit. A server
	// that was configured but is absent here was not started, e.g. blocked by
	// a managed policy.
	MCPServers []string
	// Usage is the usage snapshot an assistant event carries. InputTokens is
	// the request's real size; OutputTokens is only what had been generated
	// when the snapshot was taken, so it undercounts.
	Usage *Usage
	// Raw is the undecoded line, set for KindOther.
	Raw json.RawMessage
}

// Decoder reads the CLI's newline-delimited JSON output.
//
// One output line can carry several content blocks, so the decoder keeps a
// queue and hands them out one Event at a time.
type Decoder struct {
	r       *bufio.Reader
	pending []Event
}

// NewDecoder reads stream-json events from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReader(r)}
}

// Next returns the next event, or io.EOF when the stream ends.
//
// Lines are not length-limited: thinking blocks carry multi-kilobyte
// signatures, so a fixed scanner buffer would truncate them.
func (d *Decoder) Next() (Event, error) {
	for {
		if len(d.pending) > 0 {
			e := d.pending[0]
			d.pending = d.pending[1:]
			return e, nil
		}
		line, err := d.readLine()
		if err != nil {
			return Event{}, err
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		events, err := decodeLine([]byte(line))
		if err != nil {
			return Event{}, err
		}
		d.pending = events
	}
}

// Drain returns the events already decoded from the last line read but not yet
// handed out, and clears them. It never blocks on the underlying reader.
//
// One output line can hold several content blocks, so a caller that stops at
// the first interesting event would otherwise silently drop its siblings.
func (d *Decoder) Drain() []Event {
	pending := d.pending
	d.pending = nil
	return pending
}

func (d *Decoder) readLine() (string, error) {
	var sb strings.Builder
	for {
		chunk, err := d.r.ReadString('\n')
		sb.WriteString(chunk)
		if err == nil {
			return sb.String(), nil
		}
		if err == io.EOF && sb.Len() > 0 {
			return sb.String(), nil
		}
		return "", err
	}
}

// envelope is the shared shape of every stream-json line.
type envelope struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`

	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`

	Message struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`

	// Event carries the Anthropic streaming event when --include-partial-messages
	// is on. Its shape is the API's own, not the CLI's.
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"event"`

	Result        string  `json:"result"`
	StopReason    string  `json:"stop_reason"`
	IsError       bool    `json:"is_error"`
	NumTurns      int     `json:"num_turns"`
	DurationMS    int64   `json:"duration_ms"`
	DurationAPIMS int64   `json:"duration_api_ms"`
	TTFTMS        int64   `json:"ttft_ms"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	Usage         struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func decodeLine(line []byte) ([]Event, error) {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, fmt.Errorf("claudecli: cannot decode event: %w", err)
	}
	switch env.Type {
	case "system":
		if env.Subtype == "init" {
			var connected []string
			for _, s := range env.MCPServers {
				if s.Status == "connected" {
					connected = append(connected, s.Name)
				}
			}
			return []Event{{Kind: KindInit, SessionID: env.SessionID, MCPServers: connected, Model: env.Model}}, nil
		}
	case "assistant":
		return assistantEvents(env), nil
	case "stream_event":
		if e, ok := deltaEvent(env); ok {
			return []Event{e}, nil
		}
	case "result":
		return []Event{{
			Kind:      KindResult,
			SessionID: env.SessionID,
			Result: &Result{
				Subtype:       env.Subtype,
				Text:          env.Result,
				StopReason:    env.StopReason,
				IsError:       env.IsError,
				NumTurns:      env.NumTurns,
				DurationMS:    env.DurationMS,
				DurationAPIMS: env.DurationAPIMS,
				TTFTMS:        env.TTFTMS,
				TotalCostUSD:  env.TotalCostUSD,
				Usage: Usage{
					InputTokens:              env.Usage.InputTokens,
					OutputTokens:             env.Usage.OutputTokens,
					CacheReadInputTokens:     env.Usage.CacheReadInputTokens,
					CacheCreationInputTokens: env.Usage.CacheCreationInputTokens,
				},
			},
		}}, nil
	}
	return []Event{{Kind: KindOther, SessionID: env.SessionID, Raw: json.RawMessage(line)}}, nil
}

// deltaEvent extracts a token-level delta or a message boundary. Block starts
// and stops and signature deltas carry nothing oocla needs, so they fall
// through to KindOther.
func deltaEvent(env envelope) (Event, bool) {
	switch env.Event.Type {
	case "content_block_delta":
		switch env.Event.Delta.Type {
		case "text_delta":
			return Event{Kind: KindTextDelta, SessionID: env.SessionID, Text: env.Event.Delta.Text}, true
		case "thinking_delta":
			return Event{Kind: KindThinkingDelta, SessionID: env.SessionID, Text: env.Event.Delta.Thinking}, true
		}
	case "message_delta":
		return Event{Kind: KindMessageDelta, SessionID: env.SessionID, Usage: &Usage{
			InputTokens:  env.Event.Usage.InputTokens,
			OutputTokens: env.Event.Usage.OutputTokens,
		}}, true
	case "message_stop":
		return Event{Kind: KindMessageStop, SessionID: env.SessionID}, true
	}
	return Event{}, false
}

func assistantEvents(env envelope) []Event {
	var usage *Usage
	if env.Message.Usage.InputTokens > 0 {
		usage = &Usage{
			InputTokens:  env.Message.Usage.InputTokens,
			OutputTokens: env.Message.Usage.OutputTokens,
		}
	}
	out := make([]Event, 0, len(env.Message.Content))
	for _, b := range env.Message.Content {
		switch b.Type {
		case "text":
			out = append(out, Event{Kind: KindText, SessionID: env.SessionID, Text: b.Text, Usage: usage})
		case "thinking":
			out = append(out, Event{Kind: KindThinking, SessionID: env.SessionID, Text: b.Thinking, Usage: usage})
		case "tool_use":
			out = append(out, Event{Kind: KindToolUse, SessionID: env.SessionID, ToolUse: &ToolUse{
				ID: b.ID, Name: b.Name, Input: b.Input,
			}, Usage: usage})
		}
	}
	return out
}
