//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §5.2 sessionPolicy.recycle.runtimeProcess
// pool wire field: the served OpenAPI document declares its enum, and an
// admin pool create, update, and GET round-trip `keep` over the production
// handlers.
//
// spec: §5.2 (Runtime-process lifetime), §15.1 (REST API).
package rest_pools_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/openapi"
)

// diagnosis: the gateway's served /openapi.json does not declare
// Runtime.sessionPolicy.recycle.runtimeProcess with the enum keep and
// restart, so an SDK generated from it cannot set the lifetime a reusing
// recycling pool requires.
// spec: 5.2 (Runtime-process lifetime), 15.1 (REST API)
func TestServedOpenAPIDeclaresRuntimeProcessEnum(t *testing.T) {
	ts := httptest.NewServer(openapi.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("GET /openapi.json: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	type prop struct {
		Type       string          `json:"type"`
		Enum       []string        `json:"enum"`
		Properties map[string]prop `json:"properties"`
	}
	var doc struct {
		Components struct {
			Schemas map[string]prop `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rp, ok := doc.Components.Schemas["Runtime"].Properties["sessionPolicy"].Properties["recycle"].Properties["runtimeProcess"]
	if !ok {
		t.Fatal("Runtime.sessionPolicy.recycle.runtimeProcess is absent from the served document")
	}
	got := slices.Sorted(slices.Values(rp.Enum))
	if rp.Type != "string" || !slices.Equal(got, []string{"keep", "restart"}) {
		t.Errorf("runtimeProcess = type %q enum %v, want string [keep restart]", rp.Type, got)
	}
}

// keepRecycleBody is a reusing recycling pool under `keep`, bound to the
// seeded runtime.
const keepRecycleBody = `{"name":"keep-pool","runtimeRef":"nonce-only","isolationProfile":"sandboxed","acknowledgeNonceOnlyAuth":true,` +
	`"sessionPolicy":{"acknowledgeProcessLevelIsolation":true,"recycle":{"enabled":true,"acknowledgeBestEffortScrub":true,` +
	`"maxSessionsPerPod":5,"runtimeProcess":"keep"}}}`

// recycleLifetime reads sessionPolicy.recycle.runtimeProcess off a decoded
// pool response.
func recycleLifetime(pool map[string]any) any {
	sp, _ := pool["sessionPolicy"].(map[string]any)
	r, _ := sp["recycle"].(map[string]any)
	return r["runtimeProcess"]
}

// diagnosis: the admin pool create, update, or GET JSON does not round-trip
// sessionPolicy.recycle.runtimeProcess, so a deployer cannot read back the
// lifetime it set and the PoolScalingController mirror reads an empty value.
// spec: 5.2 (Runtime-process lifetime), 15.1 (REST API)
func TestAdminPoolRuntimeProcessKeepRoundTrips(t *testing.T) {
	router, _ := newPoolRouter(t)
	h := router.Handler()
	rr, created := serve(t, h, http.MethodPost, "/v1/admin/pools", json.RawMessage(keepRecycleBody), "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if recycleLifetime(created) != "keep" {
		t.Errorf("create response runtimeProcess = %v, want keep", recycleLifetime(created))
	}
	rr, got := serve(t, h, http.MethodGet, "/v1/admin/pools/keep-pool", nil, "")
	if rr.Code != http.StatusOK || recycleLifetime(got) != "keep" {
		t.Fatalf("get: %d runtimeProcess=%v, want 200 keep", rr.Code, recycleLifetime(got))
	}
	etag, _ := got["etag"].(string)
	update := json.RawMessage(`{"sessionPolicy":{"acknowledgeProcessLevelIsolation":true,"recycle":{"enabled":true,` +
		`"acknowledgeBestEffortScrub":true,"maxSessionsPerPod":7,"runtimeProcess":"keep"}}}`)
	rr, updated := serve(t, h, http.MethodPut, "/v1/admin/pools/keep-pool", update, etag)
	if rr.Code != http.StatusOK || recycleLifetime(updated) != "keep" {
		t.Fatalf("update: %d runtimeProcess=%v body=%s, want 200 keep", rr.Code, recycleLifetime(updated), rr.Body.String())
	}
	rr, reGot := serve(t, h, http.MethodGet, "/v1/admin/pools/keep-pool", nil, "")
	if rr.Code != http.StatusOK || recycleLifetime(reGot) != "keep" {
		t.Errorf("re-get: %d runtimeProcess=%v, want 200 keep", rr.Code, recycleLifetime(reGot))
	}
}
