package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Engine runs dialect-neutral chat turns against a Generator. Both API
// dialects convert their wire formats into a ChatSpec and hand it here; the
// engine owns everything that must behave identically no matter which dialect
// asked: validation, history flattening, stop sequences.
type Engine struct {
	Reg *Registry
	Gen Generator

	// NowFunc supplies timestamps. Tests replace it.
	NowFunc func() time.Time

	// Logger records what a dialect had to drop from a request. Nil means the
	// default logger.
	Logger *slog.Logger
}

// NewEngine returns an Engine serving the given model catalog, answering
// generation requests through gen.
func NewEngine(reg *Registry, gen Generator) *Engine {
	return &Engine{Reg: reg, Gen: gen}
}

// Now returns the current time, or the test override.
func (e *Engine) Now() time.Time {
	if e.NowFunc != nil {
		return e.NowFunc()
	}
	return time.Now()
}

// Log returns the engine's logger.
func (e *Engine) Log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// validRoles are the message roles a chat turn accepts.
var validRoles = map[string]bool{
	RoleSystem: true, RoleUser: true, RoleAssistant: true, RoleTool: true,
}

// ChatSpec is one chat turn in dialect-neutral terms: what to answer, with
// which model, shaped how. Model is the client-facing name, not the CLI one.
type ChatSpec struct {
	Model    string
	Messages []Message
	Tools    []Tool
	Format   Format
	Think    Think
	// Stop are sequences that end generation. Nothing in the CLI can stop
	// sampling, so the answer is truncated at the first one instead.
	Stop []string
}

// ChatPlan is a validated chat turn, ready to run.
type ChatPlan struct {
	Model Model
	In    GenerateInput
	Stop  []string
}

// PlanChat validates a chat spec and works out what to ask the backend. The
// returned status is the one to report if err is non-nil.
func (e *Engine) PlanChat(spec ChatSpec) (ChatPlan, int, error) {
	model, ok := e.Reg.Lookup(spec.Model)
	if !ok {
		return ChatPlan{}, http.StatusNotFound, fmt.Errorf("model %q not found", spec.Model)
	}
	for i, m := range spec.Messages {
		if !validRoles[m.Role] {
			return ChatPlan{}, http.StatusBadRequest, fmt.Errorf("messages[%d]: unknown role %q", i, m.Role)
		}
	}
	prompt, err := Flatten(spec.Messages)
	if err != nil {
		return ChatPlan{}, http.StatusBadRequest, err
	}

	in := GenerateInput{Model: model.CLIName, Prompt: prompt, Tools: spec.Tools}
	spec.Format.Apply(&in)
	spec.Think.Apply(&in)
	return ChatPlan{Model: model, In: in, Stop: spec.Stop}, http.StatusOK, nil
}

// Run executes one backend turn. emit non-nil streams chunks to it as they
// arrive; nil asks for the whole answer at once. Reaching a stop sequence is
// a normal end, not an error, and the returned text is truncated at the first
// stop sequence either way.
func (e *Engine) Run(ctx context.Context, in GenerateInput, stop []string, emit func(StreamChunk) error) (GenerateOutput, error) {
	if emit == nil {
		out, err := e.Gen.Generate(ctx, in, nil)
		if err != nil {
			return GenerateOutput{}, err
		}
		out.Text = TrimAtStop(out.Text, stop)
		return out, nil
	}

	guarded, flush := withStop(stop, emit)
	out, err := e.Gen.Generate(ctx, in, guarded)
	switch {
	case errors.Is(err, errStopSequence):
		// Not a failure: the client asked for generation to end here.
	case err != nil:
		return GenerateOutput{}, err
	default:
		if err := flush(); err != nil {
			return GenerateOutput{}, err
		}
	}
	out.Text = TrimAtStop(out.Text, stop)
	return out, nil
}
