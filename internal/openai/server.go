// Package openai serves the OpenAI dialect: every endpoint under /v1/*,
// speaking OpenAI's wire shapes. Requests are converted into the neutral
// core types and run through core.Engine; nothing Ollama-shaped lives here.
package openai

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/kazufusa/oocla/internal/core"
	"github.com/kazufusa/oocla/internal/httpapi"
)

// ownedBy labels the models in /v1/models. It is what Ollama reports.
const ownedBy = "library"

// Server implements the OpenAI-compatible HTTP API on top of a core.Engine.
type Server struct {
	eng *core.Engine
	mux *httpapi.Mux

	// nextCompletionID hands out the ids completions are labelled with.
	nextCompletionID atomic.Int64
}

// NewServer returns a Server answering through eng.
func NewServer(eng *core.Engine) *Server {
	s := &Server{eng: eng, mux: httpapi.NewMux()}
	s.mux.NotFound = func(w http.ResponseWriter, r *http.Request) {
		writeOpenAIError(w, http.StatusNotFound, fmt.Sprintf("unknown endpoint %q", r.URL.Path))
	}
	s.mux.MethodNotAllowed = func(w http.ResponseWriter, r *http.Request, allow string) {
		w.Header().Set("Allow", allow)
		writeOpenAIError(w, http.StatusMethodNotAllowed, fmt.Sprintf("method %s not allowed on %s", r.Method, r.URL.Path))
	}
	s.mux.Handle("/v1/chat/completions", http.MethodPost, s.handleChat)
	s.mux.Handle("/v1/embeddings", http.MethodPost, s.handleEmbeddings)
	s.mux.Handle("/v1/models", http.MethodGet, s.handleModels)
	s.mux.HandlePrefix("/v1/models/", http.MethodGet, s.handleModel)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// completionID hands out the opaque id a completion is labelled with.
func (s *Server) completionID() string {
	return "chatcmpl-" + strconv.FormatInt(s.nextCompletionID.Add(1), 10)
}

// writeOpenAIError reports a failure in OpenAI's error shape, which its SDKs
// parse rather than the plain {"error": "..."} Ollama uses.
func writeOpenAIError(w http.ResponseWriter, code int, msg string) {
	httpapi.WriteJSON(w, code, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "invalid_request_error",
			"code":    code,
		},
	})
}

// handleEmbeddings refuses an embedding request. The claude CLI has no
// embedding endpoint, so there is nothing to route to.
func (s *Server) handleEmbeddings(w http.ResponseWriter, _ *http.Request) {
	writeOpenAIError(w, http.StatusNotImplemented,
		"embeddings are not supported: the claude CLI does not expose an embedding model")
}

type oaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	models := s.eng.Reg.List()
	data := make([]oaiModel, 0, len(models))
	for _, m := range models {
		data = append(data, oaiModel{
			ID: m.Name, Object: "model", Created: m.ModifiedAt.Unix(), OwnedBy: ownedBy,
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	m, ok := s.eng.Reg.Lookup(name)
	if !ok {
		writeOpenAIError(w, http.StatusNotFound, fmt.Sprintf("model %q not found", name))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, oaiModel{
		ID: m.Name, Object: "model", Created: m.ModifiedAt.Unix(), OwnedBy: ownedBy,
	})
}
