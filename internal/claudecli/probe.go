package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProbeMCPName reports whether this environment lets an MCP server named name
// start. Managed policies (e.g. allowedMcpServers) strip disallowed servers
// from `claude mcp list`, so the probe declares the server in a scratch
// .mcp.json and checks whether the listing still contains it. No model is
// involved, so the probe costs nothing.
func (r *Runner) ProbeMCPName(ctx context.Context, name, exe string) (bool, error) {
	dir, err := os.MkdirTemp("", scratchPrefix+"probe-")
	if err != nil {
		return false, fmt.Errorf("claudecli: probe directory: %w", err)
	}
	defer os.RemoveAll(dir)

	cfg := fmt.Sprintf(`{"mcpServers":{%q:{"command":%q,"args":["mcp-shim"]}}}`, name, exe)
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(cfg), 0o644); err != nil {
		return false, fmt.Errorf("claudecli: probe config: %w", err)
	}

	cmd := exec.CommandContext(ctx, r.bin(), "mcp", "list")
	cmd.Dir = dir
	cmd.Env = os.Environ()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("claudecli: %s mcp list: %w: %s", r.bin(), err, out.String())
	}
	return strings.Contains(out.String(), name+":"), nil
}

// ProbeModel reports the exact model id an alias like "opus" resolves to. The
// CLI resolves aliases locally and announces the result in the init event, so
// a zero-turn run reads it without any API call being made.
func (r *Runner) ProbeModel(ctx context.Context, model string, bare bool) (string, error) {
	run, err := r.Start(ctx, Options{Model: model, InitOnly: true, Bare: bare}, "x")
	if err != nil {
		return "", err
	}
	defer run.Stop()
	for {
		ev, err := run.Next()
		if err != nil {
			// The stream ended without an init event. Reap the process first:
			// stderr may still be filling, and a non-zero exit is the better
			// explanation anyway.
			if closeErr := run.Close(); closeErr != nil {
				return "", fmt.Errorf("claudecli: probing model %q: %w", model, closeErr)
			}
			return "", fmt.Errorf("claudecli: probing model %q: stream ended without an init event", model)
		}
		if ev.Kind == KindInit {
			if ev.Model == "" {
				return "", fmt.Errorf("claudecli: probing model %q: init event carries no model id", model)
			}
			return ev.Model, nil
		}
	}
}

// managedSettingsPaths are where managed policies live on disk. There is no
// CLI query for the policy itself, so reading the file is the only way to
// name the allowed servers; missing or unreadable files just mean no answer.
var managedSettingsPaths = []string{
	"/etc/claude-code/managed-settings.json",
	"/Library/Application Support/ClaudeCode/managed-settings.json",
}

// ManagedAllowedMCPServers reports the MCP server names a managed policy
// allows, and whether such a policy was found at all. Best effort: policies
// delivered by other means (registry, server-managed settings) are invisible
// here.
func ManagedAllowedMCPServers() (names []string, found bool) {
	for _, path := range managedSettingsPaths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var settings struct {
			AllowedMCPServers []struct {
				ServerName string `json:"serverName"`
			} `json:"allowedMcpServers"`
		}
		if err := json.Unmarshal(raw, &settings); err != nil {
			continue
		}
		if settings.AllowedMCPServers == nil {
			continue
		}
		for _, s := range settings.AllowedMCPServers {
			if s.ServerName != "" {
				names = append(names, s.ServerName)
			}
		}
		return names, true
	}
	return nil, false
}
