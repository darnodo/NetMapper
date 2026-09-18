// Package testutil gives integration tests a fresh PostgreSQL schema and a fresh S3 bucket.
// Tests skip when NETMAPPER_TEST_DSN or NETMAPPER_TEST_S3_ENDPOINT is unset.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/darnodo/NetMapper/internal/store"
)

func name(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// DB is a superuser pool on a fresh, migrated schema, dropped when the test ends.
func DB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("NETMAPPER_TEST_DSN")
	if dsn == "" {
		t.Skip("NETMAPPER_TEST_DSN not set")
	}
	ctx := context.Background()
	schema := name("t_")
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close(context.Background())
	})
	pool := pool(t, dsn, schema, "")
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// As returns a pool on the same schema as db that acts as role, so tests exercise the grants.
func As(t testing.TB, db *pgxpool.Pool, role string) *pgxpool.Pool {
	t.Helper()
	cfg := db.Config()
	return pool(t, cfg.ConnString(), cfg.ConnConfig.RuntimeParams["search_path"], role)
}

func pool(t testing.TB, dsn, schema, role string) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	if role != "" {
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+role)
			return err
		}
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// S3 is a raw store on a fresh bucket, emptied and removed when the test ends.
func S3(t testing.TB) *store.RawStore {
	t.Helper()
	endpoint := os.Getenv("NETMAPPER_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("NETMAPPER_TEST_S3_ENDPOINT not set")
	}
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(env("NETMAPPER_TEST_S3_ACCESS_KEY", "GK0123456789abcdef01234567"), env("NETMAPPER_TEST_S3_SECRET_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"), ""),
		Region: env("NETMAPPER_TEST_S3_REGION", "garage"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bucket := name("t-")
	if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for o := range c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			c.RemoveObject(ctx, bucket, o.Key, minio.RemoveObjectOptions{})
		}
		c.RemoveBucket(ctx, bucket)
	})
	return &store.RawStore{Client: c, Bucket: bucket}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
