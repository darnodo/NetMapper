// Command netmapper is the one binary: `collector` and `engine` roles, and the operator
// subcommands `migrate`, `run`, `cancel` and `judge` (contracts/cli.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/darnodo/NetMapper/internal/store"
)

// Exit codes: 0 success, 1 runtime error, 2 invalid input.
const (
	exitOK      = 0
	exitError   = 1
	exitInvalid = 2
)

var commands = map[string]func(ctx context.Context, args []string) int{
	"migrate":   cmdMigrate,
	"run":       cmdRun,
	"cancel":    cmdCancel,
	"judge":     cmdJudge,
	"collector": cmdCollector,
	"engine":    cmdEngine,
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if len(os.Args) < 2 || commands[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "usage: netmapper migrate|run|cancel|judge|collector|engine [flags]")
		os.Exit(exitInvalid)
	}
	os.Exit(commands[os.Args[1]](ctx, os.Args[2:]))
}

// parse reads flags and reports whether the caller should exit, and with what code.
func parse(fs *flag.FlagSet, args []string) (int, bool) {
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK, true
		}
		return exitInvalid, true
	}
	return 0, false
}

// connect opens NETMAPPER_DSN; each process connects with its own role.
func connect(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("NETMAPPER_DSN")
	if dsn == "" {
		return nil, errors.New("NETMAPPER_DSN is not set")
	}
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return db, db.Ping(ctx)
}

func fail(msg string, err error) int {
	slog.Error(msg, "err", err)
	return exitError
}

func cmdMigrate(ctx context.Context, args []string) int {
	if code, exit := parse(flag.NewFlagSet("migrate", flag.ContinueOnError), args); exit {
		return code
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		return fail("migrate", err)
	}
	return exitOK
}
