package core

import (
	"context"
	"time"
)

// Prompt is a conversation reduced to what the claude CLI accepts for a single
// turn: a system prompt and one user message.
type Prompt struct {
	System string
	User   string
}

// GenerateInput is one backend turn: everything the claude CLI needs for a
// single invocation.
type GenerateInput struct {
	// Model is the claude CLI model name, not the client-facing one.
	Model  string
	Prompt Prompt
	Tools  []Tool
	// JSONSchema constrains the answer when the client asked for structured
	// output.
	JSONSchema string
	// UnwrapJSON marks bare "json" mode, where the answer must be JSON and
	// nothing else. The backend holds the text back until it is complete so it
	// can be cleaned up before the client sees any of it.
	UnwrapJSON bool
	// IncludeThinking asks for the model's reasoning to be reported. The model
	// reasons either way; this only decides whether the client sees it.
	IncludeThinking bool
	// Effort is the reasoning effort level, empty for the model's default.
	Effort string
}

// GenerateOutput is the result of one backend turn.
type GenerateOutput struct {
	Text      string
	Thinking  string
	ToolCalls []ToolCall

	// StopReason is Claude's stop reason, e.g. "end_turn" or "max_tokens".
	StopReason string

	InputTokens  int
	OutputTokens int
	Total        time.Duration
	API          time.Duration
}

// StreamChunk is an incremental piece of an answer.
type StreamChunk struct {
	Text      string
	Thinking  string
	ToolCalls []ToolCall
}

// Generator runs one turn against the backend. The HTTP layer depends on this
// interface rather than on the CLI, so it can be tested without spawning
// processes.
//
// emit, when non-nil, is called for each incremental piece as it arrives, and
// asking for chunks is what turns token-level streaming on. A non-nil error
// from emit aborts the turn: it means the client hung up. The returned
// GenerateOutput always holds the complete answer, whether or not chunks were
// requested.
type Generator interface {
	Generate(ctx context.Context, in GenerateInput, emit func(StreamChunk) error) (GenerateOutput, error)
}
