package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kazufusa/oocla/internal/bridge"
	"github.com/kazufusa/oocla/internal/core"
	"github.com/kazufusa/oocla/internal/httpapi"
	"github.com/kazufusa/oocla/internal/mcpshim"
	"github.com/kazufusa/oocla/internal/ollama"
	"github.com/kazufusa/oocla/internal/openai"
)

// defaultAddr is Ollama's port. Clients default to it, so oocla must too.
const defaultAddr = "127.0.0.1:11434"

// shutdownGrace is how long in-flight requests get to finish on shutdown.
const shutdownGrace = 20 * time.Second

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "address to listen on")
	claudeBin := fs.String("claude", "claude", "path to the claude executable")
	bare := fs.Bool("bare", false,
		"run claude in minimal mode: removes the base prompt and the injected user/date context, "+
			"but the CLI then accepts only ANTHROPIC_API_KEY or apiKeyHelper, not an OAuth login")
	// For managed environments whose admin allowlists MCP servers under an
	// issued name. Not for matching a name allowlisted for something else.
	shimName := fs.String("internal-mcp-shim-name", mcpshim.ServerName,
		"MCP server name the tool shim registers under; use the name your administrator allowlisted for oocla")
	promptTools := fs.Bool("internal-prompt-tools", false,
		"never use the MCP tool shim; always carry tools in the prompt (debugging, or environments that block the shim)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !mcpshim.ValidServerName(*shimName) {
		return fmt.Errorf("internal-mcp-shim-name: %q may only contain letters, digits, underscore and hyphen", *shimName)
	}

	b := bridge.New()
	b.Runner.Bin = *claudeBin
	b.Bare = *bare
	b.ShimName = *shimName
	b.PromptTools = *promptTools

	// The catalog has no files behind it, so modified_at is pinned at startup
	// rather than moving on every request.
	eng := core.NewEngine(core.NewRegistry(time.Now()), b)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The two dialects share nothing but the engine: /v1/* is OpenAI's,
	// everything else is Ollama's.
	handler := httpapi.SplitPrefix("/v1/", openai.NewServer(eng), ollama.NewServer(eng))
	httpSrv := &http.Server{Addr: *addr, Handler: handler}
	errc := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "oocla listening on %s\n", *addr)
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		// Failed before anything was served, so there is nothing to drain.
		_ = b.Close()
		return err
	case <-ctx.Done():
	}

	// Shut down deliberately rather than on process exit, so in-flight
	// requests finish and the scratch working directory is removed.
	fmt.Fprintln(os.Stderr, "oocla shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	err := httpSrv.Shutdown(shutdownCtx)
	if closeErr := b.Close(); err == nil {
		err = closeErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("shutdown timed out after %s", shutdownGrace)
	}
	return err
}
