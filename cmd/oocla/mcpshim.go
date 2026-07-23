package main

import (
	"os"

	"github.com/kazufusa/oocla/internal/mcpshim"
)

// runMCPShim serves the tools of one request over stdio. It is spawned by the
// claude CLI, not by a user.
func runMCPShim() error {
	tools, err := mcpshim.DecodeTools(os.Getenv(mcpshim.ToolsEnv))
	if err != nil {
		return err
	}
	return mcpshim.Serve(os.Stdin, os.Stdout, tools)
}
