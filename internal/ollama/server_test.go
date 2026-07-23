package ollama

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kazufusa/oocla/internal/core"
)

func testTime() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	return NewServer(core.NewEngine(core.NewRegistry(testTime()), &fakeGen{}))
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return m
}

func TestRootReportsRunning(t *testing.T) {
	h := newTestServer(t)
	w := do(t, h, "GET", "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Body.String(); got != "Ollama is running" {
		t.Errorf("body = %q, want %q", got, "Ollama is running")
	}
}

func TestHeadRootReportsRunning(t *testing.T) {
	w := do(t, newTestServer(t), "HEAD", "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestVersion(t *testing.T) {
	w := do(t, newTestServer(t), "GET", "/api/version", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := decode(t, w)["version"]; got == "" || got == nil {
		t.Errorf("version = %v, want non-empty", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestTags(t *testing.T) {
	w := do(t, newTestServer(t), "GET", "/api/tags", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got struct {
		Models []struct {
			Name       string `json:"name"`
			Model      string `json:"model"`
			ModifiedAt string `json:"modified_at"`
			Size       int64  `json:"size"`
			Digest     string `json:"digest"`
			Details    struct {
				Family string `json:"family"`
				Format string `json:"format"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Models) == 0 {
		t.Fatal("no models returned")
	}
	for _, m := range got.Models {
		if m.Name == "" || m.Model != m.Name {
			t.Errorf("name/model mismatch: %q vs %q", m.Name, m.Model)
		}
		if !strings.Contains(m.Name, ":") {
			t.Errorf("%q is missing a tag suffix", m.Name)
		}
		if m.ModifiedAt == "" {
			t.Errorf("%s: modified_at is empty", m.Name)
		}
		if m.Size <= 0 || len(m.Digest) != 64 {
			t.Errorf("%s: size=%d digest=%q", m.Name, m.Size, m.Digest)
		}
		if m.Details.Family == "" || m.Details.Format == "" {
			t.Errorf("%s: details incomplete: %+v", m.Name, m.Details)
		}
	}
}

func TestShow(t *testing.T) {
	h := newTestServer(t)
	for _, body := range []string{`{"model":"opus"}`, `{"name":"opus:latest"}`} {
		w := do(t, h, "POST", "/api/show", body)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d body = %s", body, w.Code, w.Body)
		}
		got := decode(t, w)
		for _, k := range []string{"details", "model_info", "capabilities", "template", "parameters", "modelfile", "modified_at"} {
			if _, ok := got[k]; !ok {
				t.Errorf("%s: response is missing %q", body, k)
			}
		}
		caps, _ := got["capabilities"].([]any)
		if len(caps) == 0 {
			t.Errorf("%s: capabilities is empty", body)
		}
	}
}

func TestShowUnknownModelIs404(t *testing.T) {
	w := do(t, newTestServer(t), "POST", "/api/show", `{"model":"llama3"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if msg, _ := decode(t, w)["error"].(string); !strings.Contains(msg, "llama3") {
		t.Errorf("error = %q, want it to name the model", msg)
	}
}

func TestShowRejectsBadJSON(t *testing.T) {
	w := do(t, newTestServer(t), "POST", "/api/show", `{`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestShowRequiresModel(t *testing.T) {
	w := do(t, newTestServer(t), "POST", "/api/show", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	w := do(t, newTestServer(t), "GET", "/api/show", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestUnknownRouteIs404JSON(t *testing.T) {
	w := do(t, newTestServer(t), "GET", "/api/nope", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if _, ok := decode(t, w)["error"]; !ok {
		t.Errorf("body = %q, want a JSON error", w.Body.String())
	}
}
