package core

import (
	"strings"
	"testing"
)

func TestFormatApply(t *testing.T) {
	var in GenerateInput
	Format{Schema: `{"type":"object"}`}.Apply(&in)
	if in.JSONSchema != `{"type":"object"}` || in.Prompt.System != "" {
		t.Errorf("schema mode = %+v, want the schema alone", in)
	}

	in = GenerateInput{}
	Format{JSONOnly: true}.Apply(&in)
	if in.JSONSchema != "" {
		t.Errorf("JSONSchema = %q, want json mode to use an instruction instead", in.JSONSchema)
	}
	if in.Prompt.System != jsonOnlyDirective {
		t.Errorf("System = %q", in.Prompt.System)
	}

	in = GenerateInput{Prompt: Prompt{System: "be terse"}}
	Format{JSONOnly: true}.Apply(&in)
	if !strings.HasPrefix(in.Prompt.System, "be terse") || !strings.HasSuffix(in.Prompt.System, jsonOnlyDirective) {
		t.Errorf("System = %q, want the client's prompt kept and the directive appended", in.Prompt.System)
	}
}

func TestUnwrapJSON(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"fenced with language": {"```json\n{\"a\":1}\n```", `{"a":1}`},
		"fenced bare":          {"```\n[1,2]\n```", `[1,2]`},
		"surrounding space":    {"\n  ```json\n{\"a\":1}\n```  \n", `{"a":1}`},
		"not fenced":           {`{"a":1}`, `{"a":1}`},
		"fence around prose":   {"```\nnot json at all\n```", "```\nnot json at all\n```"},
		"unterminated fence":   {"```json\n{\"a\":1}", "```json\n{\"a\":1}"},
		"lone fence":           {"```", "```"},
	}
	for name, c := range cases {
		if got := UnwrapJSON(c.in); got != c.want {
			t.Errorf("%s: UnwrapJSON(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}
