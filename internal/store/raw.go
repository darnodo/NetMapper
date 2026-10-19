package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/minio/minio-go/v7"
)

// RawStore keeps device output in the object store, addressed by the SHA-256 of the exact bytes.
type RawStore struct {
	Client *minio.Client
	Bucket string
}

func RawKey(hash []byte) string { return "raw/sha256/" + hex.EncodeToString(hash) }

// Put stores b once, whatever the number of devices that produced it, and returns its hash.
func (s *RawStore) Put(ctx context.Context, b []byte) ([]byte, error) {
	sum := sha256.Sum256(b)
	key := RawKey(sum[:])
	if _, err := s.Client.StatObject(ctx, s.Bucket, key, minio.StatObjectOptions{}); err == nil {
		return sum[:], nil
	} else if minio.ToErrorResponse(err).Code != minio.NoSuchKey {
		return nil, err
	}
	_, err := s.Client.PutObject(ctx, s.Bucket, key, bytes.NewReader(b), int64(len(b)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return sum[:], err
}

// Get returns the bytes stored under hash.
func (s *RawStore) Get(ctx context.Context, hash []byte) ([]byte, error) {
	o, err := s.Client.GetObject(ctx, s.Bucket, RawKey(hash), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer o.Close()
	var buf bytes.Buffer
	_, err = buf.ReadFrom(o)
	return buf.Bytes(), err
}

// Delete removes the bytes stored under hash. An object already gone is not an error, so an
// eviction interrupted between a delete and its bookkeeping can simply run again (012, research R5).
func (s *RawStore) Delete(ctx context.Context, hash []byte) error {
	return s.Client.RemoveObject(ctx, s.Bucket, RawKey(hash), minio.RemoveObjectOptions{})
}
