// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	blobproviderflags "github.com/lennylabs/lenny/pkg/blobstore/providerflags"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// t4AssertFlags builds a gatewayFlags carrying only the object-store fields
// assertT4DefaultEncryption reads, so the test can drive the gate without the
// full flag set.
func t4AssertFlags(provider, gcsCMEK, azureScope string, azureDeny bool) *gatewayFlags {
	return &gatewayFlags{
		objectStorageProvider:                         &provider,
		objectStorageGCSBucketDefaultCMEK:             &gcsCMEK,
		objectStorageAzureDefaultEncryptionScope:      &azureScope,
		objectStorageAzureDenyEncryptionScopeOverride: &azureDeny,
	}
}

// listFailingTenants embeds a nil tenantstore.Store and overrides List to
// fail, standing in for a transient tenant-enumeration outage. Only List is
// called by assertT4DefaultEncryption, so the embedded nil is never
// dereferenced.
type listFailingTenants struct {
	tenantstore.Store
	err error
}

func (s listFailingTenants) List(context.Context, tenantstore.ListFilter) ([]tenantstore.Tenant, error) {
	return nil, s.err
}

// spec: §12.5 line 315, 321-325; §17.9.7 — assertT4DefaultEncryption is the
// gateway-startup wrapper that resolves the object-store provider and the set
// of workspaceTier T4 tenants, then applies the fail-closed default-encryption
// gate. It skips a SigV4 backend (which folds the SSE-KMS key into the
// signature at request time), fails closed on a gcs/azure backend serving a T4
// tenant without the required backend default, and boots when the default is
// present or no T4 tenant is served.
//
// TestAssertT4DefaultEncryptionResolvesProviderAndTenants exercises the
// wrapper's own resolution and delegation, complementing the pure
// validateT4DefaultEncryption cases.
func TestAssertT4DefaultEncryptionResolvesProviderAndTenants(t *testing.T) {
	ctx := context.Background()
	seedT4 := func(t *testing.T) tenantstore.Store {
		t.Helper()
		store := tenantstore.NewMemory()
		if err := store.Create(ctx, tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"}); err != nil {
			t.Fatalf("seed T4 tenant: %v", err)
		}
		if err := store.Create(ctx, tenantstore.Tenant{ID: "globex", WorkspaceTier: "T3"}); err != nil {
			t.Fatalf("seed T3 tenant: %v", err)
		}
		return store
	}

	t.Run("minio backend is not gated even with a T4 tenant", func(t *testing.T) {
		w := &gatewayWiring{f: t4AssertFlags(blobproviderflags.ProviderMinIO, "", "", false)}
		if err := w.assertT4DefaultEncryption(ctx, seedT4(t)); err != nil {
			t.Fatalf("minio backend gated at startup: %v, want nil", err)
		}
	})

	t.Run("gcs serving a T4 tenant without CMEK fails closed", func(t *testing.T) {
		w := &gatewayWiring{f: t4AssertFlags(blobproviderflags.ProviderGCS, "", "", false)}
		err := w.assertT4DefaultEncryption(ctx, seedT4(t))
		if err == nil {
			t.Fatal("gcs backend serving a T4 tenant without a bucket-default CMEK booted, want fail-closed")
		}
		if !strings.Contains(err.Error(), "bucketDefaultCmek") {
			t.Errorf("error %q does not name the missing config key", err.Error())
		}
	})

	t.Run("gcs serving a T4 tenant with CMEK boots", func(t *testing.T) {
		w := &gatewayWiring{f: t4AssertFlags(blobproviderflags.ProviderGCS,
			"projects/acme/locations/us/keyRings/lenny/cryptoKeys/tenant-alice", "", false)}
		if err := w.assertT4DefaultEncryption(ctx, seedT4(t)); err != nil {
			t.Fatalf("gcs backend with a bucket-default CMEK failed to boot: %v", err)
		}
	})

	// A case- or whitespace-variant provider name resolves to a genuine
	// gcs/azure backend (blobproviderflags.Resolve lower-cases and trims the
	// raw value), so the gate must normalize identically before comparing.
	// Comparing the raw string would let "GCS", "Azure", or " gcs" bypass the
	// gate and boot without the required default encryption, the fail-open the
	// gate exists to prevent. These cases fail against a gate that keys off the
	// raw provider string.
	for _, variant := range []string{"GCS", " gcs", "Gcs\t"} {
		t.Run("gcs variant "+variant+" serving a T4 tenant without CMEK fails closed", func(t *testing.T) {
			w := &gatewayWiring{f: t4AssertFlags(variant, "", "", false)}
			err := w.assertT4DefaultEncryption(ctx, seedT4(t))
			if err == nil {
				t.Fatalf("gcs variant %q serving a T4 tenant without a bucket-default CMEK booted, want fail-closed", variant)
			}
			if !strings.Contains(err.Error(), "bucketDefaultCmek") {
				t.Errorf("error %q does not name the missing config key", err.Error())
			}
		})
	}

	t.Run("azure variant Azure serving a T4 tenant without a scope fails closed", func(t *testing.T) {
		w := &gatewayWiring{f: t4AssertFlags("Azure", "", "", false)}
		err := w.assertT4DefaultEncryption(ctx, seedT4(t))
		if err == nil {
			t.Fatal("azure variant \"Azure\" serving a T4 tenant without an encryption scope booted, want fail-closed")
		}
		if !strings.Contains(err.Error(), "defaultEncryptionScope") {
			t.Errorf("error %q does not name the missing config key", err.Error())
		}
	})

	t.Run("gcs serving no T4 tenant boots without CMEK", func(t *testing.T) {
		store := tenantstore.NewMemory()
		if err := store.Create(ctx, tenantstore.Tenant{ID: "globex", WorkspaceTier: "T3"}); err != nil {
			t.Fatalf("seed T3 tenant: %v", err)
		}
		w := &gatewayWiring{f: t4AssertFlags(blobproviderflags.ProviderGCS, "", "", false)}
		if err := w.assertT4DefaultEncryption(ctx, store); err != nil {
			t.Fatalf("gcs backend serving no T4 tenant gated: %v, want nil", err)
		}
	})
}

// spec: §12.5 line 315; §17.9.7 — a gcs/azure backend whose T4-tenant
// enumeration fails is itself fatal: the gateway cannot verify the T4
// default-encryption posture and must not boot, so the wrapper surfaces the
// wrapped enumeration error rather than proceeding on an empty tenant set.
//
// diagnosis: a swallowed enumeration error would boot the gateway as though it
// served no T4 tenant, skipping the default-encryption gate and letting a T4
// checkpoint land under the deployment-wide key, defeating the §12.9
// cryptographic-erasure property.
func TestAssertT4DefaultEncryptionFailsClosedWhenTenantEnumerationFails(t *testing.T) {
	ctx := context.Background()
	tenants := listFailingTenants{err: errors.New("postgres tenant list timed out")}
	w := &gatewayWiring{f: t4AssertFlags(blobproviderflags.ProviderAzure, "", "lenny-tenant-alice", true)}
	err := w.assertT4DefaultEncryption(ctx, tenants)
	if err == nil {
		t.Fatal("assertT4DefaultEncryption booted despite a failed T4-tenant enumeration, want fail-closed")
	}
	if !errors.Is(err, tenants.err) {
		t.Errorf("error %v does not wrap the underlying enumeration failure", err)
	}
	if !strings.Contains(err.Error(), "enumerate workspaceTier T4 tenants") {
		t.Errorf("error %q does not name the enumeration failure", err.Error())
	}
}

// spec: §11.2 line 35; §12.4 storageQuotaBytes — tenantStorageQuotaResolver is
// the checkpointer QuotaLimitFor seam. It returns a tenant's configured
// storageQuotaBytes for the pre-checkpoint reservation, and fails closed with a
// wrapped, tenant-named error when the lookup fails so a checkpoint does not
// reserve against a limit the gateway could not read.
//
// diagnosis: a swallowed tenant-lookup failure would resolve to a zero limit,
// which the driver treats as unlimited, silently disabling the cumulative cap
// for a tenant whose quota the gateway simply failed to read.
func TestTenantStorageQuotaResolver(t *testing.T) {
	ctx := context.Background()

	store := tenantstore.NewMemory()
	if err := store.Create(ctx, tenantstore.Tenant{ID: "acme", StorageQuotaBytes: 4096}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	resolve := tenantStorageQuotaResolver(store)

	got, err := resolve(ctx, "acme")
	if err != nil {
		t.Fatalf("resolve configured limit: %v", err)
	}
	if got != 4096 {
		t.Errorf("storage limit = %d, want 4096 (the tenant's configured storageQuotaBytes)", got)
	}

	// A missing tenant (the store returns ErrNotFound) fails closed with a
	// wrapped, tenant-named error rather than a zero unlimited resolution.
	_, err = resolve(ctx, "ghost")
	if err == nil {
		t.Fatal("resolve on a missing tenant returned nil, want a wrapped lookup failure")
	}
	if !strings.Contains(err.Error(), "resolve storage quota for tenant ghost") {
		t.Errorf("error %q does not name the failing tenant lookup", err.Error())
	}
}
