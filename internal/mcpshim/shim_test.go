package mcpshim

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kazufusa/oocla/internal/core"
)

func weatherTool() core.Tool {
	return core.Tool{Type: "function", Function: core.ToolFunction{
		Name:        "get_weather",
		Description: "look up the weather",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
	}}
}

// exchange feeds requests through the shim and returns the decoded responses.
func exchange(t *testing.T, tools []core.Tool, requests ...string) []map[string]any {
	t.Helper()
	var out strings.Builder
	if err := Serve(strings.NewReader(strings.Join(requests, "\n")+"\n"), &out, tools); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		responses = append(responses, m)
	}
	return responses
}

func TestInitialize(t *testing.T) {
	res := exchange(t, nil, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if len(res) != 1 {
		t.Fatalf("responses = %+v", res)
	}
	result, _ := res[0]["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result: %+v", res[0])
	}
	if got := result["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want the client's version echoed", got)
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities = %+v, want tools advertised", caps)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != ServerName {
		t.Errorf("serverInfo = %+v", info)
	}
}

func TestInitializeWithoutVersion(t *testing.T) {
	res := exchange(t, nil, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	result := res[0]["result"].(map[string]any)
	if result["protocolVersion"] != defaultProtocolVersion {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}
}

func TestToolsList(t *testing.T) {
	res := exchange(t, []core.Tool{weatherTool()}, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	result := res[0]["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %+v", result["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "get_weather" || tool["description"] != "look up the weather" {
		t.Errorf("tool = %+v", tool)
	}
	schema, _ := tool["inputSchema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("inputSchema = %+v", schema)
	}
}

// A tool with no declared parameters still needs a schema, or the CLI rejects
// the listing.
func TestToolsListSuppliesSchemaWhenMissing(t *testing.T) {
	tool := core.Tool{Function: core.ToolFunction{Name: "now"}}
	res := exchange(t, []core.Tool{tool}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := res[0]["result"].(map[string]any)["tools"].([]any)
	schema, _ := tools[0].(map[string]any)["inputSchema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("inputSchema = %+v, want a usable empty object schema", schema)
	}
}

// Execution belongs to the API client. If a call ever gets this far, the shim
// must say so rather than invent a result the model would reason from.
func TestToolsCallRefusesToInventAResult(t *testing.T) {
	res := exchange(t, []core.Tool{weatherTool()},
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_weather","arguments":{"city":"Tokyo"}}}`)
	result := res[0]["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("isError = %v, want true", result["isError"])
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("content = %+v", result["content"])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "get_weather") {
		t.Errorf("text = %q, want the tool named", text)
	}
}

func TestNotificationsGetNoResponse(t *testing.T) {
	res := exchange(t, nil, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(res) != 0 {
		t.Errorf("responses = %+v, want none for a notification", res)
	}
}

func TestUnknownMethod(t *testing.T) {
	res := exchange(t, nil, `{"jsonrpc":"2.0","id":9,"method":"resources/list"}`)
	errObj, _ := res[0]["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("response = %+v, want an error", res[0])
	}
	if int(errObj["code"].(float64)) != codeMethodNotFound {
		t.Errorf("code = %v", errObj["code"])
	}
}

func TestPing(t *testing.T) {
	res := exchange(t, nil, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if _, ok := res[0]["result"]; !ok {
		t.Errorf("response = %+v", res[0])
	}
}

func TestMalformedLineIsSkipped(t *testing.T) {
	res := exchange(t, nil, `{not json}`, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if len(res) != 1 {
		t.Fatalf("responses = %+v", res)
	}
	if res[0]["id"] != float64(1) {
		t.Errorf("the surviving response is not the valid request: %+v", res[0])
	}
}

func TestIDIsEchoedVerbatim(t *testing.T) {
	res := exchange(t, nil, `{"jsonrpc":"2.0","id":"abc","method":"ping"}`)
	if res[0]["id"] != "abc" {
		t.Errorf("id = %v, want the string id preserved", res[0]["id"])
	}
}
