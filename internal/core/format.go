package core

import (
	"encoding/json"
	"strings"
)

// jsonOnlyDirective is appended to the system prompt for bare "json" mode.
//
// The CLI's --json-schema is not usable here: with no properties to constrain,
// it wraps the answer in a synthetic object instead of letting the model choose
// the shape. Ollama's json mode is an instruction, so this is one too. It is
// the only text oocla ever adds to a prompt, and only when the client asks for
// it explicitly.
const jsonOnlyDirective = "Respond with a single valid JSON value and nothing else."

// Format describes how the client wants the answer shaped. Each dialect
// produces one from its own wire fields: Ollama from `format`, OpenAI from
// `response_format`.
type Format struct {
	// Schema is a JSON Schema to constrain the answer with, if one was given.
	Schema string
	// JSONOnly is set for bare "json" mode, where the shape is up to the model.
	JSONOnly bool
}

// Apply folds the format into a prompt and the backend input.
func (f Format) Apply(in *GenerateInput) {
	in.JSONSchema = f.Schema
	if !f.JSONOnly {
		return
	}
	in.UnwrapJSON = true
	if in.Prompt.System == "" {
		in.Prompt.System = jsonOnlyDirective
		return
	}
	in.Prompt.System = strings.TrimRight(in.Prompt.System, "\n") + "\n\n" + jsonOnlyDirective
}

// UnwrapJSON strips a Markdown code fence from an answer that is supposed to be
// nothing but JSON.
//
// Ollama constrains sampling and so never produces a fence. Here the shape is
// only asked for in words, and models habitually wrap JSON in ```json blocks.
// The text is returned unchanged unless removing the fence leaves valid JSON.
func UnwrapJSON(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return s
	}
	_, rest, ok := strings.Cut(trimmed, "\n")
	if !ok {
		return s
	}
	body, _, ok := strings.Cut(rest, "```")
	if !ok {
		return s
	}
	body = strings.TrimSpace(body)
	if !json.Valid([]byte(body)) {
		return s
	}
	return body
}
