package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultBin is the claude executable oocla spawns.
const DefaultBin = "claude"

// Runner spawns `claude` processes.
//
// Dir is the working directory for every spawned process. It defaults to a
// fresh empty directory: the CLI derives project context from its cwd, so
// running in the user's repository would leak CLAUDE.md and project memory into
// a request that is supposed to reach a bare model.
type Runner struct {
	Bin string
	Dir string

	once   sync.Once
	tmpDir string
	tmpErr error
}

// NewRunner returns a Runner using the claude binary on PATH.
func NewRunner() *Runner { return &Runner{Bin: DefaultBin} }

func (r *Runner) bin() string {
	if r.Bin != "" {
		return r.Bin
	}
	return DefaultBin
}

// dir returns the working directory, creating a scratch one on first use.
func (r *Runner) dir() (string, error) {
	if r.Dir != "" {
		return r.Dir, nil
	}
	r.once.Do(func() {
		r.tmpDir, r.tmpErr = os.MkdirTemp("", scratchPrefix)
	})
	return r.tmpDir, r.tmpErr
}

// scratchPrefix names the scratch working directories this package creates.
// Nothing outside oocla uses it, which is what makes the transcript directory
// derived from it safe to delete.
const scratchPrefix = "oocla-cwd-"

// Cleanup removes the scratch working directory and the session transcripts the
// CLI wrote for it, if one was created.
func (r *Runner) Cleanup() error {
	if r.tmpDir == "" {
		return nil
	}
	err := os.RemoveAll(r.tmpDir)
	if dir, ok := transcriptDir(r.tmpDir); ok {
		if rmErr := os.RemoveAll(dir); err == nil {
			err = rmErr
		}
	}
	return err
}

// transcriptDir returns where the CLI keeps session transcripts for a working
// directory.
//
// The CLI derives the name by replacing every path separator with a hyphen,
// under ~/.claude/projects (or CLAUDE_CONFIG_DIR). That is an internal layout,
// so the result is only reported when it clearly belongs to a scratch directory
// this package created: nothing else is ever a candidate for deletion.
func transcriptDir(cwd string) (string, bool) {
	if !strings.Contains(filepath.Base(cwd), scratchPrefix) {
		return "", false
	}
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		config = filepath.Join(home, ".claude")
	}
	encoded := strings.ReplaceAll(filepath.Clean(cwd), string(filepath.Separator), "-")
	return filepath.Join(config, "projects", encoded), true
}

// Run is one in-flight `claude` invocation.
type Run struct {
	cmd     *exec.Cmd
	dec     *Decoder
	stderr  *bytes.Buffer
	stdout  io.ReadCloser
	waited  bool
	stopped bool
}

// userLine is the stream-json encoding of a single user turn.
type userLine struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
}

// EncodeUserTurn returns the stream-json line for a user message.
func EncodeUserTurn(text string) ([]byte, error) {
	var l userLine
	l.Type = "user"
	l.Message.Role = "user"
	l.Message.Content = text
	b, err := json.Marshal(l)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Start spawns claude with opts and sends prompt as the single user turn.
//
// stdin is closed after the prompt so the CLI ends the session once the turn
// completes. Multi-turn conversations are handled by resuming a session, not by
// holding stdin open.
func (r *Runner) Start(ctx context.Context, opts Options, prompt string) (*Run, error) {
	args, err := opts.Args()
	if err != nil {
		return nil, err
	}
	dir, err := r.dir()
	if err != nil {
		return nil, fmt.Errorf("claudecli: working directory: %w", err)
	}
	line, err := EncodeUserTurn(prompt)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, r.bin(), args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Stdin = bytes.NewReader(line)
	isolateGroup(cmd)

	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("claudecli: start %s: %w", r.bin(), err)
	}
	return &Run{cmd: cmd, dec: NewDecoder(stdout), stderr: stderr, stdout: stdout}, nil
}

// Next returns the next event, or io.EOF when the process stops producing
// output. Call Close afterwards to reap the process.
func (run *Run) Next() (Event, error) { return run.dec.Next() }

// Drain returns the events decoded from the last line but not yet returned by
// Next. Use it after stopping early on one event to collect its siblings.
func (run *Run) Drain() []Event { return run.dec.Drain() }

// Stop ends the turn deliberately. Unlike Close it does not treat the
// process's exit status as a failure, because the caller is the one ending it.
func (run *Run) Stop() {
	run.stopped = true
	run.Kill()
	_ = run.Close()
}

// Stderr returns whatever the process wrote to stderr so far.
func (run *Run) Stderr() string { return run.stderr.String() }

// Close drains stdout and waits for the process. It reports a non-nil error if
// claude exited non-zero, with stderr attached.
func (run *Run) Close() error {
	if run.waited {
		return nil
	}
	run.waited = true
	// Draining is only worth it for a turn that ran to completion. After a
	// deliberate stop the remaining output is unwanted, and a grandchild such
	// as the MCP tool server may still hold the write end, which would make the
	// read block indefinitely.
	if !run.stopped {
		_, _ = io.Copy(io.Discard, run.stdout)
	}
	if err := run.cmd.Wait(); err != nil && !run.stopped {
		if msg := run.Stderr(); msg != "" {
			return fmt.Errorf("claudecli: %w: %s", err, msg)
		}
		return fmt.Errorf("claudecli: %w", err)
	}
	return nil
}

// Kill terminates the process without waiting for it to finish the turn.
//
// The CLI spawns children of its own, notably the MCP tool server, so the whole
// process group goes down: killing only the CLI would leave them running and
// holding the output pipe open.
func (run *Run) Kill() {
	if run.cmd.Process == nil {
		return
	}
	if killGroup(run.cmd.Process.Pid) {
		return
	}
	_ = run.cmd.Process.Kill()
}
