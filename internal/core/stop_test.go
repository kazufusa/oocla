package core

import "testing"

func TestTrimAtStop(t *testing.T) {
	cases := map[string]struct {
		in    string
		stops []string
		want  string
	}{
		"no stops":           {"hello", nil, "hello"},
		"not present":        {"hello", []string{"END"}, "hello"},
		"present":            {"hello END world", []string{"END"}, "hello "},
		"at start":           {"ENDhello", []string{"END"}, ""},
		"earliest wins":      {"a STOP b END c", []string{"END", "STOP"}, "a "},
		"empty stop ignored": {"hello", []string{""}, "hello"},
	}
	for name, c := range cases {
		if got := TrimAtStop(c.in, c.stops); got != c.want {
			t.Errorf("%s: TrimAtStop(%q, %v) = %q, want %q", name, c.in, c.stops, got, c.want)
		}
	}
}

// feed runs chunks through a trimmer and returns everything it let through.
func feed(t *testing.T, stops []string, chunks ...string) (string, bool) {
	t.Helper()
	tr := newStopTrimmer(stops)
	var out string
	for _, c := range chunks {
		emitted, stop := tr.Write(c)
		out += emitted
		if stop {
			return out, true
		}
	}
	return out + tr.Flush(), false
}

func TestStopTrimmerPassesTextThrough(t *testing.T) {
	got, stopped := feed(t, []string{"END"}, "hello ", "world")
	if got != "hello world" || stopped {
		t.Errorf("got %q, stopped = %v", got, stopped)
	}
}

// A stop sequence can straddle a chunk boundary, so the trimmer has to hold
// back any tail that might still turn into one.
func TestStopTrimmerHandlesSplitSequences(t *testing.T) {
	got, stopped := feed(t, []string{"END"}, "keep this EN", "D and drop this")
	if !stopped {
		t.Fatal("the split sequence was not recognised")
	}
	if got != "keep this " {
		t.Errorf("got %q", got)
	}
}

func TestStopTrimmerReleasesFalseAlarms(t *testing.T) {
	// "EN" looks like the start of "END" but turns out to be "ENOUGH".
	got, stopped := feed(t, []string{"END"}, "it is EN", "OUGH")
	if stopped {
		t.Fatal("stopped on a sequence that never completed")
	}
	if got != "it is ENOUGH" {
		t.Errorf("got %q, want the held-back tail released", got)
	}
}

func TestStopTrimmerReleasesTailAtEndOfStream(t *testing.T) {
	got, stopped := feed(t, []string{"END"}, "trailing EN")
	if stopped {
		t.Fatal("stopped without a complete sequence")
	}
	if got != "trailing EN" {
		t.Errorf("got %q", got)
	}
}

func TestStopTrimmerEmitsNothingAfterStopping(t *testing.T) {
	tr := newStopTrimmer([]string{"END"})
	if _, stop := tr.Write("a END b"); !stop {
		t.Fatal("want stop")
	}
	if got, stop := tr.Write("more"); got != "" || !stop {
		t.Errorf("Write after stop = %q, %v", got, stop)
	}
	if got := tr.Flush(); got != "" {
		t.Errorf("Flush after stop = %q", got)
	}
}

func TestStopTrimmerInactiveWithoutStops(t *testing.T) {
	if newStopTrimmer(nil).active() {
		t.Error("a trimmer with no sequences reports itself active")
	}
	if newStopTrimmer([]string{""}).active() {
		t.Error("an empty sequence made the trimmer active")
	}
	if !newStopTrimmer([]string{"END"}).active() {
		t.Error("a real sequence did not make the trimmer active")
	}
}

func TestStopTrimmerMultipleSequences(t *testing.T) {
	got, stopped := feed(t, []string{"</s>", "\n\n"}, "line one", "\n\nline two")
	if !stopped || got != "line one" {
		t.Errorf("got %q, stopped = %v", got, stopped)
	}
}
