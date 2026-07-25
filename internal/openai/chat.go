package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kazufusa/oocla/internal/core"
	"github.com/kazufusa/oocla/internal/httpapi"
)

const (
	sseContentType = "text/event-stream"
	// systemFingerprint is what Ollama reports. Clients only compare it across
	// responses, so a constant is correct.
	systemFingerprint = "fp_oocla"
)

type oaiToolCall struct {
	ID       string `json:"id"`
	Index    int    `json:"index,omitempty"`
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
		// Arguments is a JSON document encoded as a string, not an object.
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiMessage struct {
	// Role is omitted when empty: OpenAI announces it once, in the first
	// streamed delta, and leaves it out of the rest.
	Role    string `json:"role,omitempty"`
	Content any    `json:"content,omitempty"`
	// Reasoning carries the model's thinking. OpenAI has no field for it and
	// Ollama uses this name, so clients that want it know where to look.
	Reasoning  string        `json:"reasoning,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

type oaiResponseFormat struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
	} `json:"json_schema"`
}

type oaiChatRequest struct {
	Model         string       `json:"model"`
	Messages      []oaiMessage `json:"messages"`
	Stream        bool         `json:"stream"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	Tools          []core.Tool        `json:"tools"`
	ResponseFormat *oaiResponseFormat `json:"response_format"`
	Stop           json.RawMessage    `json:"stop"`
	// ReasoningEffort is OpenAI's reasoning control and maps onto the same
	// effort levels the Ollama think field selects.
	ReasoningEffort string `json:"reasoning_effort"`

	// Accepted and dropped, like their counterparts in the Ollama dialect.
	MaxTokens           int      `json:"max_tokens"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	Temperature         *float64 `json:"temperature"`
	TopP                *float64 `json:"top_p"`
	Seed                *int     `json:"seed"`
	FrequencyPenalty    *float64 `json:"frequency_penalty"`
	PresencePenalty     *float64 `json:"presence_penalty"`
	User                string   `json:"user"`
}

type oaiChoice struct {
	Index        int         `json:"index"`
	Message      *oaiMessage `json:"message,omitempty"`
	Delta        *oaiMessage `json:"delta,omitempty"`
	FinishReason *string     `json:"finish_reason"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type oaiChatResponse struct {
	ID                string      `json:"id"`
	Object            string      `json:"object"`
	Created           int64       `json:"created"`
	Model             string      `json:"model"`
	SystemFingerprint string      `json:"system_fingerprint"`
	Choices           []oaiChoice `json:"choices"`
	Usage             *oaiUsage   `json:"usage,omitempty"`
}

// OpenAI finish reasons.
const (
	finishStop      = "stop"
	finishLength    = "length"
	finishToolCalls = "tool_calls"
)

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req oaiChatRequest
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error())
		return
	}
	spec, err := req.spec()
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, status, err := s.eng.PlanChat(spec)
	if err != nil {
		writeOpenAIError(w, status, err.Error())
		return
	}

	id := s.completionID()
	if req.Stream {
		s.stream(w, r, p, id, req.StreamOptions != nil && req.StreamOptions.IncludeUsage)
		return
	}
	out, err := s.eng.Run(r.Context(), p.In, p.Stop, nil)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logTokens(r, out)

	msg := oaiMessage{Role: core.RoleAssistant, Content: out.Text, Reasoning: out.Thinking}
	msg.ToolCalls = toOpenAIToolCalls(out.ToolCalls)
	finish := finishFor(out)
	httpapi.WriteJSON(w, http.StatusOK, oaiChatResponse{
		ID:                id,
		Object:            "chat.completion",
		Created:           s.eng.Now().Unix(),
		Model:             p.Model.Name,
		SystemFingerprint: systemFingerprint,
		Choices:           []oaiChoice{{Index: 0, Message: &msg, FinishReason: &finish}},
		Usage: &oaiUsage{
			PromptTokens:     out.InputTokens,
			CompletionTokens: out.OutputTokens,
			TotalTokens:      out.InputTokens + out.OutputTokens,
		},
	})
}

// stream writes the answer as server-sent events, ending with the [DONE]
// sentinel the OpenAI SDKs wait for.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, p core.ChatPlan, id string, includeUsage bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}
	w.Header().Set("Content-Type", sseContentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	writeEvent := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	chunk := func(delta oaiMessage, finish *string) oaiChatResponse {
		return oaiChatResponse{
			ID:                id,
			Object:            "chat.completion.chunk",
			Created:           s.eng.Now().Unix(),
			Model:             p.Model.Name,
			SystemFingerprint: systemFingerprint,
			Choices:           []oaiChoice{{Index: 0, Delta: &delta, FinishReason: finish}},
		}
	}

	// The first event announces the role, as OpenAI does.
	if err := writeEvent(chunk(oaiMessage{Role: core.RoleAssistant}, nil)); err != nil {
		return
	}
	emit := func(c core.StreamChunk) error {
		delta := oaiMessage{Reasoning: c.Thinking, ToolCalls: toOpenAIToolCalls(c.ToolCalls)}
		if c.Text != "" {
			delta.Content = c.Text
		}
		return writeEvent(chunk(delta, nil))
	}

	out, err := s.eng.Run(r.Context(), p.In, p.Stop, emit)
	if err != nil {
		// SSE has no status code left to use, so the error goes in the stream.
		_ = writeEvent(map[string]any{"error": map[string]string{"message": err.Error()}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	logTokens(r, out)
	finish := finishFor(out)
	last := chunk(oaiMessage{}, &finish)
	if includeUsage {
		last.Usage = &oaiUsage{
			PromptTokens:     out.InputTokens,
			CompletionTokens: out.OutputTokens,
			TotalTokens:      out.InputTokens + out.OutputTokens,
		}
	}
	if err := writeEvent(last); err != nil {
		return
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// logTokens records a turn's token usage on the request's log line. A turn
// cut short by a tool call has a real input count but no final output count,
// so each side is logged only when it is known.
func logTokens(r *http.Request, out core.GenerateOutput) {
	if out.InputTokens > 0 {
		httpapi.AddAttrs(r, "input_tokens", out.InputTokens)
	}
	if out.OutputTokens > 0 {
		httpapi.AddAttrs(r, "output_tokens", out.OutputTokens)
	}
}

func finishFor(out core.GenerateOutput) string {
	if len(out.ToolCalls) > 0 {
		return finishToolCalls
	}
	if out.StopReason == "max_tokens" {
		return finishLength
	}
	return finishStop
}

// spec converts an OpenAI request into the neutral chat spec. The Ollama
// dialect's types are never involved: each wire field maps straight onto its
// core counterpart.
func (r oaiChatRequest) spec() (core.ChatSpec, error) {
	msgs, err := r.messages()
	if err != nil {
		return core.ChatSpec{}, err
	}
	spec := core.ChatSpec{
		Model:    r.Model,
		Messages: msgs,
		Tools:    r.Tools,
	}
	spec.Format, err = r.format()
	if err != nil {
		return core.ChatSpec{}, err
	}
	if r.ReasoningEffort != "" {
		if !core.ValidEffort(r.ReasoningEffort) {
			return core.ChatSpec{}, fmt.Errorf(`reasoning_effort must be one of "low", "medium", "high": got %q`, r.ReasoningEffort)
		}
		spec.Think = core.Think{Enabled: true, Effort: r.ReasoningEffort}
	}
	spec.Stop, err = r.stop()
	if err != nil {
		return core.ChatSpec{}, err
	}
	return spec, nil
}

func (r oaiChatRequest) messages() ([]core.Message, error) {
	// A tool result names the call it answers, not the tool. Resolving the id
	// against the calls already seen is what recovers the name.
	names := map[string]string{}
	out := make([]core.Message, 0, len(r.Messages))
	for i, m := range r.Messages {
		content, err := oaiContent(m.Content)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %w", i, err)
		}
		msg := core.Message{Role: m.Role, Content: content}
		if msg.Role == "developer" {
			// OpenAI's newer name for a system message.
			msg.Role = core.RoleSystem
		}
		for _, tc := range m.ToolCalls {
			args := map[string]any{}
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					return nil, fmt.Errorf("messages[%d]: tool call arguments: %w", i, err)
				}
			}
			if tc.ID != "" {
				names[tc.ID] = tc.Function.Name
			}
			msg.ToolCalls = append(msg.ToolCalls, core.ToolCall{Function: core.ToolCallFunction{
				Name: tc.Function.Name, Arguments: args,
			}})
		}
		if msg.Role == core.RoleTool {
			switch {
			case m.Name != "":
				msg.ToolName = m.Name
			case names[m.ToolCallID] != "":
				msg.ToolName = names[m.ToolCallID]
			}
		}
		out = append(out, msg)
	}
	return out, nil
}

// oaiContent accepts both spellings of a message body: a plain string, or the
// array of typed parts the vision-era API introduced.
func oaiContent(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case []any:
		var b strings.Builder
		for _, part := range t {
			p, ok := part.(map[string]any)
			if !ok {
				return "", fmt.Errorf("content parts must be objects, got %T", part)
			}
			if p["type"] != "text" {
				return "", fmt.Errorf("unsupported content part %v", p["type"])
			}
			text, _ := p["text"].(string)
			b.WriteString(text)
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("content must be a string or an array of parts, got %T", v)
	}
}

func (r oaiChatRequest) format() (core.Format, error) {
	if r.ResponseFormat == nil {
		return core.Format{}, nil
	}
	switch r.ResponseFormat.Type {
	case "", "text":
		return core.Format{}, nil
	case "json_object":
		return core.Format{JSONOnly: true}, nil
	case "json_schema":
		if r.ResponseFormat.JSONSchema == nil || len(r.ResponseFormat.JSONSchema.Schema) == 0 {
			return core.Format{}, fmt.Errorf("response_format: json_schema requires a schema")
		}
		return core.Format{Schema: core.CompactJSON(r.ResponseFormat.JSONSchema.Schema)}, nil
	default:
		return core.Format{}, fmt.Errorf("response_format: unsupported type %q", r.ResponseFormat.Type)
	}
}

func (r oaiChatRequest) stop() ([]string, error) {
	if len(r.Stop) == 0 {
		return nil, nil
	}
	var one string
	if err := json.Unmarshal(r.Stop, &one); err == nil {
		if one == "" {
			return nil, nil
		}
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(r.Stop, &many); err != nil {
		return nil, fmt.Errorf("stop must be a string or an array of strings")
	}
	out := make([]string, 0, len(many))
	for _, s := range many {
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// toOpenAIToolCalls re-encodes tool calls in OpenAI's shape, where arguments
// are a JSON string rather than an object.
func toOpenAIToolCalls(calls []core.ToolCall) []oaiToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]oaiToolCall, 0, len(calls))
	for i, c := range calls {
		args, err := json.Marshal(c.Function.Arguments)
		if err != nil {
			args = []byte("{}")
		}
		var tc oaiToolCall
		tc.ID = "call_" + strconv.Itoa(i)
		tc.Index = i
		tc.Type = "function"
		tc.Function.Name = c.Function.Name
		tc.Function.Arguments = string(args)
		out = append(out, tc)
	}
	return out
}
