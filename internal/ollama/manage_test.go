package ollama

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Nothing is ever resident: each request spawns a CLI process that exits with
// the turn.
func TestPSReportsNothingResident(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "GET", "/api/ps", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got struct {
		Models []any `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Models == nil {
		t.Error("models is null, want an empty array")
	}
	if len(got.Models) != 0 {
		t.Errorf("models = %v, want empty", got.Models)
	}
}

// Clients pull before their first request and read a failure as "model
// unavailable", so a known model has to succeed.
func TestPullSucceedsForKnownModels(t *testing.T) {
	h := newChatServer(t, &fakeGen{})

	w := do(t, h, "POST", "/api/pull", `{"model":"opus","stream":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	if got := decode(t, w)["status"]; got != "success" {
		t.Errorf("status = %v", got)
	}

	w = do(t, h, "POST", "/api/pull", `{"model":"opus"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("streaming status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != ndjsonContentType {
		t.Errorf("Content-Type = %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	last := map[string]string{}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last["status"] != "success" {
		t.Errorf("last line = %v, want success", last)
	}
}

func TestPullRejectsUnknownModels(t *testing.T) {
	h := newChatServer(t, &fakeGen{})
	if w := do(t, h, "POST", "/api/pull", `{"model":"llama3"}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown model status = %d, want 404", w.Code)
	}
	if w := do(t, h, "POST", "/api/pull", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("missing model status = %d, want 400", w.Code)
	}
}

func TestPullAcceptsLegacyNameField(t *testing.T) {
	w := do(t, newChatServer(t, &fakeGen{}), "POST", "/api/pull", `{"name":"haiku","stream":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
}

func TestUnavailableOperations(t *testing.T) {
	h := newChatServer(t, &fakeGen{})
	cases := []struct{ method, path, body string }{
		{"DELETE", "/api/delete", `{"model":"opus"}`},
		{"POST", "/api/copy", `{"source":"opus","destination":"x"}`},
		{"POST", "/api/create", `{"model":"x","from":"opus"}`},
		{"POST", "/api/push", `{"model":"opus"}`},
	}
	for _, c := range cases {
		w := do(t, h, c.method, c.path, c.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d, want 400", c.method, c.path, w.Code)
		}
		msg, _ := decode(t, w)["error"].(string)
		if msg == "" {
			t.Errorf("%s %s: no error message", c.method, c.path)
		}
	}
}

func TestEmbeddingsAreNotImplemented(t *testing.T) {
	h := newChatServer(t, &fakeGen{})
	for _, path := range []string{"/api/embed", "/api/embeddings"} {
		w := do(t, h, "POST", path, `{"model":"opus","input":"hello"}`)
		if w.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", path, w.Code)
		}
	}
}
