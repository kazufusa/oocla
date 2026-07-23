package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNoMessages is returned when a request carries nothing to answer.
var ErrNoMessages = errors.New("messages is required")

// Flatten reduces a conversation to a single turn.
//
// The claude CLI charges one API call per user message on its stdin, so
// replaying a long history natively would cost one call per turn. oocla
// instead folds the history into one user message. This is the cache-miss path;
// once a conversation has a cached session, only the new user message is sent.
//
// The common cache-miss case is a conversation whose only message is a user
// message. That one is passed through verbatim, with no added framing at all.
func Flatten(msgs []Message) (Prompt, error) {
	var rest []Message
	for _, m := range msgs {
		if m.Role != RoleSystem {
			rest = append(rest, m)
		}
	}

	p := Prompt{System: SystemPrompt(msgs)}
	switch {
	case len(rest) == 0:
		return Prompt{}, ErrNoMessages
	case len(rest) == 1 && rest[0].Role == RoleUser:
		p.User = rest[0].Content
	default:
		p.User = renderTranscript(rest)
	}
	return p, nil
}

// renderTranscript writes the history as a labelled transcript followed by the
// turn to answer. The labels are the minimum framing that keeps roles apart;
// nothing else is added, because oocla must not inject instructions of its own.
func renderTranscript(msgs []Message) string {
	last := msgs[len(msgs)-1]
	head := msgs[:len(msgs)-1]

	var b strings.Builder
	if len(head) > 0 {
		b.WriteString("<conversation>\n")
		for _, m := range head {
			writeTurn(&b, m)
		}
		b.WriteString("</conversation>\n\n")
	}
	writeTurn(&b, last)
	return strings.TrimRight(b.String(), "\n")
}

// writeTurn renders one message. Tool results carry the tool name so the model
// can tell several results apart.
func writeTurn(b *strings.Builder, m Message) {
	if m.Role == RoleTool && m.ToolName != "" {
		fmt.Fprintf(b, "<%s name=%q>\n", m.Role, m.ToolName)
	} else {
		fmt.Fprintf(b, "<%s>\n", m.Role)
	}
	if m.Content != "" {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	for _, tc := range m.ToolCalls {
		fmt.Fprintf(b, "called %s with %s\n", tc.Function.Name, encodeArgs(tc.Function.Arguments))
	}
	fmt.Fprintf(b, "</%s>\n", m.Role)
}

// encodeArgs renders tool call arguments. encoding/json sorts map keys, so the
// output is stable, which matters because these strings feed the session cache
// key.
func encodeArgs(args map[string]any) string {
	b, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(b)
}
