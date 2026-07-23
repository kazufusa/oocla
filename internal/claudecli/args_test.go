package claudecli

import (
	"slices"
	"strings"
	"testing"
)

// argValue returns the value that follows flag in args.
func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestArgsDisablesAgentBehaviour(t *testing.T) {
	args, err := Options{Model: "opus"}.Args()
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := argValue(args, "--tools"); !ok || v != "" {
		t.Errorf("--tools = %q, %v; want empty string present", v, ok)
	}
	if v, ok := argValue(args, "--setting-sources"); !ok || v != "" {
		t.Errorf("--setting-sources = %q, %v; want empty string present", v, ok)
	}
	for _, want := range []string{"-p", "--disable-slash-commands", "--strict-mcp-config", "--verbose"} {
		if !slices.Contains(args, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
	if v, _ := argValue(args, "--output-format"); v != "stream-json" {
		t.Errorf("--output-format = %q", v)
	}
	if v, _ := argValue(args, "--input-format"); v != "stream-json" {
		t.Errorf("--input-format = %q", v)
	}
	// --bare restricts auth to API keys and would break OAuth setups, so it is
	// never on unless the operator asks for it.
	if slices.Contains(args, "--bare") {
		t.Error("args must not contain --bare by default")
	}
}

func TestArgsBareIsOptIn(t *testing.T) {
	args, err := Options{Model: "opus", Bare: true}.Args()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "--bare") {
		t.Errorf("args = %v, want --bare", args)
	}
}

func TestArgsAlwaysPassesSystemPromptEvenWhenEmpty(t *testing.T) {
	args, err := Options{Model: "opus", SystemPrompt: ""}.Args()
	if err != nil {
		t.Fatal(err)
	}
	v, ok := argValue(args, "--system-prompt")
	if !ok {
		t.Fatalf("--system-prompt is absent: %v", args)
	}
	if v != "" {
		t.Errorf("--system-prompt = %q, want empty", v)
	}
}

func TestArgsSystemPrompt(t *testing.T) {
	args, _ := Options{Model: "opus", SystemPrompt: "be terse"}.Args()
	if v, _ := argValue(args, "--system-prompt"); v != "be terse" {
		t.Errorf("--system-prompt = %q", v)
	}
}

func TestArgsRequiresModel(t *testing.T) {
	if _, err := (Options{}).Args(); err == nil {
		t.Fatal("want error when model is empty")
	}
	if _, err := (Options{Model: "   "}).Args(); err == nil {
		t.Fatal("want error when model is blank")
	}
}

func TestArgsRejectsFlagLikeValues(t *testing.T) {
	cases := []Options{
		{Model: "--dangerously-skip-permissions"},
		{Model: "opus", AllowedTools: []string{"-x"}},
	}
	for _, o := range cases {
		if _, err := o.Args(); err == nil {
			t.Errorf("%+v: want error for flag-like value", o)
		}
	}
}

// Nothing may survive a turn on disk, so every invocation carries the flag.
func TestArgsAlwaysDisableSessionPersistence(t *testing.T) {
	args, _ := Options{Model: "opus"}.Args()
	if !slices.Contains(args, "--no-session-persistence") {
		t.Error("want --no-session-persistence on every invocation")
	}
}

func TestArgsOptionalFlagsAreOmitted(t *testing.T) {
	args, _ := Options{Model: "opus"}.Args()
	for _, unwanted := range []string{"--resume", "--mcp-config", "--json-schema", "--include-partial-messages", "--allowedTools", "--effort"} {
		if slices.Contains(args, unwanted) {
			t.Errorf("args should not contain %q: %v", unwanted, args)
		}
	}
}

func TestArgsOptionalFlagsArePassed(t *testing.T) {
	args, err := Options{
		Model:         "haiku",
		MCPConfigJSON: `{"mcpServers":{}}`,
		AllowedTools:  []string{"mcp__oocla__get_weather"},
		JSONSchema:    `{"type":"object"}`,
		Effort:        "high",
		Partial:       true,
	}.Args()
	if err != nil {
		t.Fatal(err)
	}
	for flag, want := range map[string]string{
		"--mcp-config":   `{"mcpServers":{}}`,
		"--allowedTools": "mcp__oocla__get_weather",
		"--json-schema":  `{"type":"object"}`,
		"--effort":       "high",
	} {
		if v, ok := argValue(args, flag); !ok || v != want {
			t.Errorf("%s = %q, %v; want %q", flag, v, ok, want)
		}
	}
	if !slices.Contains(args, "--include-partial-messages") {
		t.Error("want --include-partial-messages")
	}
}

func TestArgsIsDeterministic(t *testing.T) {
	o := Options{Model: "opus", SystemPrompt: "x", AllowedTools: []string{"a", "b"}}
	first, _ := o.Args()
	second, _ := o.Args()
	if strings.Join(first, "\x00") != strings.Join(second, "\x00") {
		t.Errorf("Args() is not deterministic:\n%v\n%v", first, second)
	}
}
