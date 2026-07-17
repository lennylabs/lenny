// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// TestCreatePoolCredentialDeliveryRoundTrip_spec_4_9 verifies the §4.9
// pool-definition credential-delivery fields (deliveryMode, spiffeBinding,
// and the two deployer opt-ins) are copied from the create payload onto the
// stored pool and surfaced back on GET, so one warm-pool admin resource
// holds the whole combination the enforcement layers evaluate.
func TestCreatePoolCredentialDeliveryRoundTrip_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                                "cred-pool",
		RuntimeRef:                          "echo",
		IsolationProfile:                    "sandboxed",
		ExecutionMode:                       "session",
		DeliveryMode:                        "proxy",
		SpiffeBinding:                       "disabled",
		AllowProxyModeSpiffeBindingDisabled: true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "cred-pool")
	if row.DeliveryMode != "proxy" || row.SpiffeBinding != "disabled" ||
		!row.AllowProxyModeSpiffeBindingDisabled || row.AllowDirectModeStandardIsolation {
		t.Fatalf("stored row missing credential-delivery fields: %+v", row)
	}

	g := poolReq(t, router.Handler(), http.MethodGet, "/v1/admin/pools/cred-pool", nil)
	if g.Code != http.StatusOK {
		t.Fatalf("get status: %d body=%s", g.Code, g.Body.String())
	}
	var got admin.PoolPayload
	if err := json.Unmarshal(g.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if got.DeliveryMode != "proxy" || got.SpiffeBinding != "disabled" ||
		!got.AllowProxyModeSpiffeBindingDisabled {
		t.Fatalf("GET payload missing credential-delivery fields: %+v", got)
	}
}

// TestCreatePoolRejectsUnknownDeliveryMode_spec_4_9 verifies a non-empty
// deliveryMode outside {proxy, direct} is rejected with 400 VALIDATION_ERROR
// at registration, before any store write, so an out-of-enum value never
// reconciles onto the enum-constrained SandboxTemplate CRD.
func TestCreatePoolRejectsUnknownDeliveryMode_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:         "bad-delivery",
		RuntimeRef:   "echo",
		DeliveryMode: "PROXY",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad deliveryMode: got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "bad-delivery"); err == nil {
		t.Fatalf("rejected pool must not be stored")
	}
}

// TestCreatePoolRejectsUnknownSpiffeBinding_spec_4_9 verifies a non-empty
// spiffeBinding outside {enabled, disabled} is rejected with 400
// VALIDATION_ERROR at registration.
func TestCreatePoolRejectsUnknownSpiffeBinding_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:          "bad-spiffe",
		RuntimeRef:    "echo",
		SpiffeBinding: "off",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad spiffeBinding: got %d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "bad-spiffe"); err == nil {
		t.Fatalf("rejected pool must not be stored")
	}
}

// TestUpdatePoolCredentialDeliveryMerge_spec_4_9 verifies a partial PUT
// toggles the §4.9 credential-delivery fields on the stored pool while an
// omitted field is left unchanged.
func TestUpdatePoolCredentialDeliveryMerge_spec_4_9(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	create := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "up-pool",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		DeliveryMode:     "proxy",
		SpiffeBinding:    "enabled",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", create.Code, create.Body.String())
	}

	dm := "direct"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/up-pool", admin.UpdatePoolRequest{
		DeliveryMode: &dm,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update status: %d body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "up-pool")
	if row.DeliveryMode != "direct" {
		t.Fatalf("deliveryMode not merged: %+v", row)
	}
	if row.SpiffeBinding != "enabled" {
		t.Fatalf("omitted spiffeBinding must be unchanged: %+v", row)
	}
}

// TestUpdatePoolRejectsUnknownDeliveryMode_spec_4_9 verifies the update path
// rejects a non-empty deliveryMode outside {proxy, direct} with 400
// VALIDATION_ERROR, matching the create-side enum clause.
func TestUpdatePoolRejectsUnknownDeliveryMode_spec_4_9(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	create := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "u2",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", create.Code, create.Body.String())
	}

	bad := "sideways"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/u2", admin.UpdatePoolRequest{
		DeliveryMode: &bad,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad deliveryMode update: got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestUpdatePoolRejectsUnknownSpiffeBinding_spec_4_9 verifies the update path
// rejects a non-empty spiffeBinding outside {enabled, disabled} with 400
// VALIDATION_ERROR.
func TestUpdatePoolRejectsUnknownSpiffeBinding_spec_4_9(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	create := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "u3",
		RuntimeRef:       "echo",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status: %d body=%s", create.Code, create.Body.String())
	}

	bad := "off"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/u3", admin.UpdatePoolRequest{
		SpiffeBinding: &bad,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad spiffeBinding update: got %d body=%s", rr.Code, rr.Body.String())
	}
}
