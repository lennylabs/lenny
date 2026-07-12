//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §12.5 T4 per-tenant SSE-KMS write path against
// a real MinIO container running its built-in KMS key manager. Covers:
// a T4 (requireKey) write applies SSE-KMS with the tenant-scoped key
// and the object records it against the real backend; a T3 write does
// not receive the per-tenant KMS key; and a T4 write whose tenant key
// is unavailable at the backend fails closed with
// CLASSIFICATION_CONTROL_VIOLATION without silently downgrading to the
// deployment-wide key.
package stores_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// tenantKMSKeyName is the single SSE-KMS key the KMS-enabled MinIO
// container is provisioned with. It stands in for the production
// tenant:{tenant_id} alias (which cannot be used verbatim because
// MinIO's built-in single-key manager parses MINIO_KMS_SECRET_KEY on
// the first colon); the tenant:{tenant_id} alias formatting itself is
// covered by the miniostore unit tests. The behavior under test here
// is that a T4 write reaches the real KMS backend under the resolved
// key and fails closed when that key is absent.
const tenantKMSKeyName = "lenny-t4-acme"

// t4KMSResolver returns an SSEKeyResolver that resolves tenant acme to
// keyID with requireKey=true (the §12.5 T4 path) and every other
// tenant to the T3 path (empty keyID, requireKey=false, no per-tenant
// key). Passing a keyID that MinIO does not know models the tenant key
// being unavailable at write time (deleted or disabled out-of-band).
func t4KMSResolver(keyID string) func(string) (string, bool, error) {
	return func(tenantID string) (string, bool, error) {
		if tenantID == "acme" {
			return keyID, true, nil
		}
		return "", false, nil
	}
}

func newKMSStore(t *testing.T, mc *containers.MinIO, resolver func(string) (string, bool, error)) *miniostore.Store {
	t.Helper()
	store, err := miniostore.New(miniostore.Config{
		Endpoint:       mc.Endpoint,
		AccessKey:      mc.AccessKey,
		SecretKey:      mc.SecretKey,
		Bucket:         mc.Bucket,
		SSEKeyResolver: resolver,
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}
	return store
}

func kmsBlobURI(tenant, part string) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenant,
		ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID:  "s_1",
		PartID:     part,
		TTL:        time.Hour,
		Encoding:   blobstore.Encoding,
	}
}

func objectKeyFor(u blobstore.URI) string {
	return u.TenantID + "/" + string(u.ObjectType) + "/" + u.SessionID + "/" + u.PartID
}

// spec: 12.5 (T4 per-tenant SSE-KMS key selection and fail-closed
// CLASSIFICATION_CONTROL_VIOLATION when the tenant key is unavailable)
// diagnosis: the §12.5 T4 cryptographic-erasure control is broken in
// the MinIO blob store. A T4 (workspaceTier: T4) artifact write must
// apply SSE-KMS with the tenant-scoped key so revoking that key renders
// the tenant's artifacts unreadable; a T3 write must not receive the
// per-tenant key; and a T4 write whose key is unavailable at the
// backend must be rejected with CLASSIFICATION_CONTROL_VIOLATION rather
// than silently stored under the deployment-wide key. A failure here
// means Restricted-tenant artifacts are encrypted under the wrong key
// or written unencrypted, defeating per-tenant cryptographic erasure.
func TestMinIOStoreT4SSEKMS(t *testing.T) {
	mc := containers.StartMinIO(t, containers.MinIOOptions{KMSKeyName: tenantKMSKeyName})

	// spec: 12.5 — "T4 uses a KMS key scoped to tenant:{tenant_id}".
	// A T4 write applies SSE-KMS with the resolved tenant key, and the
	// real backend records aws:kms with that key id on the object.
	t.Run("t4 write applies tenant SSE-KMS key", func(t *testing.T) {
		store := newKMSStore(t, mc, t4KMSResolver(tenantKMSKeyName))
		u := kmsBlobURI("acme", "part_t4_ok")
		if _, err := store.Put(u, "application/json", strings.NewReader(`{"k":"v"}`)); err != nil {
			t.Fatalf("T4 Put with available tenant key: %v", err)
		}
		info, err := mc.Client.StatObject(t.Context(), mc.Bucket, objectKeyFor(u), minio.StatObjectOptions{})
		if err != nil {
			t.Fatalf("StatObject: %v", err)
		}
		if got := info.Metadata.Get("X-Amz-Server-Side-Encryption"); got != "aws:kms" {
			t.Errorf("T4 object SSE header = %q, want aws:kms", got)
		}
		keyID := info.Metadata.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id")
		if !strings.Contains(keyID, tenantKMSKeyName) {
			t.Errorf("T4 object KMS key id = %q, want it to name the tenant key %q", keyID, tenantKMSKeyName)
		}
	})

	// spec: 12.5 — "T3 uses the deployment-wide SSE key". A T3 write
	// must not be wrapped under the per-tenant KMS key.
	t.Run("t3 write does not use the per-tenant KMS key", func(t *testing.T) {
		store := newKMSStore(t, mc, t4KMSResolver(tenantKMSKeyName))
		u := kmsBlobURI("globex", "part_t3")
		if _, err := store.Put(u, "application/json", strings.NewReader(`{"k":"v"}`)); err != nil {
			t.Fatalf("T3 Put: %v", err)
		}
		info, err := mc.Client.StatObject(t.Context(), mc.Bucket, objectKeyFor(u), minio.StatObjectOptions{})
		if err != nil {
			t.Fatalf("StatObject: %v", err)
		}
		if keyID := info.Metadata.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id"); strings.Contains(keyID, tenantKMSKeyName) {
			t.Errorf("T3 object was wrapped under the per-tenant KMS key %q (key id header = %q)", tenantKMSKeyName, keyID)
		}
	})

	// spec: 12.5 bullet 2 — "If the tenant-scoped KMS key is unavailable
	// during a checkpoint or artifact write ... the gateway MUST reject
	// the write with CLASSIFICATION_CONTROL_VIOLATION. The ArtifactStore
	// does not fall back to the deployment-wide SSE key". Model the key
	// being unavailable by resolving to a key name the KMS backend does
	// not know; MinIO rejects the SSE-KMS PutObject with kms:KeyNotFound.
	t.Run("t4 write fails closed when tenant key is unavailable", func(t *testing.T) {
		var fired int
		store := newKMSStore(t, mc, t4KMSResolver(tenantKMSKeyName+"-revoked"))
		store.SetOnKMSUnavailable(func(string) { fired++ })
		u := kmsBlobURI("acme", "part_t4_revoked")
		_, err := store.Put(u, "application/json", strings.NewReader(`{"k":"v"}`))
		if !errors.Is(err, blobstore.ErrClassificationControlViolation) {
			t.Fatalf("Put with unavailable tenant key: err = %v, want ErrClassificationControlViolation", err)
		}
		if fired != 1 {
			t.Errorf("onKMSUnavailable fired %d times, want 1", fired)
		}
		// The write must not have been stored under any weaker key.
		if _, statErr := mc.Client.StatObject(t.Context(), mc.Bucket, objectKeyFor(u), minio.StatObjectOptions{}); statErr == nil {
			t.Error("object was written despite the KMS key being unavailable; the store must fail closed with no fallback")
		}
	})
}
