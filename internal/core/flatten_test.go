package core

import (
	"errors"
	"strings"
	"testing"
)

func TestFlattenSingleUserMessageIsVerbatim(t *testing.T) {
	p, err := Flatten([]Message{{Role: RoleUser, Content: "why is the sky blue?"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.User != "why is the sky blue?" {
		t.Errorf("User = %q, want the message verbatim", p.User)
	}
	if p.System != "" {
		t.Errorf("System = %q, want empty", p.System)
	}
}

func TestFlattenCollectsSystemMessages(t *testing.T) {
	p, err := Flatten([]Message{
		{Role: RoleSystem, Content: "be terse"},
		{Role: RoleSystem, Content: "  "},
		{Role: RoleSystem, Content: "answer in Japanese"},
		{Role: RoleUser, Content: "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.System != "be terse\n\nanswer in Japanese" {
		t.Errorf("System = %q", p.System)
	}
	// A system message must not turn the single user turn into a transcript.
	if p.User != "hi" {
		t.Errorf("User = %q, want %q", p.User, "hi")
	}
}

func TestFlattenRendersHistory(t *testing.T) {
	p, err := Flatten([]Message{
		{Role: RoleUser, Content: "my name is Kazu"},
		{Role: RoleAssistant, Content: "noted"},
		{Role: RoleUser, Content: "what is my name?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.User, "my name is Kazu") || !strings.Contains(p.User, "noted") {
		t.Errorf("history missing from prompt:\n%s", p.User)
	}
	body, after, ok := strings.Cut(p.User, "</conversation>")
	if !ok {
		t.Fatalf("no conversation block:\n%s", p.User)
	}
	if strings.Contains(body, "what is my name?") {
		t.Errorf("the turn to answer must sit outside the history block:\n%s", p.User)
	}
	if !strings.Contains(after, "what is my name?") {
		t.Errorf("the turn to answer is missing:\n%s", p.User)
	}
}

func TestFlattenRendersToolResults(t *testing.T) {
	p, err := Flatten([]Message{
		{Role: RoleUser, Content: "weather in Tokyo?"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Function: ToolCallFunction{
			Name: "get_weather", Arguments: map[string]any{"city": "Tokyo"},
		}}}},
		{Role: RoleTool, ToolName: "get_weather", Content: `{"temp":22}`},
		{Role: RoleUser, Content: "and tomorrow?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"get_weather", `{"city":"Tokyo"}`, `{"temp":22}`} {
		if !strings.Contains(p.User, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p.User)
		}
	}
}

func TestFlattenRejectsEmptyConversation(t *testing.T) {
	for _, msgs := range [][]Message{
		nil,
		{{Role: RoleSystem, Content: "be terse"}},
	} {
		if _, err := Flatten(msgs); !errors.Is(err, ErrNoMessages) {
			t.Errorf("Flatten(%v) err = %v, want ErrNoMessages", msgs, err)
		}
	}
}

func TestFlattenIsDeterministic(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "a"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Function: ToolCallFunction{
			Name: "t", Arguments: map[string]any{"z": 1, "a": 2, "m": 3},
		}}}},
		{Role: RoleUser, Content: "b"},
	}
	first, _ := Flatten(msgs)
	for range 20 {
		got, _ := Flatten(msgs)
		if got != first {
			t.Fatalf("Flatten is not deterministic:\n%q\n%q", first.User, got.User)
		}
	}
}
