package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/darnodo/NetMapper/internal/gate"
)

func cmdJudge(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("judge", flag.ContinueOnError)
	if code, exit := parse(fs, args); exit {
		return code
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if fs.NArg() != 1 || err != nil {
		fmt.Fprintln(os.Stderr, "usage: netmapper judge <snapshot-id>")
		return exitInvalid
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()

	j, err := gate.Judge(ctx, db, id)
	switch {
	case errors.Is(err, gate.ErrNotFound):
		fmt.Fprintf(os.Stderr, "snapshot %d not found\n", id)
		return exitInvalid
	case errors.Is(err, gate.ErrNotClosed):
		fmt.Fprintf(os.Stderr, "snapshot %d is not closed\n", id)
		return exitInvalid
	case err != nil:
		return fail("judge", err)
	}
	if j.BaselineID == nil {
		fmt.Printf("%s no baseline\n", j.Classification)
		return exitOK
	}
	fmt.Printf("%s %d/%d (baseline snapshot %d)\n",
		j.Classification, j.CarriedOver, j.BaselineCount, *j.BaselineID)
	return exitOK
}
