package ollama

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kazufusa/oocla/internal/buildinfo"
	"github.com/kazufusa/oocla/internal/core"
	"github.com/kazufusa/oocla/internal/httpapi"
)

// Server implements the Ollama HTTP API on top of a core.Engine.
type Server struct {
	eng *core.Engine
	mux *httpapi.Mux
}

// NewServer returns a Server answering through eng.
func NewServer(eng *core.Engine) *Server {
	s := &Server{eng: eng, mux: httpapi.NewMux()}
	s.mux.NotFound = func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown endpoint %q", r.URL.Path))
	}
	s.mux.MethodNotAllowed = func(w http.ResponseWriter, r *http.Request, allow string) {
		w.Header().Set("Allow", allow)
		writeError(w, http.StatusMethodNotAllowed, fmt.Sprintf("method %s not allowed on %s", r.Method, r.URL.Path))
	}
	s.mux.Handle("/", http.MethodGet, s.handleRoot)
	s.mux.Handle("/api/version", http.MethodGet, s.handleVersion)
	s.mux.Handle("/api/tags", http.MethodGet, s.handleTags)
	s.mux.Handle("/api/show", http.MethodPost, s.handleShow)
	s.mux.Handle("/api/chat", http.MethodPost, s.handleChat)
	s.mux.Handle("/api/generate", http.MethodPost, s.handleGenerate)
	s.mux.Handle("/api/ps", http.MethodGet, s.handlePS)
	s.mux.Handle("/api/pull", http.MethodPost, s.handlePull)
	s.mux.Handle("/api/embed", http.MethodPost, s.handleEmbeddings)
	s.mux.Handle("/api/embeddings", http.MethodPost, s.handleEmbeddings)
	s.mux.Handle("/api/delete", http.MethodDelete, handleUnavailable("deleting a model"))
	s.mux.Handle("/api/copy", http.MethodPost, handleUnavailable("copying a model"))
	s.mux.Handle("/api/create", http.MethodPost, handleUnavailable("creating a model"))
	s.mux.Handle("/api/push", http.MethodPost, handleUnavailable("pushing a model"))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// options reads a request's options block, reporting anything the backend
// cannot act on. Unsupported options are dropped rather than refused: clients
// send a default block whether or not they care about it, and Ollama itself
// ignores options a model does not implement.
func (s *Server) options(endpoint string, raw map[string]any) (Options, error) {
	opts, ignored, err := ParseOptions(raw)
	if err != nil {
		return Options{}, err
	}
	if len(ignored) > 0 {
		s.eng.Log().Warn("ignoring unsupported options",
			"endpoint", endpoint, "options", strings.Join(ignored, ","))
	}
	return opts, nil
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "Ollama is running")
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": buildinfo.OllamaVersion})
}

// modelDetails mirrors the Ollama details object. oocla has no model file, so
// format and quantization are the values Ollama would report for a plain model
// and parameter_size is left empty rather than invented.
type modelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

func detailsFor(core.Model) modelDetails {
	return modelDetails{
		Format:            "gguf",
		Family:            "claude",
		Families:          []string{"claude"},
		ParameterSize:     "",
		QuantizationLevel: "F16",
	}
}

type tagsEntry struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    modelDetails `json:"details"`
}

func (s *Server) handleTags(w http.ResponseWriter, _ *http.Request) {
	models := s.eng.Reg.List()
	entries := make([]tagsEntry, 0, len(models))
	for _, m := range models {
		entries = append(entries, tagsEntry{
			Name:       m.Name,
			Model:      m.Name,
			ModifiedAt: m.ModifiedAt,
			Size:       m.Size,
			Digest:     m.Digest,
			Details:    detailsFor(m),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": entries})
}

type showRequest struct {
	Model string `json:"model"`
	Name  string `json:"name"` // legacy field, still sent by older clients
}

type showResponse struct {
	Modelfile    string         `json:"modelfile"`
	Parameters   string         `json:"parameters"`
	Template     string         `json:"template"`
	Details      modelDetails   `json:"details"`
	ModelInfo    map[string]any `json:"model_info"`
	Capabilities []string       `json:"capabilities"`
	ModifiedAt   time.Time      `json:"modified_at"`
}

func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	var req showRequest
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := req.Model
	if name == "" {
		name = req.Name
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	m, ok := s.lookupModel(w, name)
	if !ok {
		return
	}
	info := map[string]any{
		"general.architecture":  "claude",
		"general.basename":      m.CLIName,
		"general.description":   m.Description,
		"claude.context_length": m.ContextLength,
	}
	// The startup probe fills these in; a request racing it just sees less.
	if v := m.Version(); v != "" {
		info["general.version"] = v
	}
	if m.ResolvedID != "" {
		info["claude.resolved_model"] = m.ResolvedID
	}
	writeJSON(w, http.StatusOK, showResponse{
		Modelfile:    fmt.Sprintf("# oocla\nFROM %s\n", m.CLIName),
		Parameters:   "",
		Template:     "",
		Details:      detailsFor(m),
		ModelInfo:    info,
		Capabilities: m.Capabilities,
		ModifiedAt:   m.ModifiedAt,
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	httpapi.WriteJSON(w, code, v)
}

// writeError reports a failure in Ollama's flat error shape.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// lookupModel resolves a requested model name, reporting the 404 itself when
// there is no such model. Every handler answers that miss identically, so the
// wording lives here alone.
func (s *Server) lookupModel(w http.ResponseWriter, name string) (core.Model, bool) {
	m, ok := s.eng.Reg.Lookup(name)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", name))
	}
	return m, ok
}
