//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.5 MinIO-backed blob store, exercising
// pkg/blobstore/miniostore against a real MinIO container. Covers the
// Put/Get/Stat round-trip, the §4.5 write-once guarantee, the §4.5 TTL
// expiry, the not-found sentinel, and the §12.5 drain-readiness probe.
package stores_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

func startMinIOStore(t *testing.T) (*miniostore.Store, *containers.MinIO) {
	t.Helper()
	mc := containers.StartMinIO(t, containers.MinIOOptions{})
	store, err := miniostore.New(miniostore.Config{
		Endpoint:  mc.Endpoint,
		AccessKey: mc.AccessKey,
		SecretKey: mc.SecretKey,
		Bucket:    mc.Bucket,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	return store, mc
}

func blobURI(part string, ttl time.Duration) blobstore.URI {
	return blobstore.URI{
		TenantID:  "acme",
		SessionID: "s_1",
		PartID:    part,
		TTL:       ttl,
		Encoding:  blobstore.Encoding,
	}
}

func TestMinIOStoreContract(t *testing.T) {
	store, _ := startMinIOStore(t)

	t.Run("put and get round-trip", func(t *testing.T) {
		u := blobURI("part_get", time.Hour)
		if _, err := store.Put(u, "application/json", strings.NewReader(`{"k":"v"}`)); err != nil {
			t.Fatalf("Put: %v", err)
		}
		info, body, err := store.Get(u)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer body.Close()
		got, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(got) != `{"k":"v"}` {
			t.Errorf("body = %q, want the stored content", got)
		}
		if info.MimeType != "application/json" || info.Size != 9 {
			t.Errorf("info = %+v, want application/json size 9", info)
		}
	})

	t.Run("write-once put returns ErrConflict", func(t *testing.T) {
		u := blobURI("part_conflict", time.Hour)
		if _, err := store.Put(u, "text/plain", strings.NewReader("first")); err != nil {
			t.Fatalf("first Put: %v", err)
		}
		if _, err := store.Put(u, "text/plain", strings.NewReader("second")); !errors.Is(err, blobstore.ErrConflict) {
			t.Errorf("second Put: got %v, want ErrConflict", err)
		}
	})

	t.Run("stat missing blob returns ErrNotFound", func(t *testing.T) {
		if _, err := store.Stat(blobURI("part_absent", time.Hour)); !errors.Is(err, blobstore.ErrNotFound) {
			t.Errorf("Stat missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("expired blob reads as ErrNotFound", func(t *testing.T) {
		u := blobURI("part_expired", time.Second)
		if _, err := store.Put(u, "text/plain", strings.NewReader("ephemeral")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		time.Sleep(1500 * time.Millisecond)
		if _, err := store.Stat(u); !errors.Is(err, blobstore.ErrNotFound) {
			t.Errorf("Stat expired: got %v, want ErrNotFound", err)
		}
	})

	t.Run("probe reports the bucket healthy", func(t *testing.T) {
		if err := store.Probe(t.Context()); err != nil {
			t.Errorf("Probe: %v", err)
		}
	})
}
