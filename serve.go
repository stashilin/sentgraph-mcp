package main

import (
	"context"
	"flag"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shilin23061991/sentgraph-mcp/internal/config"
	"github.com/shilin23061991/sentgraph-mcp/internal/mcpserver"
	"github.com/shilin23061991/sentgraph-mcp/internal/memory"
	"github.com/shilin23061991/sentgraph-mcp/internal/zepstore"
)

// runServe starts the MCP server. With --http it serves Streamable HTTP,
// otherwise it serves over stdio (how Claude Code / Cursor launch it).
func runServe(ctx context.Context, args []string, version string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	httpAddr := fs.String("http", "", "serve Streamable HTTP on this address (e.g. :8080); empty = stdio")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := config.Load()
	if err := cfg.RequireProjectConfig(); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	store := zepstore.New(cfg.ZepAPIKey)
	server := mcpserver.New(memory.New(cfg, store), version)

	if *httpAddr == "" {
		return server.Run(ctx, &mcp.StdioTransport{})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server.MCP()
	}, nil)
	return http.ListenAndServe(*httpAddr, handler)
}
