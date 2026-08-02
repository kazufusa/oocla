package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/kazufusa/oocla/internal/core"
	"github.com/kazufusa/oocla/internal/httpapi"
)

// ndjsonContentType is what Ollama sends for streamed responses.
const ndjsonContentType = "application/x-ndjson"

// chatSpec converts the wire request into the neutral spec the engine runs.
// The returned status is the one to report if err is non-nil.
func (s *Server) chatSpec(endpoint string, req ChatRequest) (core.ChatSpec, int, error) {
	format, err := ParseFormat(req.Format)
	if err != nil {
		return core.ChatSpec{}, http.StatusBadRequest, err
	}
	think, err := ParseThink(req.Think)
	if err != nil {
		return core.ChatSpec{}, http.StatusBadRequest, err
	}
	opts, err := s.options(endpoint, req.Options)
	if err != nil {
		return core.ChatSpec{}, http.StatusBadRequest, err
	}
	return core.ChatSpec{
		Model:    req.Model,
		Messages: req.Messages,
		Tools:    req.Tools,
		Format:   format,
		Think:    think,
		Stop:     opts.Stop,
	}, http.StatusOK, nil
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// An empty conversation is how clients preload a model before the first
	// real turn, not a generation request. As with /api/generate's empty
	// prompt, it is acknowledged as a single unstreamed object.
	if len(req.Messages) == 0 {
		model, ok := s.eng.Reg.Lookup(req.Model)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", req.Model))
			return
		}
		writeJSON(w, http.StatusOK, ChatChunk{
			Model: model.Name, CreatedAt: s.eng.Now(),
			Message: Message{Role: RoleAssistant},
			Done:    true, DoneReason: loadDoneReason(req.KeepAlive),
		})
		return
	}
	spec, status, err := s.chatSpec("/api/chat", req)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	p, status, err := s.eng.PlanChat(spec)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	if req.Streaming() {
		s.chatStream(w, r, p)
		return
	}
	out, err := s.eng.Run(r.Context(), p.In, p.Stop, nil)
	if err != nil {
		writeGeneratorError(w, err)
		return
	}
	logTokens(r, out)
	writeJSON(w, http.StatusOK, s.chatFinal(p.Model.Name, out, true))
}

// chatStream writes the answer as newline-delimited JSON, one object per chunk,
// terminated by a single object carrying done and the run statistics.
func (s *Server) chatStream(w http.ResponseWriter, r *http.Request, p core.ChatPlan) {
	writeLine, ok := ndjsonWriter(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}

	emit := func(c core.StreamChunk) error {
		return writeLine(ChatChunk{
			Model:     p.Model.Name,
			CreatedAt: s.eng.Now(),
			Message: Message{
				Role:      RoleAssistant,
				Content:   c.Text,
				Thinking:  c.Thinking,
				ToolCalls: c.ToolCalls,
			},
			Done: false,
		})
	}
	out, err := s.eng.Run(r.Context(), p.In, p.Stop, emit)
	if err != nil {
		// The status line is already sent, so a failure can only be reported
		// in-band. Ollama does the same.
		_ = writeLine(map[string]string{"error": err.Error()})
		return
	}
	logTokens(r, out)
	// The content already went out as chunks; repeating it here would make
	// clients that concatenate render the answer twice.
	_ = writeLine(s.chatFinal(p.Model.Name, out, false))
}

// chatFinal builds the terminating response. withContent is false at the end of
// a stream, where the answer has already been delivered chunk by chunk.
func (s *Server) chatFinal(modelName string, out core.GenerateOutput, withContent bool) ChatResponse {
	msg := Message{Role: RoleAssistant}
	if withContent {
		msg.Content = out.Text
		msg.Thinking = out.Thinking
		msg.ToolCalls = out.ToolCalls
	}
	return ChatResponse{
		Model:              modelName,
		CreatedAt:          s.eng.Now(),
		Message:            msg,
		Done:               true,
		DoneReason:         doneReasonFor(out.StopReason),
		TotalDuration:      out.Total.Nanoseconds(),
		PromptEvalCount:    out.InputTokens,
		PromptEvalDuration: 0,
		EvalCount:          out.OutputTokens,
		EvalDuration:       out.API.Nanoseconds(),
	}
}

// ndjsonWriter starts a newline-delimited JSON response and returns a function
// that writes and flushes one object.
func ndjsonWriter(w http.ResponseWriter) (func(any) error, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	w.Header().Set("Content-Type", ndjsonContentType)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	return func(v any) error {
		if err := enc.Encode(v); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}, true
}

// logTokens records a turn's token usage on the request's log line. A count
// the CLI never reported is 0, so each side is logged only when it is known.
func logTokens(r *http.Request, out core.GenerateOutput) {
	if out.InputTokens > 0 {
		httpapi.AddAttrs(r, "input_tokens", out.InputTokens)
	}
	if out.OutputTokens > 0 {
		httpapi.AddAttrs(r, "output_tokens", out.OutputTokens)
	}
}

// writeGeneratorError turns a backend failure into an Ollama-shaped error.
// A cancelled request is the client hanging up, not a server fault.
func writeGeneratorError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		writeError(w, http.StatusRequestTimeout, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
