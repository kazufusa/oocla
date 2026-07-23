package core

import "encoding/json"

// Role values used by the chat dialects.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCallFunction is the callee of a tool call.
type ToolCallFunction struct {
	Index     int            `json:"index,omitempty"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolCall is one tool invocation requested by the model. oocla never runs
// tools itself: it returns them and the client executes them.
type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

// ToolFunction is a JSON tool definition supplied by the client.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Tool is one entry of a request's tools array. The JSON shape is OpenAI's
// original design, which Ollama adopted verbatim, so both dialects decode
// into it directly.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// Message is one chat message in the neutral conversation model.
//
// The JSON encoding is Ollama's wire shape, which happens to be identical to
// the neutral model, so the Ollama dialect uses this type directly. The
// OpenAI dialect has its own wire type and converts.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	Images    []string   `json:"images,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}
