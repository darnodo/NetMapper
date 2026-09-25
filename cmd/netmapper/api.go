package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/darnodo/NetMapper/internal/api"
)

// cmdAPI serves the read interface until SIGTERM. It connects as netmapper_api and reads the object
// store with the collector's NETMAPPER_S3_* variables, read only; it opens no device session and
// resolves no secret reference (contracts/rest.md, FR-015).
func cmdAPI(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("api", flag.ContinueOnError)
	listen := fs.String("listen", ":8080", "address to listen on")
	if code, exit := parse(fs, args); exit {
		return code
	}
	raw, err := rawStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInvalid
	}
	db, err := connect(ctx)
	if err != nil {
		return fail("connect", err)
	}
	defer db.Close()
	if ok, err := raw.Client.BucketExists(ctx, raw.Bucket); err != nil {
		return fail("object store", err)
	} else if !ok {
		return fail("object store", fmt.Errorf("bucket %s does not exist", raw.Bucket))
	}

	srv := &http.Server{Addr: *listen, Handler: api.New(db, raw).Handler(), ReadHeaderTimeout: 10 * time.Second}
	// ListenAndServe returns as soon as Shutdown starts; wait for the drain to finish before the
	// deferred db.Close and the process exit cut in-flight requests.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	slog.Info("api started", "listen", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fail("api", err)
	}
	<-drained
	return exitOK
}
