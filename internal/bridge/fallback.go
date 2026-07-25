package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kazufusa/oocla/internal/core"
)

// This file is the prompt fallback for tools: environments with a managed
// policy (e.g. allowManagedMcpServersOnly) refuse to start oocla's MCP tool
// server, and without it the model never sees the request's tools. The
// fallback folds the tool definitions into the system prompt and constrains
// the answer with --json-schema to an envelope that either carries tool calls
// or a plain answer. Less robust than MCP, but honest: the client still gets
// tool_calls instead of silently degraded prose.

// toolEnvelopeSchema shapes the fallback answer: tool calls to make, or an
// empty list and the answer as content.
const toolEnvelopeSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["tool_calls"],
  "properties": {
    "content": {
      "type": "string",
      "description": "Your answer to the user. Use this when no new tool call is needed, e.g. when the conversation already contains the tool results you need."
    },
    "tool_calls": {
      "type": "array",
      "description": "New tool calls you want the caller to execute now. Not a record of past calls: leave this empty when answering from results already present in the conversation.",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["name", "arguments"],
        "properties": {
          "name": {"type": "string"},
          "arguments": {"type": "object"}
        }
      }
    }
  }
}`

// toolEnvelope is the answer shape toolEnvelopeSchema enforces.
type toolEnvelope struct {
	Content   string `json:"content"`
	ToolCalls []struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"tool_calls"`
}

// generateWithPromptTools reruns a turn whose MCP tool server was blocked,
// carrying the tools in the prompt instead.
func (b *Bridge) generateWithPromptTools(ctx context.Context, in core.GenerateInput, emit func(core.StreamChunk) error) (core.GenerateOutput, error) {
	if in.JSONSchema != "" || in.UnwrapJSON {
		// The schema slot is the only structured channel, and the fallback
		// needs it for the tool envelope. Refusing beats answering without the
		// tools the client asked for.
		return core.GenerateOutput{}, errors.New(
			"bridge: this environment blocks oocla's MCP tool server, and the prompt fallback cannot combine tools with format constraints; drop tools or format")
	}

	directive, err := toolDirective(in.Tools)
	if err != nil {
		return core.GenerateOutput{}, err
	}
	fb := in
	fb.Tools = nil
	fb.JSONSchema = toolEnvelopeSchema
	fb.Prompt.System = joinPrompt(in.Prompt.System, directive)

	// No emit: the structured answer is only useful once complete, so it is
	// parsed first and streamed as whole chunks after.
	raw, err := b.Generate(ctx, fb, nil)
	if err != nil {
		return core.GenerateOutput{}, err
	}
	out, err := fallbackAnswer(raw)
	if err != nil {
		return core.GenerateOutput{}, err
	}
	if emit == nil {
		return out, nil
	}
	chunk := core.StreamChunk{Text: out.Text, Thinking: out.Thinking, ToolCalls: out.ToolCalls}
	if chunk.Text == "" && chunk.Thinking == "" && len(chunk.ToolCalls) == 0 {
		return out, nil
	}
	return out, emit(chunk)
}

// toolDirective renders the tool definitions as the system prompt addition
// the fallback runs with.
func toolDirective(tools []core.Tool) (string, error) {
	defs, err := json.Marshal(tools)
	if err != nil {
		return "", fmt.Errorf("bridge: encoding tools for the prompt fallback: %w", err)
	}
	return "You have access to the tools defined below. You do not execute them; " +
		"the caller does. To use tools, list the calls to make in tool_calls. " +
		"Otherwise leave tool_calls empty and answer in content.\n\n" +
		"Tools:\n" + string(defs), nil
}

// joinPrompt appends an addition to a possibly empty system prompt.
func joinPrompt(system, addition string) string {
	if system == "" {
		return addition
	}
	return strings.TrimRight(system, "\n") + "\n\n" + addition
}

// fallbackAnswer converts the fallback turn's outcome back into the shape the
// rest of oocla expects.
//
// Models answer this setup in one of two ways, measured per model: most fill
// the structured answer's tool_calls, but some emit a plain tool_use block
// for the prompted tool name instead, which the CLI passes through. Both are
// valid requests for the same calls, so both are accepted.
func fallbackAnswer(raw core.GenerateOutput) (core.GenerateOutput, error) {
	if len(raw.ToolCalls) > 0 {
		out := raw
		out.Text = ""
		out.StopReason = stopReasonToolUse
		return out, nil
	}
	var env toolEnvelope
	if err := json.Unmarshal([]byte(raw.Text), &env); err != nil {
		return core.GenerateOutput{}, fmt.Errorf("bridge: prompt fallback returned an unusable answer: %w", err)
	}
	out := raw
	out.Text = env.Content
	out.ToolCalls = nil
	for _, c := range env.ToolCalls {
		args := c.Arguments
		if args == nil {
			args = map[string]any{}
		}
		out.ToolCalls = append(out.ToolCalls, core.ToolCall{Function: core.ToolCallFunction{
			Name: c.Name, Arguments: args,
		}})
	}
	if len(out.ToolCalls) > 0 {
		out.Text = ""
		out.StopReason = stopReasonToolUse
	} else {
		out.StopReason = stopReasonEndTurn
	}
	return out, nil
}
