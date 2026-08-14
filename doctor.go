package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/shilin23061991/sentgraph-mcp/internal/config"
	"github.com/shilin23061991/sentgraph-mcp/internal/memory"
	"github.com/shilin23061991/sentgraph-mcp/internal/zepstore"
)

// runDoctor validates configuration (API key, user/project resolution) and
// checks Zep connectivity.
func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	online := fs.Bool("online", false, "check Zep connectivity by ensuring user/project graph/thread")
	threadID := fs.String("thread", "sentgraph-doctor", "thread id to use for --online checks")
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

	fmt.Fprintf(os.Stdout, "config ok: user=%s project=%s graph=%s\n", cfg.UserID, cfg.ProjectID, cfg.ProjectGraphID())
	if !*online {
		return nil
	}

	store := zepstore.New(cfg.ZepAPIKey)
	if err := memory.New(cfg, store).EnsureIdentity(ctx, *threadID); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "zep ok: identity ensured")
	return nil
}
