package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/darnodo/NetMapper/internal/jobrunner"
)

// cmdRun starts a discovery and prints its job id. It opens no device session and resolves no secret.
func cmdRun(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	file := fs.String("config", "", "configuration document (YAML)")
	perimeter := fs.String("perimeter", "", "perimeter name")
	seedSet := fs.String("seed-set", "", "seed set name")
	if code, exit := parse(fs, args); exit {
		return code
	}
	if *file == "" || *perimeter == "" || *seedSet == "" {
		fmt.Fprintln(os.Stderr, "--config, --perimeter and --seed-set are required")
		return exitInvalid
	}
	doc, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInvalid
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	by := "unknown"
	if u, err := user.Current(); err == nil {
		by = u.Username
	}
	id, err := jobrunner.Start(ctx, db, doc, *perimeter, *seedSet, by)
	var invalid jobrunner.Invalid
	if errors.As(err, &invalid) {
		for _, p := range invalid {
			fmt.Fprintln(os.Stderr, p)
		}
		return exitInvalid
	}
	if err != nil {
		return fail("start", err)
	}
	fmt.Println(id)
	return exitOK
}

func cmdCancel(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	if code, exit := parse(fs, args); exit {
		return code
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if fs.NArg() != 1 || err != nil {
		fmt.Fprintln(os.Stderr, "usage: netmapper cancel <job-id>")
		return exitInvalid
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	if err := jobrunner.Cancel(ctx, db, id); errors.Is(err, jobrunner.ErrNotRunning) {
		fmt.Fprintf(os.Stderr, "job %d is not running\n", id)
		return exitInvalid
	} else if err != nil {
		return fail("cancel", err)
	}
	return exitOK
}
