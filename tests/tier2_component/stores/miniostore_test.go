//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.5 MinIO-backed blob store, exercising
// pkg/blobstore/miniostore against a real MinIO container. Covers the
// Put/Get/Stat round-trip, the §4.5 write-once guarantee, the §4.5 TTL
// expiry, the not-found sentinel, and the §12.5 drain-readiness probe.
package stores_test

import (
	"context"
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
	store, err := minioStoreFor(mc)
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	return store, mc
}

// minioStoreFor builds a MinIO-backed blob store over the container's
// bucket.
func minioStoreFor(mc *containers.MinIO) (*miniostore.Store, error) {
	return miniostore.New(miniostore.Config{
		Endpoint:  mc.Endpoint,
		AccessKey: mc.AccessKey,
		SecretKey: mc.SecretKey,
		Bucket:    mc.Bucket,
	})
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

// spec: 4.5, 12.5
// diagnosis: the MinIO-backed blob store in pkg/blobstore/miniostore
// did not behave as specified. Put, Get, and Stat must round-trip a
// blob, the §4.5 write-once guarantee must reject a second Put with
// ErrConflict, an expired blob must read as ErrNotFound, and the §12.5
// drain-readiness Probe must report the bucket healthy.
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

// spec: 4.5, 12.5
// diagnosis: the §4.5 interface-level tenant-prefix guard failed to
// reject a foreign-tenant URI before issuing an S3 call against the
// real MinIO backend. §4.5 requires that every method validating a
// supplied tenant_id against the path prefix reject a mismatch
// "without reaching MinIO". A blobstore.TenantScoped store bound to
// "acme" must return ErrCrossTenant from Put/Get/Stat/Copy/
// DeleteByTenant for a "globex" URI, and the object must never appear
// in the underlying MinIO bucket.
func TestMinIOStoreTenantPrefixRejectsCrossTenant(t *testing.T) {
	inner, _ := startMinIOStore(t)
	// The gateway constructs this wrapper from the resolved request
	// tenant; a call built from foreign-tenant input cannot reach the
	// real S3 backend behind it.
	scoped := blobstore.NewTenantScoped("acme", inner)

	foreign := blobstore.URI{
		TenantID:   "globex",
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  "s_x",
		PartID:     "p_x",
		TTL:        time.Hour,
		Encoding:   blobstore.Encoding,
	}

	if _, err := scoped.Put(foreign, "text/plain", strings.NewReader("x")); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Put cross-tenant: got %v, want ErrCrossTenant", err)
	}
	if _, _, err := scoped.Get(foreign); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Get cross-tenant: got %v, want ErrCrossTenant", err)
	}
	if _, err := scoped.Stat(foreign); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Stat cross-tenant: got %v, want ErrCrossTenant", err)
	}

	// A Copy whose destination names a foreign tenant is rejected even
	// though the source passes the acme caller-tenant gate.
	src := foreign
	src.TenantID = "acme"
	if err := scoped.Copy(src, foreign); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Copy cross-tenant dst: got %v, want ErrCrossTenant", err)
	}

	// A bulk delete for a tenant other than the bound one is rejected
	// before any object is touched.
	if _, err := scoped.DeleteByTenant(context.Background(), "globex"); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("DeleteByTenant(globex) on acme-scoped store: got %v, want ErrCrossTenant", err)
	}

	// The guard rejects before issuing any S3 call: the foreign object
	// never reached MinIO, so a direct Stat on the underlying store
	// returns ErrNotFound rather than a stored blob.
	if _, err := inner.Stat(foreign); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("inner Stat foreign: got %v, want ErrNotFound (guard must reject before S3)", err)
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle), 12.5
// diagnosis: the MinIO store's tenant prefix delete, which tenant-deletion
// Phase 4 runs as the artifacts entry, left an object under the deleted
// tenant's prefix, removed an object of another tenant, or failed on a
// re-run. The prefix delete must remove every object under the tenant's
// prefix, including objects of every object type, and nothing else.
func TestMinIOStoreDeleteByTenant_spec_12_8(t *testing.T) {
	store, _ := startMinIOStore(t)
	ctx := context.Background()
	put := func(tenant string, objType blobstore.ObjectType, session string) blobstore.URI {
		u := blobstore.URI{TenantID: tenant, ObjectType: objType, SessionID: session, PartID: "p", TTL: time.Hour, Encoding: blobstore.Encoding}
		if _, err := store.Put(u, "text/plain", strings.NewReader(tenant)); err != nil {
			t.Fatalf("Put %v: %v", u, err)
		}
		return u
	}
	acme := []blobstore.URI{
		put("acme", blobstore.ObjectTypeUpload, "s1"),
		put("acme", blobstore.ObjectTypeWorkspace, "s2"),
		put("acme", blobstore.ObjectTypeCheckpoint, "s2"),
	}
	twin := put("globex", blobstore.ObjectTypeUpload, "s1")
	// A tenant whose ID shares a prefix with acme must not be reached.
	sibling := put("acme-2", blobstore.ObjectTypeUpload, "s1")

	n, err := store.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != len(acme) {
		t.Errorf("DeleteByTenant removed %d objects, want %d", n, len(acme))
	}
	for _, u := range acme {
		if _, err := store.Stat(u); !errors.Is(err, blobstore.ErrNotFound) {
			t.Errorf("acme object %v survived: %v", u, err)
		}
	}
	for _, u := range []blobstore.URI{twin, sibling} {
		if _, err := store.Stat(u); err != nil {
			t.Errorf("object %v of another tenant lost: %v", u, err)
		}
	}
	if n, err := store.DeleteByTenant(ctx, "acme"); err != nil || n != 0 {
		t.Errorf("second DeleteByTenant = (%d, %v), want (0, nil)", n, err)
	}
}
