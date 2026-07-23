package core_test

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"
)

// The two dialect packages must not know about each other: their schemas have
// nothing in common, and neither may serve as a conversion tunnel for the
// other. This test pins the package structure so a convenient cross-import
// cannot creep back in.

const (
	module    = "github.com/kazufusa/oocla"
	ollamaPkg = module + "/internal/ollama"
	openaiPkg = module + "/internal/openai"
	corePkg   = module + "/internal/core"
	ollamaDir = "../ollama"
	openaiDir = "../openai"
)

// imports collects every import path used by the Go files in dir, tests
// included.
func imports(t *testing.T, dir string) map[string]bool {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dialect package directory is missing: %v", err)
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	out := map[string]bool{}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, imp := range f.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatalf("unquoting import %s: %v", imp.Path.Value, err)
				}
				out[path] = true
			}
		}
	}
	return out
}

func TestDialectsDoNotImportEachOther(t *testing.T) {
	if imports(t, ollamaDir)[openaiPkg] {
		t.Errorf("internal/ollama imports internal/openai; the dialects must stay independent")
	}
	if imports(t, openaiDir)[ollamaPkg] {
		t.Errorf("internal/openai imports internal/ollama; the dialects must stay independent")
	}
}

func TestDialectsShareTheCore(t *testing.T) {
	if !imports(t, ollamaDir)[corePkg] {
		t.Errorf("internal/ollama does not import internal/core; the shared middle should carry it")
	}
	if !imports(t, openaiDir)[corePkg] {
		t.Errorf("internal/openai does not import internal/core; the shared middle should carry it")
	}
}

// Below the core, nothing is allowed to know a dialect exists: the backend
// packages speak core types only, and cmd/oocla is the only place where the
// dialects are composed.
func TestBackendDoesNotImportTheDialects(t *testing.T) {
	for _, dir := range []string{"../bridge", "../mcpshim", "../claudecli", "../httpapi"} {
		got := imports(t, dir)
		if got[ollamaPkg] || got[openaiPkg] {
			t.Errorf("%s imports a dialect package; only cmd/oocla may compose the dialects", dir)
		}
	}
}
