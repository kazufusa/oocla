package core

import "testing"

func TestSystemPrompt(t *testing.T) {
	got := SystemPrompt([]Message{
		{Role: RoleSystem, Content: "a"},
		{Role: RoleUser, Content: "ignored"},
		{Role: RoleSystem, Content: " \n "},
		{Role: RoleSystem, Content: "b"},
	})
	if got != "a\n\nb" {
		t.Errorf("SystemPrompt = %q", got)
	}
}
