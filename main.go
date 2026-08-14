// Command sentgraph-mcp is a memory MCP server backed by Zep Cloud.
//
// It runs in three modes:
//
//	sentgraph-mcp serve [--http ADDR]   run the MCP server (stdio by default)
//	sentgraph-mcp hook <event>          handle a Claude Code lifecycle hook (reads JSON from stdin)
//	sentgraph-mcp doctor                check configuration and Zep connectivity
package main

import (
	"context"
	"fmt"
	"os"
)

// version is the build version, injected at release time via -ldflags
// "-X main.version=...". It stays "dev" for local builds and `go install`.
var version = "dev"

const usage = `sentgraph-mcp - memory MCP server backed by Zep Cloud

Usage:
  sentgraph-mcp serve [--http ADDR]   Run the MCP server (stdio by default)
  sentgraph-mcp hook <event>          Handle a Claude Code lifecycle hook (reads JSON from stdin)
  sentgraph-mcp doctor                Check configuration and Zep connectivity
  sentgraph-mcp version               Print the binary version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(ctx, os.Args[2:], version)
	case "hook":
		err = runHook(ctx, os.Args[2:])
	case "doctor":
		err = runDoctor(ctx, os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "sentgraph:", err)
		os.Exit(1)
	}
}

func errNotImplemented(what string) error {
	return fmt.Errorf("%s: not implemented yet", what)
}
