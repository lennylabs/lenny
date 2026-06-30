// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// newNonceOnlyAdmin builds an admin Router with a nonce-only runtime
// (requireSoPeercred: false) and an SO_PEERCRED-enforcing runtime seeded,
// at the requested dev-mode tenancy setting. The gate is unconditional, so
// the test drives it at both settings to prove it does not branch on
// tenancy mode.
func newNonceOnlyAdmin(t *testing.T, devMode bool) (*admin.Router, *poolstore.Memory) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	// A nonce-only runtime carries an explicit requireSoPeercred: false; the
	// Memory store preserves the pointer without applying the §5.1 default of
	// true, so RuntimeRequireSoPeercred reports false.
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:              "nonce-only",
		RequireSoPeercred: boolPtr(false),
	}); err != nil {
		t.Fatalf("seed nonce-only runtime: %v", err)
	}
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "enforcing",
	}); err != nil {
		t.Fatalf("seed enforcing runtime: %v", err)
	}
	router := admin.NewRouter(tenants, admin.Options{
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		DevMode: devMode,
	}).WithRuntimes(runtimes).WithPools(pools)
	return router, pools
}

// TestCreatePoolNonceOnlyAcknowledgment_spec_4_7 drives the §4.7 / §5.3
// nonce-only acknowledgment gate through the live admin create path in both
// tenancy modes: a pool bound to a requireSoPeercred: false runtime is
// rejected with 400 VALIDATION_ERROR unless it carries
// acknowledgeNonceOnlyAuth: true. The unconditional check must fire
// identically whether dev mode is on or off.
//
// diagnosis: the §4.7 nonce-only acknowledgment gate did not fail closed at
// pool create. An admitted unacknowledged pool means a pool can enter the
// registry bound to a runtime that weakens the adapter-agent authentication
// boundary without the deployer opt-in.
//
// spec: 4.7 (acknowledgment gate), 5.3 (acknowledgeNonceOnlyAuth)
func TestCreatePoolNonceOnlyAcknowledgment_spec_4_7(t *testing.T) {
	for _, devMode := range []bool{false, true} {
		mode := "tenant-mode"
		if devMode {
			mode = "dev-mode"
		}
		t.Run(mode, func(t *testing.T) {
			router, pools := newNonceOnlyAdmin(t, devMode)

			// Unacknowledged nonce-only pool: rejected.
			rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
				Name:             "nonce-no-ack",
				RuntimeRef:       "nonce-only",
				IsolationProfile: "sandboxed",
			})
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("unacknowledged nonce-only create: got %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
			if _, err := pools.Get(context.Background(), "nonce-no-ack"); err == nil {
				t.Error("unacknowledged nonce-only pool entered the registry; the gate did not fail closed")
			}

			// Acknowledged nonce-only pool: admitted and persisted with the flag.
			rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
				Name:                     "nonce-ack",
				RuntimeRef:               "nonce-only",
				IsolationProfile:         "sandboxed",
				AcknowledgeNonceOnlyAuth: true,
			})
			if rr.Code != http.StatusCreated {
				t.Fatalf("acknowledged nonce-only create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
			}
			row, err := pools.Get(context.Background(), "nonce-ack")
			if err != nil {
				t.Fatalf("acknowledged pool not persisted: %v", err)
			}
			if !row.AcknowledgeNonceOnlyAuth {
				t.Error("acknowledgeNonceOnlyAuth not persisted on the admitted pool")
			}

			// A pool bound to an enforcing runtime needs no acknowledgment.
			rr = poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
				Name:             "enforcing-pool",
				RuntimeRef:       "enforcing",
				IsolationProfile: "sandboxed",
			})
			if rr.Code != http.StatusCreated {
				t.Fatalf("enforcing-runtime pool create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// TestUpdatePoolNonceOnlyAcknowledgment_spec_4_7 drives the §4.7 / §5.3
// gate through the live admin PUT path in both tenancy modes. The gate
// evaluates the effective post-update runtimeRef and acknowledgment, so a
// runtimeRef swap to a nonce-only runtime without the ack is rejected, and
// toggling the ack off on an already-nonce-only pool is rejected. Setting
// the ack in the same swap is admitted.
//
// diagnosis: the §4.7 nonce-only acknowledgment gate did not fail closed at
// pool update. A pool could be moved into nonce-only mode, or strip its
// acknowledgment while remaining nonce-only, without the deployer opt-in.
//
// spec: 4.7 (acknowledgment gate), 5.3 (acknowledgeNonceOnlyAuth)
func TestUpdatePoolNonceOnlyAcknowledgment_spec_4_7(t *testing.T) {
	for _, devMode := range []bool{false, true} {
		mode := "tenant-mode"
		if devMode {
			mode = "dev-mode"
		}
		t.Run(mode, func(t *testing.T) {
			router, _ := newNonceOnlyAdmin(t, devMode)
			h := router.Handler()

			// Start with an enforcing-runtime pool that needs no acknowledgment.
			rr := poolReq(t, h, http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
				Name:             "swap-pool",
				RuntimeRef:       "enforcing",
				IsolationProfile: "sandboxed",
			})
			if rr.Code != http.StatusCreated {
				t.Fatalf("seed swap-pool: got %d, want 201; body=%s", rr.Code, rr.Body.String())
			}

			// A runtimeRef swap to the nonce-only runtime without the ack is
			// rejected.
			rr = poolReq(t, h, http.MethodPut, "/v1/admin/pools/swap-pool", admin.UpdatePoolRequest{
				RuntimeRef: ptr("nonce-only"),
			})
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("unacknowledged runtimeRef swap: got %d, want 400; body=%s", rr.Code, rr.Body.String())
			}

			// The same swap with the acknowledgment set is admitted.
			rr = poolReq(t, h, http.MethodPut, "/v1/admin/pools/swap-pool", admin.UpdatePoolRequest{
				RuntimeRef:               ptr("nonce-only"),
				AcknowledgeNonceOnlyAuth: boolPtr(true),
			})
			if rr.Code != http.StatusOK {
				t.Fatalf("acknowledged runtimeRef swap: got %d, want 200; body=%s", rr.Code, rr.Body.String())
			}

			// Toggling the acknowledgment off while the pool stays nonce-only is
			// rejected: the gate reads the effective post-update ack (false).
			rr = poolReq(t, h, http.MethodPut, "/v1/admin/pools/swap-pool", admin.UpdatePoolRequest{
				AcknowledgeNonceOnlyAuth: boolPtr(false),
			})
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("acknowledgment toggle-off: got %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}
