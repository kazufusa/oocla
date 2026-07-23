package mcpshim

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kazufusa/oocla/internal/core"
)

func TestConfigWithoutToolsIsEmpty(t *testing.T) {
	cfg, allowed, err := Config("/usr/bin/oocla", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != "" || allowed != nil {
		t.Errorf("cfg = %q allowed = %v, want nothing configured", cfg, allowed)
	}
}

func TestConfigDescribesTheShim(t *testing.T) {
	cfg, allowed, err := Config("/usr/bin/oocla", []core.Tool{weatherTool()})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(cfg), &got); err != nil {
		t.Fatalf("decode %q: %v", cfg, err)
	}
	srv, ok := got.MCPServers[ServerName]
	if !ok {
		t.Fatalf("no %q server in %q", ServerName, cfg)
	}
	if srv.Command != "/usr/bin/oocla" {
		t.Errorf("command = %q", srv.Command)
	}
	if !slices.Contains(srv.Args, "mcp-shim") {
		t.Errorf("args = %v", srv.Args)
	}
	// The tool definitions must not ride on the command line.
	if strings.Contains(strings.Join(srv.Args, " "), "get_weather") {
		t.Errorf("tool definitions leaked into args: %v", srv.Args)
	}

	tools, err := DecodeTools(srv.Env[ToolsEnv])
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Function.Name != "get_weather" {
		t.Errorf("round-tripped tools = %+v", tools)
	}

	if len(allowed) != 1 || allowed[0] != ToolPrefix+"get_weather" {
		t.Errorf("allowed = %v", allowed)
	}
}

func TestConfigRejectsUnusableTools(t *testing.T) {
	cases := map[string][]core.Tool{
		"empty name":     {{Function: core.ToolFunction{Name: ""}}},
		"bad characters": {{Function: core.ToolFunction{Name: "get weather"}}},
		"duplicate": {
			{Function: core.ToolFunction{Name: "f"}},
			{Function: core.ToolFunction{Name: "f"}},
		},
		"invalid schema": {{Function: core.ToolFunction{
			Name: "f", Parameters: json.RawMessage(`{`),
		}}},
	}
	for name, tools := range cases {
		if _, _, err := Config("/usr/bin/oocla", tools); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestDecodeToolsEmpty(t *testing.T) {
	tools, err := DecodeTools("")
	if err != nil || tools != nil {
		t.Errorf("DecodeTools(\"\") = %v, %v", tools, err)
	}
}

func TestDecodeToolsRejectsGarbage(t *testing.T) {
	if _, err := DecodeTools("not json"); err == nil {
		t.Error("want an error")
	}
}
