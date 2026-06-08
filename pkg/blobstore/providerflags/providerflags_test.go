// SPDX-License-Identifier: MIT

package providerflags

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
)

// spec: §17.9.3 — an unset provider with no MinIO endpoint resolves to
// the in-memory store (the §17.4 minimal/dev posture).
func TestResolveDefaultsToMemoryWithoutMinIO_spec_17_9_3(t *testing.T) {
	s, err := Resolve(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Resolve(default): %v", err)
	}
	if _, ok := s.(*blobstore.MemoryStore); !ok {
		t.Fatalf("default provider with no MinIO endpoint = %T, want *blobstore.MemoryStore", s)
	}
}

// spec: §17.9.3 — provider=memory forces the in-memory store regardless
// of any MinIO endpoint.
func TestResolveMemoryProvider_spec_17_9_3(t *testing.T) {
	s, err := Resolve(context.Background(), Options{Provider: ProviderMemory, MinIOEndpoint: "minio:9000"})
	if err != nil {
		t.Fatalf("Resolve(memory): %v", err)
	}
	if _, ok := s.(*blobstore.MemoryStore); !ok {
		t.Fatalf("provider=memory = %T, want *blobstore.MemoryStore", s)
	}
}

// spec: §17.4 line 165 — provider=filesystem resolves to the
// local-filesystem store rooted at the configured directory.
func TestResolveFilesystemProvider_spec_17_4_165(t *testing.T) {
	root := t.TempDir()
	s, err := Resolve(context.Background(), Options{Provider: ProviderFilesystem, FilesystemRoot: root})
	if err != nil {
		t.Fatalf("Resolve(filesystem): %v", err)
	}
	if _, ok := s.(*blobstore.FilesystemStore); !ok {
		t.Fatalf("provider=filesystem = %T, want *blobstore.FilesystemStore", s)
	}
}

// spec: §17.4 line 165 — filesystem without a root is a hard error so a
// misconfiguration fails startup rather than silently losing artifacts.
func TestResolveFilesystemRequiresRoot_spec_17_4_165(t *testing.T) {
	if _, err := Resolve(context.Background(), Options{Provider: ProviderFilesystem}); err == nil {
		t.Fatal("provider=filesystem with no root unexpectedly succeeded")
	}
}

// spec: §17.9.3 — the default/minio provider with a configured endpoint
// resolves to the MinIO backend (the §17.1 self-managed posture).
func TestResolveMinIOWhenEndpointSet_spec_17_9_3(t *testing.T) {
	for _, prov := range []string{"", ProviderMinIO} {
		s, err := Resolve(context.Background(), Options{
			Provider:       prov,
			MinIOEndpoint:  "minio.lenny-system.svc:9000",
			MinIOAccessKey: "ak",
			MinIOSecretKey: "sk",
			MinIOBucket:    "lenny-artifacts",
			MinIOUseSSL:    true,
		})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", prov, err)
		}
		if _, ok := s.(*miniostore.Store); !ok {
			t.Fatalf("provider=%q = %T, want *miniostore.Store", prov, s)
		}
	}
}

// spec: §17.9.3 — an unrecognised provider is a hard error so a typo in
// objectStorage.provider fails startup rather than silently degrading.
func TestResolveUnknownProvider_spec_17_9_3(t *testing.T) {
	_, err := Resolve(context.Background(), Options{Provider: "wasabi"})
	if err == nil {
		t.Fatal("Resolve(wasabi): want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown objectStorage.provider") {
		t.Fatalf("error = %q, want unknown-provider message", err)
	}
}

// spec: §17.9.3 — each cloud provider validates its required config
// before any SDK call so a misconfiguration fails fast with a clear
// message (these paths do not require live cloud credentials).
func TestResolveCloudProviderRequiredConfig_spec_17_9_3(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"s3 missing bucket", Options{Provider: ProviderS3}, "provider=s3 requires objectStorage.bucket"},
		{"gcs missing bucket", Options{Provider: ProviderGCS}, "provider=gcs requires objectStorage.bucket"},
		{"azure missing account url", Options{Provider: ProviderAzure, Bucket: "c"}, "provider=azure requires objectStorage.accountUrl"},
		{"azure missing container", Options{Provider: ProviderAzure, AzureAccountURL: "https://acct.blob.core.windows.net"}, "provider=azure requires objectStorage.bucket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(context.Background(), tc.opts)
			if err == nil {
				t.Fatalf("Resolve(%s): want error, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

// spec: §12.5 ll. 297-303 — the cloud SSE hook adapter forwards the
// blobstore.URI tenant id to the gateway's tenant-keyed resolver so the
// T4 fail-closed write contract survives the provider switch.
func TestURIResolverForwardsTenantID_spec_12_5_297(t *testing.T) {
	var gotTenant string
	r := uriResolver(func(tenantID string) (string, bool, error) {
		gotTenant = tenantID
		return "tenant:" + tenantID, true, nil
	})
	keyID, require, err := r(blobstore.URI{TenantID: "acme"})
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	if gotTenant != "acme" {
		t.Fatalf("tenant forwarded = %q, want acme", gotTenant)
	}
	if keyID != "tenant:acme" || !require {
		t.Fatalf("adapter result = (%q, %v), want (tenant:acme, true)", keyID, require)
	}
}

// A nil tenant resolver yields a nil hook so bucket-default encryption
// applies (the T3 path); the adapter must not wrap nil in a closure.
func TestURIResolverNilIsNil(t *testing.T) {
	if uriResolver(nil) != nil {
		t.Fatal("uriResolver(nil) must return nil")
	}
}

// spec: §12.9 line 1048 — the in-memory backend resolved with an SSE
// resolver installs a tier guard, so a confirmed-T4 tenant's write is
// rejected with CLASSIFICATION_CONTROL_VIOLATION / tier_store_mismatch
// rather than persisted in the clear.
func TestResolveMemoryInstallsTierGuard_spec_12_9_1048(t *testing.T) {
	s, err := Resolve(context.Background(), Options{
		Provider: ProviderMemory,
		SSEKeyResolver: func(tenantID string) (string, bool, error) {
			return "tenant:" + tenantID, tenantID == "restricted", nil
		},
	})
	if err != nil {
		t.Fatalf("Resolve(memory): %v", err)
	}
	u := blobstore.URI{TenantID: "restricted", SessionID: "s", PartID: "p"}
	if _, err := s.Put(u, "text/plain", strings.NewReader("x")); !errors.Is(err, blobstore.ErrTierStoreMismatch) {
		t.Fatalf("T4 Put on memory store = %v, want ErrTierStoreMismatch", err)
	}
	// A non-T4 tenant writes normally through the same guarded store.
	ok := blobstore.URI{TenantID: "acme", SessionID: "s", PartID: "p"}
	if _, err := s.Put(ok, "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("non-T4 Put on memory store: %v", err)
	}
}

// spec: §12.9 line 1048 — the filesystem backend likewise installs the
// tier guard from the SSE resolver.
func TestResolveFilesystemInstallsTierGuard_spec_12_9_1048(t *testing.T) {
	s, err := Resolve(context.Background(), Options{
		Provider:       ProviderFilesystem,
		FilesystemRoot: t.TempDir(),
		SSEKeyResolver: func(tenantID string) (string, bool, error) {
			return "tenant:" + tenantID, tenantID == "restricted", nil
		},
	})
	if err != nil {
		t.Fatalf("Resolve(filesystem): %v", err)
	}
	u := blobstore.URI{TenantID: "restricted", SessionID: "s", PartID: "p"}
	if _, err := s.Put(u, "text/plain", strings.NewReader("x")); !errors.Is(err, blobstore.ErrTierStoreMismatch) {
		t.Fatalf("T4 Put on filesystem store = %v, want ErrTierStoreMismatch", err)
	}
}
