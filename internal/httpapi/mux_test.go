package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMux() *Mux {
	m := NewMux()
	m.NotFound = func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "custom 404", http.StatusNotFound)
	}
	m.MethodNotAllowed = func(w http.ResponseWriter, r *http.Request, allow string) {
		w.Header().Set("Allow", allow)
		http.Error(w, "custom 405", http.StatusMethodNotAllowed)
	}
	return m
}

func hit(h http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestMuxRoutesExactPath(t *testing.T) {
	m := newMux()
	m.Handle("/api/x", http.MethodPost, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	if w := hit(m, "POST", "/api/x"); w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want the handler to run", w.Code)
	}
}

func TestMuxServesHeadForGet(t *testing.T) {
	m := newMux()
	m.Handle("/api/x", http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if w := hit(m, "HEAD", "/api/x"); w.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want 200", w.Code)
	}
}

func TestMuxUsesCustomNotFound(t *testing.T) {
	w := hit(newMux(), "GET", "/nope")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "custom 404") {
		t.Errorf("status = %d body = %q, want the custom not-found handler", w.Code, w.Body)
	}
}

func TestMuxUsesCustomMethodNotAllowed(t *testing.T) {
	m := newMux()
	m.Handle("/api/x", http.MethodPost, func(http.ResponseWriter, *http.Request) {})
	w := hit(m, "GET", "/api/x")
	if w.Code != http.StatusMethodNotAllowed || !strings.Contains(w.Body.String(), "custom 405") {
		t.Errorf("status = %d body = %q, want the custom handler", w.Code, w.Body)
	}
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, "POST") {
		t.Errorf("Allow = %q, want it to list POST", allow)
	}
}

func TestMuxRoutesPrefix(t *testing.T) {
	m := newMux()
	m.HandlePrefix("/v1/models/", http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	if w := hit(m, "GET", "/v1/models/opus"); w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want the prefix handler to run", w.Code)
	}
	// The bare prefix itself is not a match.
	if w := hit(m, "GET", "/v1/models/"); w.Code != http.StatusNotFound {
		t.Errorf("bare prefix status = %d, want 404", w.Code)
	}
}

func TestSplitPrefixDispatchesByPath(t *testing.T) {
	under := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	other := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	h := SplitPrefix("/v1/", under, other)

	for path, want := range map[string]int{
		"/v1/chat/completions": http.StatusTeapot,
		"/v1/nope":             http.StatusTeapot, // unknown paths under the prefix stay with the prefix handler
		"/api/chat":            http.StatusAccepted,
		"/":                    http.StatusAccepted,
		"/v1":                  http.StatusAccepted, // no trailing slash, not under the prefix
	} {
		if w := hit(h, "GET", path); w.Code != want {
			t.Errorf("%s: status = %d, want %d", path, w.Code, want)
		}
	}
}
