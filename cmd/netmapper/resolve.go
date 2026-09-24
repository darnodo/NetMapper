package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/darnodo/NetMapper/internal/entity"
)

// cmdResolve resolves one snapshot again, replacing its entity set. This is the only way a snapshot
// is ever resolved twice: the sweep takes the ones that carry no set at all, and nothing decides on
// its own to redo one (FR-017).
func cmdResolve(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	if code, exit := parse(fs, args); exit {
		return code
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if fs.NArg() != 1 || err != nil {
		fmt.Fprintln(os.Stderr, "usage: netmapper resolve <snapshot-id>")
		return exitInvalid
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()

	r, err := entity.Resolve(ctx, db, id)
	switch {
	case errors.Is(err, entity.ErrNotFound):
		fmt.Fprintf(os.Stderr, "snapshot %d not found\n", id)
		return exitInvalid
	case errors.Is(err, entity.ErrNotClosed):
		fmt.Fprintf(os.Stderr, "snapshot %d is not closed\n", id)
		return exitInvalid
	case err != nil:
		return fail("resolve", err)
	}
	fmt.Printf("%d entities, %d weakly identified, %d conflicts\n", r.Entities, r.Weak, r.Conflicts)
	return exitOK
}
