// Package ollama serves the Ollama dialect: every endpoint under /api/*,
// speaking Ollama's wire shapes. Requests are converted into the neutral
// core types and run through core.Engine; nothing OpenAI-shaped lives here.
package ollama

import (
	"encoding/json"
	"time"

	"github.com/kazufusa/oocla/internal/core"
)

// The Ollama wire format for the conversation model is identical to the
// neutral one, so the core types are used directly under this package's
// names. The OpenAI dialect is the one that has to convert.
type (
	Message          = core.Message
	Tool             = core.Tool
	ToolCall         = core.ToolCall
	ToolCallFunction = core.ToolCallFunction
	ToolFunction     = core.ToolFunction
)

// Role values used by the Ollama chat API.
const (
	RoleSystem    = core.RoleSystem
	RoleUser      = core.RoleUser
	RoleAssistant = core.RoleAssistant
	RoleTool      = core.RoleTool
)

// ChatRequest is the body of POST /api/chat.
type ChatRequest struct {
	Model    string          `json:"model"`
	Messages []Message       `json:"messages"`
	Stream   *bool           `json:"stream"`
	Format   json.RawMessage `json:"format"`
	Options  map[string]any  `json:"options"`
	Tools    []Tool          `json:"tools"`
	Think    json.RawMessage `json:"think"`
	// KeepAlive cannot keep anything resident; it only decides whether an
	// empty conversation is acknowledged as a load or an unload.
	KeepAlive json.RawMessage `json:"keep_alive"`
}

// Streaming reports whether the client wants a streamed response. Ollama
// streams unless the client explicitly opts out.
func (r ChatRequest) Streaming() bool { return r.Stream == nil || *r.Stream }

// ChatChunk is one streamed content message of POST /api/chat, and also the
// acknowledgement of a load request, which Ollama sends without statistics
// fields. The statistics belong to the terminator alone.
type ChatChunk struct {
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"created_at"`
	Message    Message   `json:"message"`
	Done       bool      `json:"done"`
	DoneReason string    `json:"done_reason,omitempty"`
}

// ChatResponse ends POST /api/chat: the non-streaming answer, or the last
// line of a stream. The statistics are always written, zeros included: on a
// real Ollama they are never zero, so clients read them without a presence
// check and a dropped field becomes None-arithmetic on their side. Durations
// are nanoseconds, matching Ollama.
//
// Deliberately not ChatChunk plus statistics: embedding would put the chunk's
// omitempty tags on the terminator, and which fields a client may rely on is
// exactly what these types exist to state.
type ChatResponse struct {
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"created_at"`
	Message    Message   `json:"message"`
	Done       bool      `json:"done"`
	DoneReason string    `json:"done_reason"`

	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
	PromptEvalCount    int   `json:"prompt_eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalCount          int   `json:"eval_count"`
	EvalDuration       int64 `json:"eval_duration"`
}

// Done reasons reported by Ollama. Load and unload mark the reply to an
// empty request (no messages, or no prompt), which Ollama treats as a request
// to load — or with keep_alive 0, unload — the model rather than to generate
// anything. Nothing is ever resident here, so both acknowledge a no-op.
const (
	DoneReasonStop   = "stop"
	DoneReasonLength = "length"
	DoneReasonLoad   = "load"
	DoneReasonUnload = "unload"
)

// doneReasonFor maps a Claude stop reason onto Ollama's vocabulary.
func doneReasonFor(stopReason string) string {
	switch stopReason {
	case "max_tokens":
		return DoneReasonLength
	default:
		return DoneReasonStop
	}
}

// loadDoneReason picks the acknowledgement for an empty request: keep_alive 0
// asks for an unload, anything else for a load. Ollama reads a bare number as
// seconds and a string as a Go duration.
func loadDoneReason(keepAlive json.RawMessage) string {
	var n float64
	if json.Unmarshal(keepAlive, &n) == nil {
		if n == 0 {
			return DoneReasonUnload
		}
		return DoneReasonLoad
	}
	var s string
	if json.Unmarshal(keepAlive, &s) == nil {
		if d, err := time.ParseDuration(s); err == nil && d == 0 {
			return DoneReasonUnload
		}
	}
	return DoneReasonLoad
}
