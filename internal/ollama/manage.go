package ollama

import (
	"fmt"
	"net/http"

	"github.com/kazufusa/oocla/internal/httpapi"
)

// The endpoints in this file exist because Ollama has them and clients probe
// them. None of them can do what they say: there is no model file to pull,
// copy or delete, and nothing is ever resident in memory. Each one answers in
// the shape Ollama uses, and says plainly when the operation is meaningless
// rather than pretending it succeeded.

// modelRef is the body shared by the model management endpoints.
type modelRef struct {
	Model  string `json:"model"`
	Name   string `json:"name"` // legacy field, still sent by older clients
	Stream *bool  `json:"stream"`
}

func (m modelRef) name() string {
	if m.Model != "" {
		return m.Model
	}
	return m.Name
}

func (m modelRef) streaming() bool { return m.Stream == nil || *m.Stream }

// handlePS reports the models resident in memory. Nothing is ever resident:
// every request spawns a CLI process that exits when the turn ends.
func (s *Server) handlePS(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
}

// handlePull acknowledges a pull. Every advertised model is already reachable,
// so there is nothing to transfer, but clients call this before their first
// request and treat a failure as "model unavailable".
func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	var req modelRef
	if err := httpapi.DecodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.name() == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if _, ok := s.lookupModel(w, req.name()); !ok {
		return
	}
	if !req.streaming() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "success"})
		return
	}
	writeLine, ok := ndjsonWriter(w)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}
	for _, status := range []string{"pulling manifest", "verifying sha256 digest", "success"} {
		if err := writeLine(map[string]string{"status": status}); err != nil {
			return
		}
	}
}

// handleUnavailable refuses an operation that cannot be carried out here.
func handleUnavailable(what string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("%s is not supported: oocla serves Claude models through the claude CLI and has no local model files", what))
	}
}

// handleEmbeddings refuses an embedding request. The claude CLI has no
// embedding endpoint, so there is nothing to route to.
func (s *Server) handleEmbeddings(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented,
		"embeddings are not supported: the claude CLI does not expose an embedding model")
}
