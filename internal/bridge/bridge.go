// Package bridge implements core.Generator on top of the claude CLI.
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kazufusa/oocla/internal/claudecli"
	"github.com/kazufusa/oocla/internal/core"
	"github.com/kazufusa/oocla/internal/mcpshim"
)

// stopReasonToolUse is reported when the turn ended because the model asked to
// call a tool. The CLI never gets to emit its own result event in that case:
// oocla stops the turn first, so that the call reaches the API client instead
// of being answered locally.
const stopReasonToolUse = "tool_use"

// stopReasonEndTurn is what a completed turn reports.
const stopReasonEndTurn = "end_turn"

// structuredOutputTool is the tool the CLI adds when --json-schema is set. The
// constrained answer arrives as a call to it rather than as text, so it is the
// answer, not a tool call to hand to the API client.
const structuredOutputTool = "StructuredOutput"

// errMCPBlocked reports that the CLI started without the MCP tool server oocla
// configured, e.g. under a managed policy that allowlists MCP servers. Without
// it the model cannot see the request's tools, and the turn would silently
// come back as prose.
var errMCPBlocked = errors.New("bridge: the claude CLI started without oocla's MCP tool server; a managed policy may be blocking it")

// Bridge turns one generation request into one claude CLI invocation.
type Bridge struct {
	Runner *claudecli.Runner

	// Exe is the oocla binary the CLI should spawn as its MCP tool server.
	// It defaults to the running executable.
	Exe string

	// Bare runs the CLI in its minimal mode, which removes the last of the
	// context it injects but restricts it to API key authentication. See
	// claudecli.Options.Bare.
	Bare bool

	// ShimName overrides the MCP server name the tool shim registers under.
	// Empty means the default, mcpshim.ServerName. Exists for managed
	// environments whose admin allowlists MCP servers under an issued name.
	ShimName string

	// PromptTools skips the MCP shim entirely and always carries tools in the
	// prompt. For debugging the fallback, and for environments where the shim
	// is known to be blocked, where it saves the aborted detection call every
	// tools request would otherwise pay.
	PromptTools bool
}

// New returns a Bridge driving the claude binary on PATH.
func New() *Bridge { return &Bridge{Runner: claudecli.NewRunner()} }

// Close releases the runner's scratch directory.
func (b *Bridge) Close() error { return b.Runner.Cleanup() }

func (b *Bridge) exe() (string, error) {
	if b.Exe != "" {
		return b.Exe, nil
	}
	return os.Executable()
}

// shimName returns the MCP server name the tool shim registers under.
func (b *Bridge) shimName() string {
	if b.ShimName != "" {
		return b.ShimName
	}
	return mcpshim.ServerName
}

// ProbeShim reports whether this environment lets the tool shim start, so a
// blocked shim can be announced once at startup instead of surprising the
// first tools request. The per-request detection stays authoritative; this is
// advance notice.
func (b *Bridge) ProbeShim(ctx context.Context) (bool, error) {
	exe, err := b.exe()
	if err != nil {
		return false, err
	}
	return b.Runner.ProbeMCPName(ctx, b.shimName(), exe)
}

// ProbeModel reports the exact model id an alias resolves to. The CLI answers
// from its own tables before any API call, so the probe costs no tokens.
func (b *Bridge) ProbeModel(ctx context.Context, model string) (string, error) {
	return b.Runner.ProbeModel(ctx, claudecli.Options{Model: model, Bare: b.Bare})
}

// Generate runs a single turn and collects the answer. When emit is non-nil the
// CLI is asked for token-level events and each piece is forwarded as it lands.
func (b *Bridge) Generate(ctx context.Context, in core.GenerateInput, emit func(core.StreamChunk) error) (core.GenerateOutput, error) {
	if b.PromptTools && len(in.Tools) > 0 {
		return b.generateWithPromptTools(ctx, in, emit)
	}
	// Partial events are also requested for turns that can end at a tool call
	// or a structured answer: those are cut before the CLI's result event, and
	// the message_delta partial event is then the only source of the final
	// output token count.
	partial := emit != nil || len(in.Tools) > 0 || in.JSONSchema != ""
	opts := claudecli.Options{
		Model:        in.Model,
		SystemPrompt: in.Prompt.System,
		JSONSchema:   in.JSONSchema,
		Effort:       in.Effort,
		Partial:      partial,
		Bare:         b.Bare,
	}
	if len(in.Tools) > 0 {
		exe, err := b.exe()
		if err != nil {
			return core.GenerateOutput{}, fmt.Errorf("bridge: locating the oocla binary: %w", err)
		}
		cfg, allowed, err := mcpshim.Config(exe, b.shimName(), in.Tools)
		if err != nil {
			return core.GenerateOutput{}, err
		}
		opts.MCPConfigJSON = cfg
		opts.AllowedTools = allowed
	}

	run, err := b.Runner.Start(ctx, opts, in.Prompt.User)
	if err != nil {
		return core.GenerateOutput{}, err
	}
	defer run.Kill()
	expectServer := ""
	if len(in.Tools) > 0 {
		expectServer = b.shimName()
	}
	out, err := collect(run, emit != nil, partial, in, emit, expectServer)
	if errors.Is(err, errMCPBlocked) {
		// The turn is cut before the model answers, so switching strategies
		// costs one aborted call, not a wasted answer.
		run.Stop()
		slog.Warn("mcp tool server unavailable; folding tools into the prompt",
			"model", in.Model)
		return b.generateWithPromptTools(ctx, in, emit)
	}
	return out, err
}

// collect drains one run into an answer. partial says whether the CLI was
// asked for token-level events; streaming says whether the caller wants them
// forwarded. expectServer names the MCP server the turn's tools were
// configured as, which the init event must then confirm; empty means no tools
// were configured.
func collect(run *claudecli.Run, streaming, partial bool, in core.GenerateInput, emit func(core.StreamChunk) error, expectServer string) (core.GenerateOutput, error) {
	// A JSON Schema changes what a tool call means, and either JSON mode makes
	// the text unusable until it is complete.
	structured := in.JSONSchema != ""
	buffered := structured || in.UnwrapJSON

	var (
		text       strings.Builder
		thinking   strings.Builder
		out        core.GenerateOutput
		result     *claudecli.Result
		calling    bool
		structural string
		// inputTokens comes from any usage snapshot (the request size is
		// fixed); outputTokens only from message_delta, whose count is final.
		// Both are for turns that end before the CLI reports its result.
		inputTokens  int
		outputTokens int
	)

	prefix := mcpshim.ToolPrefix
	if expectServer != "" {
		prefix = mcpshim.Prefix(expectServer)
	}
	addToolCall := func(tu *claudecli.ToolUse) error {
		tc, err := toolCall(tu, prefix)
		if err != nil {
			return err
		}
		out.ToolCalls = append(out.ToolCalls, tc)
		if streaming {
			return emit(core.StreamChunk{ToolCalls: []core.ToolCall{tc}})
		}
		return nil
	}

events:
	for {
		ev, err := run.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return core.GenerateOutput{}, err
		}
		if ev.Usage != nil && ev.Usage.InputTokens > 0 {
			inputTokens = ev.Usage.InputTokens
		}
		switch ev.Kind {
		case claudecli.KindInit:
			if expectServer != "" && !slices.Contains(ev.MCPServers, expectServer) {
				return core.GenerateOutput{}, errMCPBlocked
			}

		// In partial mode the CLI sends token deltas and, at the end of each
		// block, the assembled message. Only one of the two may be counted,
		// or the answer comes out doubled.
		case claudecli.KindText:
			if !partial {
				text.WriteString(ev.Text)
			}
		case claudecli.KindThinking:
			if !partial && in.IncludeThinking {
				thinking.WriteString(ev.Text)
			}
		case claudecli.KindTextDelta:
			text.WriteString(ev.Text)
			// Under a schema the prose is only a preamble to the structured
			// answer, which Ollama does not include; in JSON mode the text
			// still has to have its code fence removed. Either way, sending it
			// as it arrives would make the stream disagree with the answer.
			if buffered || !streaming {
				continue
			}
			if err := emit(core.StreamChunk{Text: ev.Text}); err != nil {
				return core.GenerateOutput{}, err
			}
		// The model always reasons; whether the client sees it is the
		// client's call, so unreported thinking is dropped rather than
		// collected and then hidden.
		case claudecli.KindThinkingDelta:
			if !in.IncludeThinking {
				continue
			}
			thinking.WriteString(ev.Text)
			if !streaming {
				continue
			}
			if err := emit(core.StreamChunk{Thinking: ev.Text}); err != nil {
				return core.GenerateOutput{}, err
			}

		// A tool call ends the turn. The API contract is that the client runs
		// the tool, so the CLI must not be allowed to run it and answer from
		// the result. Siblings decoded from the same line are collected first:
		// one message can carry several calls.
		//
		// In partial mode the turn is not cut here but at the message_delta
		// that follows the message's last block: it carries the final output
		// token count, and the CLI only moves on to executing the tool after
		// the message ends, so waiting for it costs nothing.
		case claudecli.KindToolUse:
			if structural != "" {
				continue
			}
			if structured && ev.ToolUse.Name == structuredOutputTool {
				structural = compact(ev.ToolUse.Input)
				calling = true
				if !partial {
					break events
				}
				continue
			}
			if err := addToolCall(ev.ToolUse); err != nil {
				return core.GenerateOutput{}, err
			}
			for _, sib := range run.Drain() {
				if sib.Kind == claudecli.KindToolUse {
					if err := addToolCall(sib.ToolUse); err != nil {
						return core.GenerateOutput{}, err
					}
				}
			}
			calling = true
			if !partial {
				break events
			}

		case claudecli.KindMessageDelta:
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				outputTokens = ev.Usage.OutputTokens
			}
			if calling {
				break events
			}
		case claudecli.KindMessageStop:
			if calling {
				break events
			}

		case claudecli.KindResult:
			result = ev.Result
		}
	}

	out.Text = text.String()
	out.Thinking = thinking.String()

	if structural != "" {
		// The constrained answer replaces the prose that led up to it.
		out.Text = structural
		run.Stop()
		out.StopReason = stopReasonEndTurn
		out.InputTokens, out.OutputTokens = inputTokens, outputTokens
		return out, flushBuffered(out.Text, streaming, emit)
	}
	if calling {
		run.Stop()
		out.StopReason = stopReasonToolUse
		out.InputTokens, out.OutputTokens = inputTokens, outputTokens
		return out, flushBuffered(out.Text, streaming && buffered, emit)
	}
	// A failing turn reports why in its result event and *also* exits non-zero.
	// The result is the better message, so the exit status is only used when
	// there was no result to explain it.
	closeErr := run.Close()
	if result == nil {
		if closeErr != nil {
			return core.GenerateOutput{}, closeErr
		}
		return core.GenerateOutput{}, errors.New("bridge: claude produced no result event")
	}
	if result.IsError {
		// The CLI flags a failed turn with is_error even when the subtype still
		// says success, so the subtype is only worth reporting when it differs.
		if result.Subtype != "" && result.Subtype != "success" {
			return core.GenerateOutput{}, fmt.Errorf("bridge: claude reported %s: %s", result.Subtype, result.Text)
		}
		return core.GenerateOutput{}, fmt.Errorf("bridge: claude reported an error: %s", result.Text)
	}
	if closeErr != nil {
		return core.GenerateOutput{}, closeErr
	}
	if in.UnwrapJSON {
		out.Text = core.UnwrapJSON(out.Text)
		if err := flushBuffered(out.Text, streaming, emit); err != nil {
			return core.GenerateOutput{}, err
		}
	}

	out.StopReason = result.StopReason
	out.InputTokens = result.Usage.InputTokens
	out.OutputTokens = result.Usage.OutputTokens
	out.Total = time.Duration(result.DurationMS) * time.Millisecond
	out.API = time.Duration(result.DurationAPIMS) * time.Millisecond
	return out, nil
}

// flushBuffered sends an answer that was held back, as a single chunk.
func flushBuffered(text string, streaming bool, emit func(core.StreamChunk) error) error {
	if !streaming || text == "" {
		return nil
	}
	return emit(core.StreamChunk{Text: text})
}

// compact normalizes a structured answer so clients get one canonical form.
func compact(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// toolCall converts a CLI tool_use block into the neutral shape, stripping
// the shim's server prefix from the tool name.
func toolCall(tu *claudecli.ToolUse, prefix string) (core.ToolCall, error) {
	args := map[string]any{}
	if len(tu.Input) > 0 {
		if err := json.Unmarshal(tu.Input, &args); err != nil {
			return core.ToolCall{}, fmt.Errorf("bridge: tool %q arguments: %w", tu.Name, err)
		}
	}
	return core.ToolCall{Function: core.ToolCallFunction{
		Name:      strings.TrimPrefix(tu.Name, prefix),
		Arguments: args,
	}}, nil
}
