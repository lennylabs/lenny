// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// assertNoMaxSessionRetries fails when any JSON document in docs names the
// retired sessionPolicy.maxSessionRetries key.
func assertNoMaxSessionRetries(t *testing.T, what string, doc []byte) {
	t.Helper()
	if strings.Contains(string(doc), "maxSessionRetries") {
		t.Errorf("%s carries maxSessionRetries: %s", what, doc)
	}
}

// TestRuntimeCreateIgnoresRetiredMaxSessionRetries_spec_7_3 pins the
// deletion of sessionPolicy.maxSessionRetries: retryPolicy.maxRetries is
// the one retry budget, so a runtime create that still carries the key,
// even with a negative value the deleted validation used to refuse, is
// accepted, and neither the stored row nor the response names the key.
//
// spec: §7.3 (Retry and Resume), §5.2 (Pool Configuration and Execution Modes)
func TestRuntimeCreateIgnoresRetiredMaxSessionRetries_spec_7_3(t *testing.T) {
	router, store, _ := newRuntimeAdmin(t)
	body := json.RawMessage(`{"name":"rt-retired","image":"lenny/rt@sha256:abc","labels":{"tier":"test"},` +
		`"sessionPolicy":{"maxSessionRetries":-1,"cleanupTimeoutSeconds":30}}`)
	rr := runtimeRequest(t, router.Handler(), http.MethodPost, "/v1/admin/runtimes", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	assertNoMaxSessionRetries(t, "create response", rr.Body.Bytes())

	got, err := store.Get(context.Background(), "rt-retired")
	if err != nil {
		t.Fatalf("store get: %v", err)
	}
	if got.SessionPolicy == nil || got.SessionPolicy.CleanupTimeoutSeconds != 30 {
		t.Fatalf("sessionPolicy not persisted: %+v", got.SessionPolicy)
	}
	stored, err := json.Marshal(got.SessionPolicy)
	if err != nil {
		t.Fatalf("marshal stored policy: %v", err)
	}
	assertNoMaxSessionRetries(t, "stored sessionPolicy", stored)

	rr = runtimeRequest(t, router.Handler(), http.MethodGet, "/v1/admin/runtimes/rt-retired", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d; body=%s", rr.Code, rr.Body.String())
	}
	assertNoMaxSessionRetries(t, "GET response", rr.Body.Bytes())
}

// TestPoolCreateIgnoresRetiredMaxSessionRetries_spec_7_3 is the pool-admin
// counterpart: a pool sessionPolicy that still carries maxSessionRetries is
// accepted and the key is persisted nowhere.
//
// spec: §7.3 (Retry and Resume), §5.2 (Pool Configuration and Execution Modes)
func TestPoolCreateIgnoresRetiredMaxSessionRetries_spec_7_3(t *testing.T) {
	router, pools, runtimes, _ := newPoolAdmin(t)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	body := json.RawMessage(`{"name":"retired-pool","runtimeRef":"claude-code","isolationProfile":"sandboxed",` +
		`"executionMode":"session","sessionPolicy":{"maxSessionRetries":-1,"cleanupTimeoutSeconds":30}}`)
	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	assertNoMaxSessionRetries(t, "create response", rr.Body.Bytes())

	got, err := pools.Get(context.Background(), "retired-pool")
	if err != nil {
		t.Fatalf("store get: %v", err)
	}
	stored, err := json.Marshal(got.SessionPolicy)
	if err != nil {
		t.Fatalf("marshal stored policy: %v", err)
	}
	if got.SessionPolicy == nil || got.SessionPolicy.CleanupTimeoutSeconds != 30 {
		t.Fatalf("sessionPolicy not persisted: %s", stored)
	}
	assertNoMaxSessionRetries(t, "stored sessionPolicy", stored)
}
