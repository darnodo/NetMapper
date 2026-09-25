package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/darnodo/NetMapper/internal/graph"
	"github.com/darnodo/NetMapper/internal/pack"
)

// cmdProject projects one snapshot again, replacing its interfaces and edges. This is the only way a
// snapshot whose inputs have not changed is ever projected twice: the sweep takes the ones that carry
// no current projection, and nothing decides on its own to redo one (FR-022).
func cmdProject(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("project", flag.ContinueOnError)
	packs := fs.String("packs", "packs", "directory of platform packs")
	if code, exit := parse(fs, args); exit {
		return code
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if fs.NArg() != 1 || err != nil {
		fmt.Fprintln(os.Stderr, "usage: netmapper project <snapshot-id>")
		return exitInvalid
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

	r, err := graph.Project(ctx, db, reg, id)
	switch {
	case errors.Is(err, graph.ErrNotFound):
		fmt.Fprintf(os.Stderr, "snapshot %d not found\n", id)
		return exitInvalid
	case errors.Is(err, graph.ErrNotClosed):
		fmt.Fprintf(os.Stderr, "snapshot %d is not closed\n", id)
		return exitInvalid
	case errors.Is(err, graph.ErrNotResolved):
		fmt.Fprintf(os.Stderr, "snapshot %d has no entity set\n", id)
		return exitInvalid
	case err != nil:
		return fail("project", err)
	}
	fmt.Printf("%d interfaces, %d edges, %d disagreements\n", r.Interfaces, r.Edges, r.Disagreements)
	return exitOK
}
