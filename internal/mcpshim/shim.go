// Package mcpshim exposes a request's JSON tools to the claude CLI as an MCP
// server.
//
// The tools are never executed here. Both API dialects share the contract
// that the server returns tool calls and the client runs them, so oocla only
// needs the model to be able to *declare* a call. The parent process watches the CLI's output
// and stops the turn as soon as a tool call appears, which is normally before
// this server is ever asked to run anything.
package mcpshim

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kazufusa/oocla/internal/buildinfo"
	"github.com/kazufusa/oocla/internal/core"
)

// ServerName is the MCP server name. The CLI derives tool names from it:
// a tool "f" is offered to the model as "mcp__oocla__f".
const ServerName = "oocla"

// defaultProtocolVersion is used when the client does not name one.
const defaultProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes used here.
const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Serve runs the MCP stdio protocol over r and w until r is exhausted.
func Serve(r io.Reader, w io.Writer, tools []core.Tool) error {
	descriptors := describe(tools)
	br := bufio.NewReader(r)
	enc := json.NewEncoder(w)

	for {
		line, err := readLine(br)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(line) == "" {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			// A malformed line has no id to answer to; the CLI is the only
			// writer here, so drop it rather than guess.
			continue
		}
		// A request without an id is a notification: acknowledge nothing.
		if len(req.ID) == 0 {
			continue
		}
		if err := enc.Encode(respond(req, descriptors)); err != nil {
			return err
		}
	}
}

func respond(req rpcRequest, descriptors []toolDescriptor) rpcResponse {
	res := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		res.Result = map[string]any{
			"protocolVersion": negotiateVersion(req.Params),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": ServerName, "version": buildinfo.Get()},
		}
	case "ping":
		res.Result = map[string]any{}
	case "tools/list":
		res.Result = map[string]any{"tools": descriptors}
	case "tools/call":
		res.Result = callResult(req.Params)
	default:
		res.Error = &rpcError{Code: codeMethodNotFound, Message: "unsupported method " + req.Method}
	}
	return res
}

// callResult answers a tool call that should never have happened. Reaching this
// point means the parent did not stop the turn in time; saying so plainly is
// better than inventing a result the model would then reason from.
func callResult(params json.RawMessage) map[string]any {
	var p struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(params, &p)
	return map[string]any{
		"isError": true,
		"content": []map[string]any{{
			"type": "text",
			"text": fmt.Sprintf("tool %q is executed by the API client, not by the server; no result is available here", p.Name),
		}},
	}
}

// negotiateVersion echoes the client's protocol version. The shim implements
// only the handful of methods every revision shares, so agreeing with the
// client is safer than insisting on one version.
func negotiateVersion(params json.RawMessage) string {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &p); err == nil && p.ProtocolVersion != "" {
		return p.ProtocolVersion
	}
	return defaultProtocolVersion
}

func describe(tools []core.Tool) []toolDescriptor {
	out := make([]toolDescriptor, 0, len(tools))
	for _, t := range tools {
		schema := t.Function.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, toolDescriptor{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: schema,
		})
	}
	return out
}

// readLine reads one newline-terminated message. Tool schemas can be large, so
// the line length is not bounded.
func readLine(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := br.ReadString('\n')
		sb.WriteString(chunk)
		if err == nil {
			return sb.String(), nil
		}
		if err == io.EOF && sb.Len() > 0 {
			return sb.String(), nil
		}
		return "", err
	}
}
