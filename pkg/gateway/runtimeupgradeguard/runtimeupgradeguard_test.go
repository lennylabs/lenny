// SPDX-License-Identifier: MIT

package runtimeupgradeguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtimeupgradeguard"
	"github.com/lennylabs/lenny/pkg/gateway/runtimeupgradestore"
)

// spec: §10.5 line 508 — GET /internal/runtime-upgrade/active reports
// whether a pool has an active (non-terminal) RuntimeUpgrade so the
// sandboxtemplate-deletion-guard webhook can refuse a delete.

// seed builds a memory store carrying one record for pool.
func seed(t *testing.T, rec runtimeupgradestore.Record) *runtimeupgradestore.Memory {
	t.Helper()
	m := runtimeupgradestore.NewMemory()
	if _, err := m.Put(context.Background(), rec, 0); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	return m
}

func get(t *testing.T, h *runtimeupgradeguard.Handler, pool string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/internal/runtime-upgrade/active?pool="+pool, nil)
	h.ServeHTTP(rr, req)
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
	return body
}

func TestActiveWhenUpgradeNonTerminal_spec_10_5_508(t *testing.T) {
	h := &runtimeupgradeguard.Handler{Store: seed(t, runtimeupgradestore.Record{
		Pool:  "coding-agents",
		Phase: "draining",
	})}
	rr := get(t, h, "coding-agents")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := decode(t, rr)
	if body["active"] != true {
		t.Errorf("active = %v, want true for a draining upgrade", body["active"])
	}
	if body["phase"] != "draining" {
		t.Errorf("phase = %v, want draining", body["phase"])
	}
	if body["schemaGated"] != false {
		t.Errorf("schemaGated = %v, want false with no schemaVersion", body["schemaGated"])
	}
}

func TestPausedUpgradeStaysActive_spec_10_5_508(t *testing.T) {
	// A paused upgrade has not completed; the old template must remain
	// protected until the state machine reaches Complete.
	h := &runtimeupgradeguard.Handler{Store: seed(t, runtimeupgradestore.Record{
		Pool:  "coding-agents",
		Phase: "paused",
	})}
	if body := decode(t, get(t, h, "coding-agents")); body["active"] != true {
		t.Errorf("active = %v, want true for a paused upgrade", body["active"])
	}
}

func TestSchemaGatedWhenSchemaVersionSet_spec_10_5_502(t *testing.T) {
	h := &runtimeupgradeguard.Handler{Store: seed(t, runtimeupgradestore.Record{
		Pool:          "coding-agents",
		Phase:         "expanding",
		SchemaVersion: "2025-06-08-workspace-v2",
	})}
	body := decode(t, get(t, h, "coding-agents"))
	if body["active"] != true || body["schemaGated"] != true {
		t.Errorf("active=%v schemaGated=%v, want both true (§10.5 line 502 Phase 3 gate)", body["active"], body["schemaGated"])
	}
}

func TestCompleteUpgradeIsInactive_spec_10_5_508(t *testing.T) {
	h := &runtimeupgradeguard.Handler{Store: seed(t, runtimeupgradestore.Record{
		Pool:  "coding-agents",
		Phase: "complete",
	})}
	body := decode(t, get(t, h, "coding-agents"))
	if body["active"] != false {
		t.Errorf("active = %v, want false for a complete upgrade", body["active"])
	}
}

func TestUnknownPoolIsInactive_spec_10_5_508(t *testing.T) {
	h := &runtimeupgradeguard.Handler{Store: runtimeupgradestore.NewMemory()}
	body := decode(t, get(t, h, "no-such-pool"))
	if body["active"] != false {
		t.Errorf("active = %v, want false for an unregistered pool", body["active"])
	}
}

func TestMissingPoolParamIs400(t *testing.T) {
	h := &runtimeupgradeguard.Handler{Store: runtimeupgradestore.NewMemory()}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/internal/runtime-upgrade/active", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing pool", rr.Code)
	}
}

func TestNonGetIs405(t *testing.T) {
	h := &runtimeupgradeguard.Handler{Store: runtimeupgradestore.NewMemory()}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/internal/runtime-upgrade/active?pool=x", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// faultyStore returns an error from Get so the handler must fail closed.
type faultyStore struct{}

func (faultyStore) Get(context.Context, string) (runtimeupgradestore.Record, bool, error) {
	return runtimeupgradestore.Record{}, false, errors.New("postgres unreachable")
}

func TestStoreErrorIs503_failClosed(t *testing.T) {
	h := &runtimeupgradeguard.Handler{Store: faultyStore{}}
	rr := get(t, h, "coding-agents")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 so the webhook fails closed", rr.Code)
	}
}
