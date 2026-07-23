package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kazufusa/oocla/internal/core"
)

// The OpenAI dialect owns everything under /v1/, so even a miss there must
// come back in OpenAI's nested error shape, not Ollama's flat one.

func newRoutingServer(t *testing.T) http.Handler {
	t.Helper()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return NewServer(core.NewEngine(core.NewRegistry(created), nil))
}

func openAIError(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if body.Error == nil {
		t.Fatalf("body = %q, want a nested OpenAI error object", w.Body.String())
	}
	return body.Error
}

func TestUnknownV1EndpointIsOpenAIError(t *testing.T) {
	h := newRoutingServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	msg, _ := openAIError(t, w)["message"].(string)
	if !strings.Contains(msg, "/v1/nope") {
		t.Errorf("message = %q, want it to name the endpoint", msg)
	}
}

// The OpenAI surface reports missing embeddings in the OpenAI error shape.
func TestEmbeddingsAreNotImplemented(t *testing.T) {
	h := newRoutingServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/embeddings",
		strings.NewReader(`{"model":"opus","input":"hello"}`)))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
	if msg, _ := openAIError(t, w)["message"].(string); msg == "" {
		t.Errorf("body = %s, want a nested OpenAI error", w.Body)
	}
}

func TestWrongMethodOnV1IsOpenAIError(t *testing.T) {
	h := newRoutingServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/chat/completions", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, "POST") {
		t.Errorf("Allow = %q, want it to list POST", allow)
	}
	openAIError(t, w)
}
