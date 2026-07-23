// Package httpapi is the small HTTP plumbing both API dialects share: a
// method-aware router that reports 405 with an Allow header, and JSON
// request/response helpers. Error bodies are dialect-shaped, so the router
// delegates misses to per-mux hooks instead of writing anything itself.
package httpapi

import (
	"net/http"
	"sort"
	"strings"
)

// Mux routes exact paths, plus prefix families for the rare route with a path
// parameter. Routing is exact on purpose: a catch-all would swallow the 405
// that clients rely on to probe capabilities.
type Mux struct {
	routes   map[string]map[string]http.HandlerFunc // path -> method -> handler
	prefixes []prefixRoute

	// NotFound reports a path with no route. Nil falls back to http.NotFound.
	NotFound http.HandlerFunc
	// MethodNotAllowed reports a known path hit with the wrong method. allow
	// is the comma-separated method list for the Allow header. Nil falls back
	// to a bare 405 with the header set.
	MethodNotAllowed func(w http.ResponseWriter, r *http.Request, allow string)
}

// prefixRoute matches every path strictly under a prefix.
type prefixRoute struct {
	prefix  string
	method  string
	handler http.HandlerFunc
}

// NewMux returns an empty Mux.
func NewMux() *Mux {
	return &Mux{routes: map[string]map[string]http.HandlerFunc{}}
}

// Handle registers a handler for an exact path and method. GET handlers also
// serve HEAD.
func (m *Mux) Handle(path, method string, h http.HandlerFunc) {
	byMethod, ok := m.routes[path]
	if !ok {
		byMethod = map[string]http.HandlerFunc{}
		m.routes[path] = byMethod
	}
	byMethod[method] = h
	if method == http.MethodGet {
		byMethod[http.MethodHead] = h
	}
}

// HandlePrefix registers a handler for every path strictly under prefix. GET
// handlers also serve HEAD.
func (m *Mux) HandlePrefix(prefix, method string, h http.HandlerFunc) {
	m.prefixes = append(m.prefixes, prefixRoute{prefix: prefix, method: method, handler: h})
	if method == http.MethodGet {
		m.prefixes = append(m.prefixes, prefixRoute{prefix: prefix, method: http.MethodHead, handler: h})
	}
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	byMethod, ok := m.routes[r.URL.Path]
	if !ok {
		for _, p := range m.prefixes {
			if p.method == r.Method && strings.HasPrefix(r.URL.Path, p.prefix) && len(r.URL.Path) > len(p.prefix) {
				p.handler(w, r)
				return
			}
		}
		m.notFound(w, r)
		return
	}
	h, ok := byMethod[r.Method]
	if !ok {
		m.methodNotAllowed(w, r, strings.Join(allowedMethods(byMethod), ", "))
		return
	}
	h(w, r)
}

func (m *Mux) notFound(w http.ResponseWriter, r *http.Request) {
	if m.NotFound != nil {
		m.NotFound(w, r)
		return
	}
	http.NotFound(w, r)
}

func (m *Mux) methodNotAllowed(w http.ResponseWriter, r *http.Request, allow string) {
	if m.MethodNotAllowed != nil {
		m.MethodNotAllowed(w, r, allow)
		return
	}
	w.Header().Set("Allow", allow)
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func allowedMethods(byMethod map[string]http.HandlerFunc) []string {
	out := make([]string, 0, len(byMethod))
	for method := range byMethod {
		out = append(out, method)
	}
	sort.Strings(out)
	return out
}

// SplitPrefix routes requests strictly under prefix to under, and everything
// else to other. It is how the two API dialects share one listener without
// sharing any routes.
func SplitPrefix(prefix string, under, other http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, prefix) {
			under.ServeHTTP(w, r)
			return
		}
		other.ServeHTTP(w, r)
	})
}
