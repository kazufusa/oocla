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
	if err := fs.Parse(args); err != nil {
		return err
	}

	b := bridge.New()
	b.Runner.Bin = *claudeBin
	b.Bare = *bare

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
