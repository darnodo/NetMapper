package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/darnodo/NetMapper/internal/collector"
	"github.com/darnodo/NetMapper/internal/pack"
	"github.com/darnodo/NetMapper/internal/secret"
	"github.com/darnodo/NetMapper/internal/store"
	"github.com/darnodo/NetMapper/internal/transport"
	"github.com/darnodo/NetMapper/internal/transport/snmp"
	"github.com/darnodo/NetMapper/internal/transport/ssh"
)

// cmdCollector claims and runs tasks until SIGTERM, then stops claiming and lets in-flight steps
// finish for up to the lease duration.
func cmdCollector(ctx context.Context, args []string) int {
	host, _ := os.Hostname()
	fs := flag.NewFlagSet("collector", flag.ContinueOnError)
	id := fs.String("id", host, "collector id, recorded on claimed tasks and in the audit log")
	workers := fs.Int("workers", 64, "concurrent device sessions")
	consume := fs.String("consume", "find,scrape", "task kinds to claim")
	packs := fs.String("packs", "packs", "directory of platform packs")
	if code, exit := parse(fs, args); exit {
		return code
	}
	reg, err := pack.LoadRoot(*packs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInvalid
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
	c := collector.New(db, *id, reg, raw, secret.NewResolver(), map[string]transport.Transport{
		"snmp": snmp.New(),
		"ssh":  &ssh.Transport{Platforms: reg.Scrapli(), Port: 22, SocketTimeout: 10 * time.Second},
	}, slog.Default())
	c.Workers = *workers
	c.Kinds = strings.Split(*consume, ",")
	slog.Info("collector started", "id", *id, "workers", *workers, "consume", c.Kinds)
	if err := c.Run(ctx); err != nil {
		return fail("collector", err)
	}
	return exitOK
}

// rawStore reads NETMAPPER_S3_*. Only the collector does.
func rawStore() (*store.RawStore, error) {
	var missing []string
	get := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			missing = append(missing, k)
		}
		return v
	}
	endpoint, bucket := get("NETMAPPER_S3_ENDPOINT"), get("NETMAPPER_S3_BUCKET")
	access, secretKey := get("NETMAPPER_S3_ACCESS_KEY"), get("NETMAPPER_S3_SECRET_KEY")
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	region := os.Getenv("NETMAPPER_S3_REGION")
	if region == "" {
		region = "garage"
	}
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secretKey, ""),
		Secure: os.Getenv("NETMAPPER_S3_INSECURE") == "",
		Region: region,
	})
	return &store.RawStore{Client: c, Bucket: bucket}, err
}
