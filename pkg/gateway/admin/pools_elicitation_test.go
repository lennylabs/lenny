// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// TestCreatePoolPersistsElicitationPolicy_spec_9_2 proves the §9.2
// per-pool elicitation policy flows from the admin POST body into the
// store and is echoed back on the response. F-9.2.12.
func TestCreatePoolPersistsElicitationPolicy_spec_9_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                       "pool-oauth",
		RuntimeRef:                 "echo",
		ExecutionMode:              "session",
		ElicitationDepthPolicy:     "suppress_at_depth",
		ElicitationSuppressAtDepth: 2,
		URLModeElicitation: &admin.URLModeElicitationPayload{
			Enabled:         true,
			DomainAllowlist: []string{"accounts.example.com"},
		},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "pool-oauth")
	if row.ElicitationDepthPolicy != elicitation.DepthSuppressAtDepth {
		t.Errorf("stored depthPolicy = %q, want suppress_at_depth", row.ElicitationDepthPolicy)
	}
	if row.ElicitationSuppressAtDepth != 2 {
		t.Errorf("stored suppressAtDepth = %d, want 2", row.ElicitationSuppressAtDepth)
	}
	if !row.URLModeElicitation.Enabled || len(row.URLModeElicitation.DomainAllowlist) != 1 {
		t.Errorf("stored url-mode = %+v, want enabled with 1 domain", row.URLModeElicitation)
	}
	// The response echoes the policy.
	var got admin.PoolPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ElicitationDepthPolicy != "suppress_at_depth" || got.URLModeElicitation == nil ||
		!got.URLModeElicitation.Enabled {
		t.Errorf("response did not echo the elicitation policy: %+v", got)
	}
}

// TestCreatePoolRejectsURLModeWithoutDomain_spec_9_2_86 proves the §9.2
// line 86 rule surfaces as 400 URL_MODE_ELICITATION_DOMAIN_REQUIRED.
// F-9.2.12.
func TestCreatePoolRejectsURLModeWithoutDomain_spec_9_2_86(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:               "pool-bad",
		RuntimeRef:         "echo",
		ExecutionMode:      "session",
		URLModeElicitation: &admin.URLModeElicitationPayload{Enabled: true},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error.Code != "URL_MODE_ELICITATION_DOMAIN_REQUIRED" {
		t.Errorf("error code = %q, want URL_MODE_ELICITATION_DOMAIN_REQUIRED (body=%s)", body.Error.Code, rr.Body.String())
	}
}

// TestCreatePoolRejectsBadDepthPolicy_spec_9_2 proves an unrecognised
// §9.2 depth policy is rejected at admission. F-9.2.12.
func TestCreatePoolRejectsBadDepthPolicy_spec_9_2(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                   "pool-bad",
		RuntimeRef:             "echo",
		ExecutionMode:          "session",
		ElicitationDepthPolicy: "occasionally",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad depth policy: got %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestUpdatePoolElicitationPolicy_spec_9_2 proves a PUT can set the §9.2
// elicitation policy and that the line 86 rule is enforced on update.
// F-9.2.12.
func TestUpdatePoolElicitationPolicy_spec_9_2(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	if rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name: "p", RuntimeRef: "echo", ExecutionMode: "session",
	}); rr.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", rr.Code, rr.Body.String())
	}

	// A valid update sets the policy.
	depth := "block_all"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{
		ElicitationDepthPolicy: &depth,
		URLModeElicitation: &admin.URLModeElicitationPayload{
			Enabled: true, DomainAllowlist: []string{"accounts.example.com"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "p")
	if row.ElicitationDepthPolicy != elicitation.DepthBlockAll || !row.URLModeElicitation.Enabled {
		t.Errorf("stored policy after update = %+v / %+v", row.ElicitationDepthPolicy, row.URLModeElicitation)
	}

	// An update that enables url-mode without a domain is rejected.
	rr = poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{
		URLModeElicitation: &admin.URLModeElicitationPayload{Enabled: true, DomainAllowlist: nil},
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("update url-mode without domain: got %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}
