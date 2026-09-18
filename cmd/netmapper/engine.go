package main

import (
	"context"
	"flag"
	"log/slog"
	"time"

	"github.com/darnodo/NetMapper/internal/jobrunner"
)

// cmdEngine runs the job runner. It never reads S3 credentials, Vault variables or packs.
func cmdEngine(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("engine", flag.ContinueOnError)
	every := fs.Duration("interval", 2*time.Second, "job runner interval")
	if code, exit := parse(fs, args); exit {
		return code
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	slog.Info("engine started")
	jobrunner.Run(ctx, db, *every, slog.Default())
	return exitOK
}
