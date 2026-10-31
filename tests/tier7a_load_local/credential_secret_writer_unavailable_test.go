// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for a gateway that has a Token Service and no
// cluster client. Concurrent admin credential-pool writes run against the
// production Router over the failing writer such a gateway holds, and tenant
// deletion Phase 4 runs through the production eraser.
//
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rules 5 and 6), §12.8 (Compliance Interfaces)
package tier7a_load_local_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// diagnosis: a failure means a secretData write through the failing writer
// answered other than 503, committed a row, or the 422 of rule 6 reached a
// gateway that has a Token Service.
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 5 and 6
func TestCredentialWritesRefused503WithoutClusterClient_spec_4_9(t *testing.T) {
	const (
		workers = 8
		opsEach = 25
	)
	store := credentialpoolstore.NewMemory()
	writer := admin.NewUnavailableCredentialSecretWriter(errors.New("no cluster config"))
	h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{CredentialsNamespace: interleaveNS}).
		WithCredentialPools(store).WithCredentialSecretWriter(writer).Handler()
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var refused atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < opsEach; i++ {
				code := interleaveSend(h, http.MethodPost, "/v1/admin/credential-pools/p1/credentials?tenantId=acme",
					map[string]any{"id": "key-1", "secretData": map[string]string{"apiKey": "v"}})
				if code != http.StatusServiceUnavailable {
					t.Errorf("worker %d: status %d, want 503", w, code)
				}
				refused.Add(1)
			}
		}(w)
	}
	wg.Wait()
	if got := refused.Load(); got != workers*opsEach {
		t.Fatalf("issued %d writes, want %d", got, workers*opsEach)
	}
	row, err := store.Get(context.Background(), "acme", "p1")
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}
	if len(row.Credentials) != 0 {
		t.Fatalf("pool holds %d entries after refused writes, want 0", len(row.Credentials))
	}
}

// diagnosis: a failure means tenant deletion Phase 4 reported success while
// the delete-collection could not run, which leaves the tenant's
// Lenny-written Secrets behind, or a retry on a recovered writer did not
// finish the phase.
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 5; §12.8 (Compliance Interfaces)
func TestTenantErasureRetriesWithoutClusterClient_spec_4_9(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	ctx := context.Background()
	if err := store.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	unavailable := admin.NewUnavailableCredentialSecretWriter(errors.New("no cluster config"))
	if _, err := admin.TenantCredentialPoolEraser(store, unavailable, interleaveNS)(ctx, "acme"); err == nil {
		t.Fatal("Phase 4 erasure returned nil with a failing writer, want an error so the job retries")
	}
	recovered := &interleaveWriter{secrets: map[string]admin.SecretOwner{}}
	if _, err := admin.TenantCredentialPoolEraser(store, recovered, interleaveNS)(ctx, "acme"); err != nil {
		t.Fatalf("retried Phase 4 erasure: %v", err)
	}
}
