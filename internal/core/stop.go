package core

import (
	"errors"
	"strings"
)

// errStopSequence is returned by a wrapped emit to end generation early. It is
// an internal signal, never an error the client sees.
var errStopSequence = errors.New("stop sequence reached")

// withStop wraps emit so that a stream ends at the first stop sequence.
//
// The wrapped function returns errStopSequence once generation should end,
// which unwinds the backend the same way a disconnected client does. flush
// releases any text that was held back and turned out to be harmless; call it
// only when the stream ended without hitting a sequence.
func withStop(stops []string, emit func(StreamChunk) error) (wrapped func(StreamChunk) error, flush func() error) {
	t := newStopTrimmer(stops)
	if !t.active() {
		return emit, func() error { return nil }
	}
	wrapped = func(c StreamChunk) error {
		if c.Text == "" {
			return emit(c)
		}
		text, stop := t.Write(c.Text)
		c.Text = text
		if text != "" || c.Thinking != "" || len(c.ToolCalls) != 0 {
			if err := emit(c); err != nil {
				return err
			}
		}
		if stop {
			return errStopSequence
		}
		return nil
	}
	flush = func() error {
		if tail := t.Flush(); tail != "" {
			return emit(StreamChunk{Text: tail})
		}
		return nil
	}
	return wrapped, flush
}

// stopTrimmer cuts a stream of text at the first stop sequence.
//
// A stop sequence can straddle two chunks, so any tail that could still turn
// out to be the start of one is held back until the next chunk proves it either
// way. Once a sequence is seen, everything after it is discarded.
type stopTrimmer struct {
	stops   []string
	pending strings.Builder
	done    bool
}

func newStopTrimmer(stops []string) *stopTrimmer {
	live := make([]string, 0, len(stops))
	for _, s := range stops {
		if s != "" {
			live = append(live, s)
		}
	}
	return &stopTrimmer{stops: live}
}

// active reports whether the trimmer has anything to do.
func (t *stopTrimmer) active() bool { return t != nil && len(t.stops) > 0 }

// Write takes the next piece of text and returns the part that is safe to pass
// on, along with whether generation should stop here.
func (t *stopTrimmer) Write(s string) (string, bool) {
	if t.done {
		return "", true
	}
	t.pending.WriteString(s)
	buf := t.pending.String()

	if cut, ok := firstStop(buf, t.stops); ok {
		t.done = true
		t.pending.Reset()
		return buf[:cut], true
	}

	// Hold back the longest tail that is still a possible start of a stop
	// sequence, so the sequence is recognised even when split across chunks.
	keep := len(buf) - longestPartialSuffix(buf, t.stops)
	t.pending.Reset()
	t.pending.WriteString(buf[keep:])
	return buf[:keep], false
}

// Flush returns whatever was held back, once no more text is coming.
func (t *stopTrimmer) Flush() string {
	if t.done {
		return ""
	}
	out := t.pending.String()
	t.pending.Reset()
	return out
}

// TrimAtStop cuts s at the first stop sequence that occurs in it.
func TrimAtStop(s string, stops []string) string {
	if cut, ok := firstStop(s, stops); ok {
		return s[:cut]
	}
	return s
}

// firstStop returns the index of the earliest stop sequence in s.
func firstStop(s string, stops []string) (int, bool) {
	best := -1
	for _, stop := range stops {
		if stop == "" {
			continue
		}
		if i := strings.Index(s, stop); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	return best, best >= 0
}

// longestPartialSuffix returns the length of the longest suffix of s that is a
// proper prefix of some stop sequence.
func longestPartialSuffix(s string, stops []string) int {
	longest := 0
	for _, stop := range stops {
		limit := min(len(stop)-1, len(s))
		for n := limit; n > longest; n-- {
			if strings.HasPrefix(stop, s[len(s)-n:]) {
				longest = n
				break
			}
		}
	}
	return longest
}
