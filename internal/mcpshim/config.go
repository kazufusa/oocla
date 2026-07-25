package mcpshim

import (
	"encoding/json"
	"fmt"

	"github.com/kazufusa/oocla/internal/core"
)

// ToolsEnv is the environment variable carrying the tool definitions to the
// shim subprocess. Passing them out of band keeps them off the command line,
// which the CLI logs and which has a length limit.
const ToolsEnv = "OOCLA_TOOLS"

// ToolPrefix is what the CLI prepends to a tool name from this server under
// its default name.
const ToolPrefix = "mcp__" + ServerName + "__"

// Prefix is what the CLI prepends to a tool name from a server named server.
func Prefix(server string) string { return "mcp__" + server + "__" }

// ValidServerName reports whether server can be used as the shim's MCP server
// name. The name ends up in tool identifiers and --allowedTools, so the
// character set stays as narrow as tool names.
func ValidServerName(server string) bool { return server != "" && validName(server) }

// Config describes the MCP server that exposes tools, as the JSON string for
// --mcp-config, together with the tool names to pass to --allowedTools.
//
// exe is the path to the oocla binary; the shim is oocla itself under a
// different subcommand, so there is nothing extra to install. server is the
// MCP server name to register under, normally ServerName.
func Config(exe, server string, tools []core.Tool) (mcpConfig string, allowed []string, err error) {
	if len(tools) == 0 {
		return "", nil, nil
	}
	if !ValidServerName(server) {
		return "", nil, fmt.Errorf("mcpshim: server name %q may only contain letters, digits, underscore and hyphen", server)
	}
	if err := validate(tools); err != nil {
		return "", nil, err
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return "", nil, err
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			server: map[string]any{
				"command": exe,
				"args":    []string{"mcp-shim"},
				"env":     map[string]string{ToolsEnv: string(encoded)},
			},
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	for _, t := range tools {
		allowed = append(allowed, Prefix(server)+t.Function.Name)
	}
	return string(raw), allowed, nil
}

// DecodeTools reads the tool definitions the parent passed in.
func DecodeTools(encoded string) ([]core.Tool, error) {
	if encoded == "" {
		return nil, nil
	}
	var tools []core.Tool
	if err := json.Unmarshal([]byte(encoded), &tools); err != nil {
		return nil, fmt.Errorf("mcpshim: %s: %w", ToolsEnv, err)
	}
	return tools, nil
}

// validate rejects tool definitions that cannot be represented as MCP tools.
// Names travel through a tool identifier and end up in --allowedTools, so the
// character set has to stay narrow.
func validate(tools []core.Tool) error {
	seen := make(map[string]bool, len(tools))
	for i, t := range tools {
		name := t.Function.Name
		if name == "" {
			return fmt.Errorf("tools[%d]: name is required", i)
		}
		if !validName(name) {
			return fmt.Errorf("tools[%d]: name %q may only contain letters, digits, underscore and hyphen", i, name)
		}
		if seen[name] {
			return fmt.Errorf("tools[%d]: duplicate tool name %q", i, name)
		}
		seen[name] = true
		if len(t.Function.Parameters) > 0 && !json.Valid(t.Function.Parameters) {
			return fmt.Errorf("tools[%d]: parameters is not valid JSON", i)
		}
	}
	return nil
}

func validName(s string) bool {
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
