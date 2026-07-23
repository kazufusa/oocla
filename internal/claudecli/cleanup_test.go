package claudecli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The transcript directory is derived from an internal CLI layout, so it may
// only ever be derived from a directory this package created.
func TestTranscriptDirOnlyForScratchDirectories(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/config")

	got, ok := transcriptDir("/tmp/" + scratchPrefix + "123")
	if !ok {
		t.Fatal("a scratch directory produced no transcript directory")
	}
	want := filepath.Join("/config", "projects", "-tmp-"+scratchPrefix+"123")
	if got != want {
		t.Errorf("transcriptDir = %q, want %q", got, want)
	}

	for _, dir := range []string{"/home/someone/project", "/tmp/other", "/", "/tmp"} {
		if _, ok := transcriptDir(dir); ok {
			t.Errorf("transcriptDir(%q) claimed a directory it did not create", dir)
		}
	}
}

func TestCleanupRemovesTranscripts(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)

	r := &Runner{Bin: "true"}
	cwd, err := r.dir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cwd, scratchPrefix) {
		t.Fatalf("scratch directory = %q", cwd)
	}

	transcripts, ok := transcriptDir(cwd)
	if !ok {
		t.Fatal("no transcript directory for the scratch directory")
	}
	if err := os.MkdirAll(transcripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transcripts, "sess.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := r.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cwd); !os.IsNotExist(err) {
		t.Errorf("scratch directory survived cleanup")
	}
	if _, err := os.Stat(transcripts); !os.IsNotExist(err) {
		t.Errorf("transcripts survived cleanup")
	}
}

// A Runner given an explicit directory did not create it and must not delete
// anything.
func TestCleanupLeavesExplicitDirectoriesAlone(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{Bin: "true", Dir: dir}
	if _, err := r.dir(); err != nil {
		t.Fatal(err)
	}
	if err := r.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("an explicitly given directory was removed: %v", err)
	}
}
