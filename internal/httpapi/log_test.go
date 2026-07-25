package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogRequestsRecordsMethodPathStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := LogRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/chat", nil))

	line := buf.String()
	for _, want := range []string{"method=POST", "path=/api/chat", "status=404", "duration="} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q is missing %q", line, want)
		}
	}
}

// Streaming handlers depend on http.Flusher; the logger must not hide it.
func TestLogRequestsKeepsFlusher(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	sawFlusher := false
	h := LogRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, sawFlusher = w.(http.Flusher)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/chat", nil))
	if !sawFlusher {
		t.Error("the wrapper hid http.Flusher from the handler")
	}
}

func TestLogRequestsIncludesAddedAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := LogRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		AddAttrs(r, "input_tokens", 170, "output_tokens", 81)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/chat", nil))

	line := buf.String()
	for _, want := range []string{"input_tokens=170", "output_tokens=81"} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q is missing %q", line, want)
		}
	}
}

// AddAttrs outside the wrapper must be harmless: dialect handlers call it
// unconditionally.
func TestAddAttrsWithoutWrapperIsNoOp(t *testing.T) {
	AddAttrs(httptest.NewRequest("POST", "/api/chat", nil), "input_tokens", 1)
}
