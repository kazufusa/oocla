// Package claudecli builds and runs `claude` CLI invocations and parses its
// stream-json output.
//
// oocla never touches authentication: whatever credentials the `claude` binary
// already has are inherited from the environment. No auth flag is ever passed,
// and --bare is deliberately avoided because it restricts auth to
// ANTHROPIC_API_KEY / apiKeyHelper and would break OAuth setups.
package claudecli

import (
	"errors"
	"fmt"
	"strings"
)

// Options describes one `claude -p` invocation.
type Options struct {
	// Model is the value for --model: an alias like "opus" or an exact model id.
	Model string

	// SystemPrompt replaces the Claude Code system prompt entirely. The empty
	// string is meaningful and still passed: it is how the default prompt is
	// suppressed when the caller supplied no system message.
	SystemPrompt string

	// MCPConfigJSON is an inline MCP configuration. When set, it is the only
	// MCP configuration the CLI is allowed to see.
	MCPConfigJSON string

	// AllowedTools lists tool names the session may call without a prompt.
	// Only MCP tool names belong here; built-in tools are always off.
	AllowedTools []string

	// JSONSchema constrains the final answer to a JSON Schema.
	JSONSchema string

	// Effort is the reasoning effort level: low, medium, high, xhigh or max.
	// Empty leaves the model's default in place.
	Effort string

	// Partial requests token-level streaming events.
	Partial bool

	// Bare asks the CLI for its minimal mode, which is the only way to stop it
	// injecting a base identity prompt and a context block naming the logged-in
	// user and today's date.
	//
	// It comes at a price the caller has to accept: in this mode the CLI reads
	// credentials only from ANTHROPIC_API_KEY or an apiKeyHelper, never from an
	// OAuth login. oocla does not manage credentials either way; it passes the
	// flag through and lets the CLI's own rules apply.
	Bare bool
}

// baseArgs are the flags that make `claude` behave as a plain LLM rather than
// a coding agent. Every invocation gets them.
//
//	--tools ""                   no built-in tools at all
//	--setting-sources ""         ignore user/project/local settings
//	--disable-slash-commands     no skills
//	--strict-mcp-config          ignore MCP servers configured elsewhere
//	--no-session-persistence     leave nothing on disk after the turn
func baseArgs() []string {
	return []string{
		"-p",
		"--tools", "",
		"--setting-sources", "",
		"--disable-slash-commands",
		"--strict-mcp-config",
		"--no-session-persistence",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
	}
}

var errNoModel = errors.New("model is required")

// Args returns the argv (excluding the binary name) for this invocation.
func (o Options) Args() ([]string, error) {
	if strings.TrimSpace(o.Model) == "" {
		return nil, errNoModel
	}
	if err := checkFlagValue("model", o.Model); err != nil {
		return nil, err
	}

	args := baseArgs()
	if o.Bare {
		args = append(args, "--bare")
	}
	args = append(args, "--model", o.Model)
	args = append(args, "--system-prompt", o.SystemPrompt)
	if o.MCPConfigJSON != "" {
		args = append(args, "--mcp-config", o.MCPConfigJSON)
	}
	for _, t := range o.AllowedTools {
		if err := checkFlagValue("allowedTools", t); err != nil {
			return nil, err
		}
		args = append(args, "--allowedTools", t)
	}
	if o.JSONSchema != "" {
		args = append(args, "--json-schema", o.JSONSchema)
	}
	if o.Effort != "" {
		if err := checkFlagValue("effort", o.Effort); err != nil {
			return nil, err
		}
		args = append(args, "--effort", o.Effort)
	}
	if o.Partial {
		args = append(args, "--include-partial-messages")
	}
	return args, nil
}

// checkFlagValue rejects values that would be parsed as another flag. These
// values come from HTTP request bodies; there is no shell involved, but a value
// starting with "-" would still be read as an option by the CLI.
func checkFlagValue(name, v string) error {
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("%s: %q must not start with %q", name, v, "-")
	}
	return nil
}
