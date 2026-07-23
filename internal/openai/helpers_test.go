package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kazufusa/oocla/internal/core"
)

func testTime() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// fakeGen records the last input and replays a canned output.
type fakeGen struct {
	last core.GenerateInput
	out  core.GenerateOutput
	err  error
	// chunks are replayed through emit when the caller wants a stream.
	chunks []core.StreamChunk
	// sawEmit records whether the server asked for chunks.
	sawEmit bool
}

func (f *fakeGen) Generate(_ context.Context, in core.GenerateInput, emit func(core.StreamChunk) error) (core.GenerateOutput, error) {
	f.last = in
	f.sawEmit = emit != nil
	if emit != nil {
		for _, c := range f.chunks {
			if err := emit(c); err != nil {
				return core.GenerateOutput{}, err
			}
		}
	}
	return f.out, f.err
}

func newChatServer(t *testing.T, gen core.Generator) http.Handler {
	t.Helper()
	eng := core.NewEngine(core.NewRegistry(testTime()), gen)
	eng.NowFunc = testTime
	return NewServer(eng)
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
