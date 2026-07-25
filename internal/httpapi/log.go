package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// LogRequests wraps h so that every request is logged on completion with its
// method, path, status and duration, plus whatever the handler recorded
// through AddAttrs while it ran.
func LogRequests(logger *slog.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		sink := &attrSink{}
		r = r.WithContext(context.WithValue(r.Context(), attrSinkKey{}, sink))

		// Streaming handlers probe for http.Flusher, so the wrapper must not
		// hide it when the underlying writer has one.
		var out http.ResponseWriter = sw
		if f, ok := w.(http.Flusher); ok {
			out = &flushingStatusWriter{statusWriter: sw, flusher: f}
		}
		h.ServeHTTP(out, r)

		args := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.code,
			"duration", time.Since(start).Round(time.Millisecond).String(),
		}
		sink.mu.Lock()
		args = append(args, sink.args...)
		sink.mu.Unlock()
		logger.Info("request", args...)
	})
}

type attrSinkKey struct{}

// attrSink collects extra log attributes while a request is handled.
type attrSink struct {
	mu   sync.Mutex
	args []any
}

// AddAttrs attaches extra key/value pairs to the request's completion log
// line. Outside a LogRequests wrapper it is a no-op.
func AddAttrs(r *http.Request, args ...any) {
	sink, ok := r.Context().Value(attrSinkKey{}).(*attrSink)
	if !ok {
		return
	}
	sink.mu.Lock()
	sink.args = append(sink.args, args...)
	sink.mu.Unlock()
}

// statusWriter remembers the status code a handler sent.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

// flushingStatusWriter adds the underlying writer's Flush.
type flushingStatusWriter struct {
	*statusWriter
	flusher http.Flusher
}

func (w *flushingStatusWriter) Flush() { w.flusher.Flush() }
