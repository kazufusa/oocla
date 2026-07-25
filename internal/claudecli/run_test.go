package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeUserTurn(t *testing.T) {
	b, err := EncodeUserTurn("hello \"world\"")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Error("line is not newline terminated")
	}
	var got struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "user" || got.Message.Role != "user" || got.Message.Content != `hello "world"` {
		t.Errorf("decoded = %+v", got)
	}
}

// fakeClaude writes a small script that echoes canned stream-json, so the
// spawn path can be tested without calling the real CLI.
func fakeClaude(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStartStreamsEvents(t *testing.T) {
	bin := fakeClaude(t, `
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"assistant","session_id":"sess-1","message":{"content":[{"type":"text","text":"hi"}]}}'
echo '{"type":"result","subtype":"success","session_id":"sess-1","result":"hi","stop_reason":"end_turn"}'
`)
	r := &Runner{Bin: bin}
	t.Cleanup(func() { _ = r.Cleanup() })

	run, err := r.Start(context.Background(), Options{Model: "haiku"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()

	var kinds []Kind
	for {
		e, err := run.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, e.Kind)
	}
	want := []Kind{KindInit, KindText, KindResult}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if err := run.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestStartPassesPromptOnStdin(t *testing.T) {
	bin := fakeClaude(t, `
line=$(cat)
printf '{"type":"result","subtype":"success","result":%s}\n' "$(printf '%s' "$line" | sed 's/.*"content":"\([^"]*\)".*/"\1"/')"
`)
	r := &Runner{Bin: bin}
	t.Cleanup(func() { _ = r.Cleanup() })

	run, err := r.Start(context.Background(), Options{Model: "haiku"}, "ping")
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()

	e, err := run.Next()
	if err != nil {
		t.Fatal(err)
	}
	if e.Result == nil || e.Result.Text != "ping" {
		t.Errorf("prompt did not reach stdin: %+v", e.Result)
	}
}

func TestCloseReportsNonZeroExit(t *testing.T) {
	bin := fakeClaude(t, "cat >/dev/null; echo 'boom' >&2; exit 3")
	r := &Runner{Bin: bin}
	t.Cleanup(func() { _ = r.Cleanup() })

	run, err := r.Start(context.Background(), Options{Model: "haiku"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next = %v, want EOF", err)
	}
	err = run.Close()
	if err == nil {
		t.Fatal("Close: want error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("Close error = %v, want stderr included", err)
	}
}

func TestStartRejectsInvalidOptions(t *testing.T) {
	r := &Runner{Bin: fakeClaude(t, "true")}
	t.Cleanup(func() { _ = r.Cleanup() })
	if _, err := r.Start(context.Background(), Options{}, "x"); err == nil {
		t.Fatal("want error for missing model")
	}
}

// The CLI derives project context from its cwd, so the default must not be the
// directory oocla happens to be started from.
func TestRunnerUsesIsolatedWorkingDirectory(t *testing.T) {
	r := &Runner{Bin: fakeClaude(t, "cat >/dev/null; pwd")}
	t.Cleanup(func() { _ = r.Cleanup() })

	dir, err := r.dir()
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if dir == cwd {
		t.Errorf("runner dir = cwd = %q, want an isolated directory", dir)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("runner dir is not empty: %v %v", entries, err)
	}
	if err := r.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Cleanup did not remove %q", dir)
	}
}

// probeStub builds a Runner whose claude prints the given mcp list output.
func probeStub(t *testing.T, listing string) *Runner {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nif [ \"$1\" = mcp ]; then printf '%s\\n' '" + listing + "'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Runner{Bin: path}
}

func TestProbeMCPNameAllowed(t *testing.T) {
	r := probeStub(t, "oocla: /usr/bin/oocla mcp-shim - ✓ Connected")
	ok, err := r.ProbeMCPName(context.Background(), "oocla", "/usr/bin/oocla")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("want the listed server to count as allowed")
	}
}

func TestProbeMCPNameBlocked(t *testing.T) {
	r := probeStub(t, "No MCP servers configured. Use `claude mcp add` to add a server.")
	ok, err := r.ProbeMCPName(context.Background(), "oocla", "/usr/bin/oocla")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("want a stripped server to count as blocked")
	}
}
