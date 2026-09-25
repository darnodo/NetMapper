package main

import (
	"context"
	"flag"
	"log/slog"
	"time"

	"github.com/darnodo/NetMapper/internal/jobrunner"
	"github.com/darnodo/NetMapper/internal/pack"
)

// cmdEngine runs the job runner. It never reads S3 credentials and never resolves a Vault secret. It
// does read platform packs, for one thing: the graph projector needs the naming rules that turn the
// port a neighbour reports for the far end of a cable into a canonical name, and it is the first
// component that knows both the spelling and the far end's platform (004, FR-003, research R3).
func cmdEngine(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("engine", flag.ContinueOnError)
	every := fs.Duration("interval", 2*time.Second, "job runner interval")
	packs := fs.String("packs", "packs", "directory of platform packs")
	if code, exit := parse(fs, args); exit {
		return code
	}
	reg, err := pack.LoadRoot(*packs)
	if err != nil {
		return fail("packs", err)
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	slog.Info("engine started")
	jobrunner.Run(ctx, db, reg, *every, slog.Default())
	return exitOK
}
