package ollama

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/kazufusa/oocla/internal/core"
	"github.com/kazufusa/oocla/internal/httpapi"
)

// GenerateRequest is the body of POST /api/generate.
type GenerateRequest struct {
	Model   string          `json:"model"`
	Prompt  string          `json:"prompt"`
	System  string          `json:"system"`
	Stream  *bool           `json:"stream"`
	Format  json.RawMessage `json:"format"`
	Options map[string]any  `json:"options"`
	Images  []string        `json:"images"`
	Think   json.RawMessage `json:"think"`

	// Accepted and ignored. Context is Ollama's continuation handle, which
	// cannot be honoured without keeping sessions; Suffix and Raw describe
	// fill-in-the-middle and template bypass, neither of which the claude CLI
	// exposes; Template and KeepAlive describe a local model file that does
	// not exist here.
	Context   []int64         `json:"context"`
	Suffix    string          `json:"suffix"`
	Raw       bool            `json:"raw"`
	Template  string          `json:"template"`
	KeepAlive json.RawMessage `json:"keep_alive"`
}

// Streaming reports whether the client wants a streamed response.
func (r GenerateRequest) Streaming() bool { return r.Stream == nil || *r.Stream }

// GenerateResponse is one message of POST /api/generate.
type GenerateResponse struct {
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"created_at"`
	Response   string    `json:"response"`
	Thinking   string    `json:"thinking,omitempty"`
	Done       bool      `json:"done"`
	DoneReason string    `json:"done_reason,omitempty"`

	TotalDuration      int64 `json:"total_duration,omitempty"`
	LoadDuration       int64 `json:"load_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}

// DoneReasonLoad marks the reply to an empty prompt, which Ollama treats as a
// request to load the model rather than to generate anything.
const DoneReasonLoad = "load"

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req GenerateRequest
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	model, ok := s.eng.Reg.Lookup(req.Model)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", req.Model))
		return
	}
	// An empty prompt is how clients preload a model. There is nothing to load,
	// so acknowledge it and generate nothing.
	if req.Prompt == "" {
		writeJSON(w, http.StatusOK, GenerateResponse{
			Model: model.Name, CreatedAt: s.eng.Now(), Done: true, DoneReason: DoneReasonLoad,
		})
		return
	}

	format, err := ParseFormat(req.Format)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	think, err := ParseThink(req.Think)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts, err := s.options("/api/generate", req.Options)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	in := core.GenerateInput{
		Model:  model.CLIName,
		Prompt: core.Prompt{System: req.System, User: req.Prompt},
	}
	format.Apply(&in)
	think.Apply(&in)

	if req.Streaming() {
		s.generateStream(w, r, model.Name, in, opts)
		return
	}
	out, err := s.eng.Run(r.Context(), in, opts.Stop, nil)
	if err != nil {
		writeGeneratorError(w, err)
		return
	}
	logTokens(r, out)
	writeJSON(w, http.StatusOK, s.generateFinal(model.Name, out, true))
}

func (s *Server) generateStream(w http.ResponseWriter, r *http.Request, modelName string, in core.GenerateInput, opts Options) {
	writeLine, ok := ndjsonWriter(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}

	emit := func(c core.StreamChunk) error {
		// Tool calls have no place in the generate API; they only reach a
		// client through /api/chat.
		if c.Text == "" && c.Thinking == "" {
			return nil
		}
		return writeLine(GenerateResponse{
			Model:     modelName,
			CreatedAt: s.eng.Now(),
			Response:  c.Text,
			Thinking:  c.Thinking,
			Done:      false,
		})
	}
	out, err := s.eng.Run(r.Context(), in, opts.Stop, emit)
	if err != nil {
		_ = writeLine(map[string]string{"error": err.Error()})
		return
	}
	logTokens(r, out)
	_ = writeLine(s.generateFinal(modelName, out, false))
}

// generateFinal builds the terminating response. withResponse is false at the
// end of a stream, where the text has already been delivered in chunks.
func (s *Server) generateFinal(modelName string, out core.GenerateOutput, withResponse bool) GenerateResponse {
	res := GenerateResponse{
		Model:              modelName,
		CreatedAt:          s.eng.Now(),
		Done:               true,
		DoneReason:         doneReasonFor(out.StopReason),
		TotalDuration:      out.Total.Nanoseconds(),
		PromptEvalCount:    out.InputTokens,
		PromptEvalDuration: 0,
		EvalCount:          out.OutputTokens,
		EvalDuration:       out.API.Nanoseconds(),
	}
	if withResponse {
		res.Response = out.Text
		res.Thinking = out.Thinking
	}
	return res
}
