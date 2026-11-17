//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §4.5 / §12.5 AWS S3-backed artifact store,
// exercising pkg/blobstore/s3 against a real S3-compatible MinIO
// container through the AWS SDK. §12.5 names "any S3-compatible object
// store" and requires the same artifact interface across the MinIO, S3,
// GCS, and Azure Blob backends; this pins the S3 backend's Put/Get/Stat
// round-trip, the §4.5 write-once guarantee, the §4.5 derive Copy
// primitive, the §12.5 soft-delete lifecycle, the §12.8 prefix-scoped
// DeleteByTenant, and the §4.5 interface-level cross-tenant guard against
// a real S3 API rather than an in-memory fake.
package stores_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/s3"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

func startS3Store(t *testing.T) *s3.Store {
	t.Helper()
	// An S3-compatible emulator reachable only at an IP or bare-host
	// endpoint (MinIO, LocalStack) cannot place the bucket in a DNS label,
	// so it requires path-style addressing. Config.UsePathStyle enables it,
	// which lets this real-backend contract address the local emulator.
	mc := containers.StartMinIO(t, containers.MinIOOptions{})
	awscfg := awssdk.Config{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(mc.AccessKey, mc.SecretKey, ""),
		BaseEndpoint: awssdk.String("http://" + mc.Endpoint),
	}
	store, err := s3.New(s3.Config{AWSConfig: awscfg, Bucket: mc.Bucket, UsePathStyle: true})
	if err != nil {
		t.Fatalf("s3.New: %v", err)
	}
	return store
}

// spec: 4.5, 12.5
// diagnosis: the §4.5 / §12.5 S3-backed artifact store in
// pkg/blobstore/s3 did not behave as specified against a real
// S3-compatible API. Put/Get/Stat must round-trip a blob, the §4.5
// write-once guarantee must reject a second live Put with ErrConflict, a
// Stat of an absent blob must return ErrNotFound, and the §12.5
// soft-delete must make a live blob read as ErrNotFound.
func TestS3StoreContract(t *testing.T) {
	store := startS3Store(t)

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

	t.Run("derive Copy duplicates bytes into a new key", func(t *testing.T) {
		src := blobURI("part_copy_src", time.Hour)
		dst := blobURI("part_copy_dst", time.Hour)
		if _, err := store.Put(src, "text/plain", strings.NewReader("payload")); err != nil {
			t.Fatalf("Put src: %v", err)
		}
		if err := store.Copy(src, dst); err != nil {
			t.Fatalf("Copy: %v", err)
		}
		_, body, err := store.Get(dst)
		if err != nil {
			t.Fatalf("Get dst: %v", err)
		}
		defer body.Close()
		got, _ := io.ReadAll(body)
		if string(got) != "payload" {
			t.Errorf("copied body = %q, want the source content", got)
		}
	})

	t.Run("soft-deleted blob reads as ErrNotFound", func(t *testing.T) {
		u := blobURI("part_tombstone", time.Hour)
		if _, err := store.Put(u, "text/plain", strings.NewReader("doomed")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if err := store.SoftDelete(u); err != nil {
			t.Fatalf("SoftDelete: %v", err)
		}
		if _, _, err := store.Get(u); !errors.Is(err, blobstore.ErrNotFound) {
			t.Errorf("Get post-SoftDelete: got %v, want ErrNotFound", err)
		}
		if _, err := store.Stat(u); !errors.Is(err, blobstore.ErrNotFound) {
			t.Errorf("Stat post-SoftDelete: got %v, want ErrNotFound", err)
		}
	})
}

// spec: 4.5, 12.5
// diagnosis: the §12.8 prefix-scoped DeleteByTenant on the S3 backend
// failed to bulk-delete exactly the target tenant's objects. §4.5
// requires DeleteByTenant(tenant_id) map to a deterministic
// prefix-scoped bulk delete on /{tenant_id}/*; a foreign tenant's
// objects must survive.
func TestS3StoreDeleteByTenantIsPrefixScoped(t *testing.T) {
	store := startS3Store(t)

	acme := blobstore.URI{TenantID: "acme", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s_a", PartID: "p_a", TTL: time.Hour, Encoding: blobstore.Encoding}
	globex := blobstore.URI{TenantID: "globex", ObjectType: blobstore.ObjectTypeUpload, SessionID: "s_g", PartID: "p_g", TTL: time.Hour, Encoding: blobstore.Encoding}
	if _, err := store.Put(acme, "text/plain", strings.NewReader("a")); err != nil {
		t.Fatalf("Put acme: %v", err)
	}
	if _, err := store.Put(globex, "text/plain", strings.NewReader("g")); err != nil {
		t.Fatalf("Put globex: %v", err)
	}

	n, err := store.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant(acme): %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteByTenant(acme) removed %d objects, want 1", n)
	}
	if _, err := store.Stat(acme); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("acme Stat after DeleteByTenant: got %v, want ErrNotFound", err)
	}
	if _, err := store.Stat(globex); err != nil {
		t.Errorf("globex Stat after DeleteByTenant(acme): got %v, want the object to survive", err)
	}
}

// spec: 4.5, 12.5
// diagnosis: the §4.5 interface-level tenant-prefix guard failed to
// reject a foreign-tenant URI before issuing an S3 call against the real
// backend. §4.5 requires every method that validates a supplied
// tenant_id against the path prefix reject a mismatch "without reaching"
// the object store. A blobstore.TenantScoped store bound to "acme" must
// return ErrCrossTenant from Put/Get/Stat/Copy/DeleteByTenant for a
// "globex" URI, and the object must never appear in the underlying S3
// bucket.
func TestS3StoreTenantPrefixRejectsCrossTenant(t *testing.T) {
	inner := startS3Store(t)
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

	src := foreign
	src.TenantID = "acme"
	if err := scoped.Copy(src, foreign); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("Copy cross-tenant dst: got %v, want ErrCrossTenant", err)
	}

	if _, err := scoped.DeleteByTenant(context.Background(), "globex"); !errors.Is(err, blobstore.ErrCrossTenant) {
		t.Errorf("DeleteByTenant(globex) on acme-scoped store: got %v, want ErrCrossTenant", err)
	}

	if _, err := inner.Stat(foreign); !errors.Is(err, blobstore.ErrNotFound) {
		t.Errorf("inner Stat foreign: got %v, want ErrNotFound (guard must reject before S3)", err)
	}
}
