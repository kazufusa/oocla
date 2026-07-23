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
	// KeepAlive is accepted and ignored: there is no model to keep resident.
	KeepAlive json.RawMessage `json:"keep_alive"`
}

// Streaming reports whether the client wants a streamed response. Ollama
// streams unless the client explicitly opts out.
func (r ChatRequest) Streaming() bool { return r.Stream == nil || *r.Stream }

// ChatResponse is one message of POST /api/chat. Durations are nanoseconds,
// matching Ollama.
type ChatResponse struct {
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"created_at"`
	Message    Message   `json:"message"`
	Done       bool      `json:"done"`
	DoneReason string    `json:"done_reason,omitempty"`

	TotalDuration      int64 `json:"total_duration,omitempty"`
	LoadDuration       int64 `json:"load_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}

// Done reasons reported by Ollama.
const (
	DoneReasonStop   = "stop"
	DoneReasonLength = "length"
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
