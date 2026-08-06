//go:build e2e

// Package e2e runs the real oocla binary against the real claude CLI.
//
// These tests spend money and need an authenticated claude installation, so
// they are behind a build tag and are not part of `make check`. Run them with
// `make e2e`.
package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// model is the cheapest model that still exercises every path.
const model = "haiku"

// server is a running oocla process.
type server struct {
	base string
	cmd  *exec.Cmd
	log  *bytes.Buffer
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func start(t *testing.T) *server {
	t.Helper()
	bin := os.Getenv("OOCLA_BIN")
	if bin == "" {
		t.Skip("OOCLA_BIN is not set; run these through `make e2e`")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	log := &bytes.Buffer{}
	cmd := exec.Command(bin, "serve", "-addr", addr)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %s: %v", bin, err)
	}
	s := &server{base: "http://" + addr, cmd: cmd, log: log}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
		}
		if t.Failed() {
			t.Logf("server log:\n%s", log.String())
		}
	})
	s.waitReady(t)
	return s
}

func (s *server) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(s.base + "/api/version")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not come up:\n%s", s.log.String())
}

// post sends a JSON body and returns the response body, failing on a non-200.
func (s *server) post(t *testing.T, path, body string) []byte {
	t.Helper()
	resp, err := http.Post(s.base+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	out := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d body %s", path, resp.StatusCode, out)
	}
	return out
}

func (s *server) get(t *testing.T, path string) []byte {
	t.Helper()
	resp, err := http.Get(s.base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	out := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", path, resp.StatusCode, out)
	}
	return out
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// lines streams a request and returns each decoded response line.
func (s *server) lines(t *testing.T, path, body string) []map[string]any {
	t.Helper()
	resp, err := http.Post(s.base+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d", path, resp.StatusCode)
	}
	var out []map[string]any
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "data: ")
		if line == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

func chatBody(t *testing.T, extra map[string]any, messages ...map[string]any) string {
	t.Helper()
	body := map[string]any{"model": model, "messages": messages}
	for k, v := range extra {
		body[k] = v
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func user(text string) map[string]any {
	return map[string]any{"role": "user", "content": text}
}

func TestCatalog(t *testing.T) {
	s := start(t)

	if got := decode[map[string]string](t, s.get(t, "/api/version"))["version"]; got == "" {
		t.Error("no version reported")
	}

	tags := decode[struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"models"`
	}](t, s.get(t, "/api/tags"))
	if len(tags.Models) == 0 {
		t.Fatal("no models advertised")
	}
	// The tag is ":latest" until the startup probe resolves the real version,
	// then e.g. ":4.5"; either can win the race with this test.
	var found bool
	for _, m := range tags.Models {
		if strings.HasPrefix(m.Name, model+":") {
			found = true
		}
		if m.Digest == "" || m.Size == 0 {
			t.Errorf("%s: incomplete entry", m.Name)
		}
	}
	if !found {
		t.Errorf("%s is not advertised", model)
	}

	show := decode[struct {
		Capabilities []string `json:"capabilities"`
	}](t, s.post(t, "/api/show", `{"model":"`+model+`"}`))
	if len(show.Capabilities) == 0 {
		t.Error("no capabilities reported")
	}
}

type chatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		Thinking  string `json:"thinking"`
		ToolCalls []struct {
			Function struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

func TestChat(t *testing.T) {
	s := start(t)
	got := decode[chatResponse](t, s.post(t, "/api/chat", chatBody(t,
		map[string]any{"stream": false},
		map[string]any{"role": "system", "content": "Answer with a single word, no punctuation."},
		user("What is the capital of Japan?"))))

	if !strings.Contains(got.Message.Content, "Tokyo") {
		t.Errorf("content = %q, want it to name Tokyo", got.Message.Content)
	}
	if got.Message.Role != "assistant" || !got.Done || got.DoneReason != "stop" {
		t.Errorf("envelope = %+v", got)
	}
	if got.PromptEvalCount == 0 || got.EvalCount == 0 {
		t.Errorf("token counts = %d/%d", got.PromptEvalCount, got.EvalCount)
	}
	// Thinking is withheld unless the client asks for it.
	if got.Message.Thinking != "" {
		t.Errorf("thinking = %q, want it withheld", got.Message.Thinking)
	}
}

func TestChatStreaming(t *testing.T) {
	s := start(t)
	lines := s.lines(t, "/api/chat", chatBody(t, nil,
		user("Count from 1 to 5, one number per line, nothing else.")))
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want chunks and a terminator", len(lines))
	}

	var joined string
	for _, l := range lines[:len(lines)-1] {
		msg, _ := l["message"].(map[string]any)
		content, _ := msg["content"].(string)
		joined += content
		if done, _ := l["done"].(bool); done {
			t.Error("a content chunk is marked done")
		}
	}
	last := lines[len(lines)-1]
	if done, _ := last["done"].(bool); !done {
		t.Errorf("last line is not the terminator: %v", last)
	}
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		if !strings.Contains(joined, n) {
			t.Errorf("streamed content is missing %q:\n%s", n, joined)
		}
	}
}

// A follow-up turn must see the earlier ones, whichever path it takes.
func TestChatRemembersTheConversation(t *testing.T) {
	s := start(t)
	first := decode[chatResponse](t, s.post(t, "/api/chat", chatBody(t,
		map[string]any{"stream": false},
		user("Remember the number 7391. Acknowledge in one short sentence."))))

	second := decode[chatResponse](t, s.post(t, "/api/chat", chatBody(t,
		map[string]any{"stream": false},
		user("Remember the number 7391. Acknowledge in one short sentence."),
		map[string]any{"role": "assistant", "content": first.Message.Content},
		user("What number did I ask you to remember? Reply with only the digits."))))

	if !strings.Contains(second.Message.Content, "7391") {
		t.Errorf("content = %q, want the number recalled", second.Message.Content)
	}
}

const weatherTool = `{"type":"function","function":{
	"name":"get_current_weather",
	"description":"Get the current weather for a city",
	"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`

func TestToolCallRoundTrip(t *testing.T) {
	s := start(t)
	called := decode[chatResponse](t, s.post(t, "/api/chat", `{"model":"`+model+`","stream":false,
		"messages":[{"role":"user","content":"What is the weather in Tokyo? Use the tool."}],
		"tools":[`+weatherTool+`]}`))

	if len(called.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls = %+v", called.Message.ToolCalls)
	}
	fn := called.Message.ToolCalls[0].Function
	if fn.Name != "get_current_weather" {
		t.Errorf("name = %q, want the client-facing name", fn.Name)
	}
	if city, _ := fn.Arguments["city"].(string); !strings.EqualFold(city, "Tokyo") {
		t.Errorf("arguments = %+v", fn.Arguments)
	}

	answered := decode[chatResponse](t, s.post(t, "/api/chat", `{"model":"`+model+`","stream":false,
		"messages":[
			{"role":"user","content":"What is the weather in Tokyo? Use the tool."},
			{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_current_weather","arguments":{"city":"Tokyo"}}}]},
			{"role":"tool","tool_name":"get_current_weather","content":"{\"temp_c\":22,\"condition\":\"light rain\"}"}],
		"tools":[`+weatherTool+`]}`))
	if len(answered.Message.ToolCalls) != 0 {
		t.Errorf("the model called a tool again instead of answering: %+v", answered.Message.ToolCalls)
	}
	if !strings.Contains(answered.Message.Content, "22") {
		t.Errorf("content = %q, want it to use the tool result", answered.Message.Content)
	}
}

// The whole point of the design: the model gets the client's JSON tools and
// nothing else.
func TestBuiltInToolsAreDisabled(t *testing.T) {
	s := start(t)
	got := decode[chatResponse](t, s.post(t, "/api/chat", chatBody(t,
		map[string]any{"stream": false},
		user("Use the Bash tool to print the current directory. If you have no such tool, reply with exactly CANNOT."))))

	if len(got.Message.ToolCalls) != 0 {
		t.Fatalf("a built-in tool was reachable: %+v", got.Message.ToolCalls)
	}
	if !strings.Contains(strings.ToUpper(got.Message.Content), "CANNOT") {
		t.Errorf("content = %q, want the model to report having no tools", got.Message.Content)
	}
}

func TestStructuredOutput(t *testing.T) {
	s := start(t)

	schema := decode[chatResponse](t, s.post(t, "/api/chat", `{"model":"`+model+`","stream":false,
		"format":{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"]},
		"messages":[{"role":"user","content":"What is 6 times 7?"}]}`))
	var typed struct {
		Answer int `json:"answer"`
	}
	if err := json.Unmarshal([]byte(schema.Message.Content), &typed); err != nil {
		t.Fatalf("content %q is not the requested object: %v", schema.Message.Content, err)
	}
	if typed.Answer != 42 {
		t.Errorf("answer = %d", typed.Answer)
	}

	plain := decode[chatResponse](t, s.post(t, "/api/chat", chatBody(t,
		map[string]any{"stream": false, "format": "json"},
		user("Give me two colours as a JSON array."))))
	if !json.Valid([]byte(plain.Message.Content)) {
		t.Errorf("json mode produced %q, which is not JSON", plain.Message.Content)
	}
}

func TestThinking(t *testing.T) {
	s := start(t)
	got := decode[chatResponse](t, s.post(t, "/api/chat", chatBody(t,
		map[string]any{"stream": false, "think": true},
		user("What is 17 times 23? Answer with only the number."))))
	if got.Message.Thinking == "" {
		t.Error("think:true returned no thinking")
	}
	if !strings.Contains(got.Message.Content, "391") {
		t.Errorf("content = %q", got.Message.Content)
	}
}

func TestStopSequence(t *testing.T) {
	s := start(t)
	got := decode[chatResponse](t, s.post(t, "/api/chat", chatBody(t,
		map[string]any{"stream": false, "options": map[string]any{"stop": []string{"3"}}},
		user("Count from 1 to 5, one number per line, nothing else."))))
	if strings.Contains(got.Message.Content, "4") {
		t.Errorf("content = %q, want it cut at the stop sequence", got.Message.Content)
	}
}

func TestGenerate(t *testing.T) {
	s := start(t)
	whole := decode[struct {
		Response string `json:"response"`
		Done     bool   `json:"done"`
	}](t, s.post(t, "/api/generate",
		`{"model":"`+model+`","stream":false,"system":"Answer with a single word.","prompt":"Capital of France?"}`))
	if !strings.Contains(whole.Response, "Paris") {
		t.Errorf("response = %q", whole.Response)
	}
	if !whole.Done {
		t.Error("done is false")
	}

	lines := s.lines(t, "/api/generate", `{"model":"`+model+`","prompt":"Say hello in one word."}`)
	if len(lines) < 2 {
		t.Fatalf("streaming returned %d lines", len(lines))
	}
	if done, _ := lines[len(lines)-1]["done"].(bool); !done {
		t.Error("the stream did not terminate")
	}
}

func TestOpenAICompatibility(t *testing.T) {
	s := start(t)

	models := decode[struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}](t, s.get(t, "/v1/models"))
	if models.Object != "list" || len(models.Data) == 0 {
		t.Fatalf("/v1/models = %+v", models)
	}

	completion := decode[struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}](t, s.post(t, "/v1/chat/completions",
		`{"model":"`+model+`","messages":[{"role":"user","content":"Capital of Italy? One word."}]}`))

	if completion.Object != "chat.completion" || len(completion.Choices) != 1 {
		t.Fatalf("completion = %+v", completion)
	}
	if !strings.Contains(completion.Choices[0].Message.Content, "Rome") {
		t.Errorf("content = %q", completion.Choices[0].Message.Content)
	}
	if completion.Choices[0].FinishReason != "stop" || completion.Usage.TotalTokens == 0 {
		t.Errorf("completion = %+v", completion)
	}

	events := s.lines(t, "/v1/chat/completions",
		`{"model":"`+model+`","stream":true,"messages":[{"role":"user","content":"Say hello."}]}`)
	if len(events) < 2 {
		t.Fatalf("SSE returned %d events", len(events))
	}
	var joined, finish string
	for _, e := range events {
		choices, _ := e["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		c, _ := choices[0].(map[string]any)
		if delta, ok := c["delta"].(map[string]any); ok {
			text, _ := delta["content"].(string)
			joined += text
		}
		if fr, ok := c["finish_reason"].(string); ok && fr != "" {
			finish = fr
		}
	}
	if joined == "" {
		t.Error("no content arrived over SSE")
	}
	if finish != "stop" {
		t.Errorf("finish_reason = %q", finish)
	}
}

func TestManagementEndpoints(t *testing.T) {
	s := start(t)

	ps := decode[struct {
		Models []any `json:"models"`
	}](t, s.get(t, "/api/ps"))
	if len(ps.Models) != 0 {
		t.Errorf("/api/ps = %+v, want nothing resident", ps.Models)
	}

	pull := decode[map[string]string](t, s.post(t, "/api/pull", `{"model":"`+model+`","stream":false}`))
	if pull["status"] != "success" {
		t.Errorf("/api/pull = %v", pull)
	}

	resp, err := http.Post(s.base+"/api/embeddings", "application/json",
		strings.NewReader(`{"model":"`+model+`","input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("/api/embeddings status = %d, want 501", resp.StatusCode)
	}
}

func TestUnknownModelIsRejected(t *testing.T) {
	s := start(t)
	resp, err := http.Post(s.base+"/api/chat", "application/json",
		strings.NewReader(`{"model":"llama3","stream":false,"messages":[{"role":"user","content":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
