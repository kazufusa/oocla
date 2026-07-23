// Command oocla serves Ollama- and OpenAI-compatible HTTP APIs backed by the
// claude CLI.
package main

import (
	"fmt"
	"os"

	"github.com/kazufusa/oocla/internal/buildinfo"
)

const usage = `oocla - Ollama- and OpenAI-compatible server backed by the claude CLI

usage:
  oocla serve [--addr host:port]   start the HTTP server
  oocla mcp-shim                   MCP stdio server exposing request tools
  oocla version                    print version
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "oocla:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("no subcommand given")
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "mcp-shim":
		return runMCPShim()
	case "version":
		fmt.Println(buildinfo.Get())
		return nil
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}
