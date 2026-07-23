package core

import (
	"bytes"
	"encoding/json"
	"strings"
)

// SystemPrompt joins every system message in the conversation.
func SystemPrompt(msgs []Message) string {
	var parts []string
	for _, m := range msgs {
		if m.Role != RoleSystem {
			continue
		}
		if s := strings.TrimSpace(m.Content); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// CompactJSON normalizes whitespace so that equivalent JSON documents compare
// alike.
func CompactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
