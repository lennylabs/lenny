// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// TestCreatePoolRejectsCodingAgentStandard_spec_26_2_38 asserts the
// gateway rejects a pool that pairs a §26.1 coding-agent runtime with
// standard (runc) isolation, even when allowStandardIsolation is set.
func TestCreatePoolRejectsCodingAgentStandard_spec_26_2_38(t *testing.T) {
	router, store, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                   "coding-pool",
		RuntimeRef:             "claude-code",
		IsolationProfile:       "standard",
		AllowStandardIsolation: true,
		ExecutionMode:          "session",
		ResourceClass:          "small",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "coding-agent") {
		t.Errorf("error body should name the coding-agent rule: %s", rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "coding-pool"); err == nil {
		t.Errorf("rejected pool must not be stored")
	}
}

// TestCreatePoolAllowsCodingAgentSandboxed_spec_26_2_38 confirms the
// rule does not block the spec-default sandboxed profile.
func TestCreatePoolAllowsCodingAgentSandboxed_spec_26_2_38(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:             "coding-pool",
		RuntimeRef:       "claude-code",
		IsolationProfile: "sandboxed",
		ExecutionMode:    "session",
		ResourceClass:    "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestCreatePoolAllowsNonCodingAgentStandard_spec_26_2_38 confirms the
// coding-agent rule is scoped: a non-coding-agent runtime may still use
// standard isolation under the generic §5.3 allowStandardIsolation opt-in.
func TestCreatePoolAllowsNonCodingAgentStandard_spec_26_2_38(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})

	rr := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                   "echo-pool",
		RuntimeRef:             "echo",
		IsolationProfile:       "standard",
		AllowStandardIsolation: true,
		ExecutionMode:          "session",
		ResourceClass:          "small",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
}

// TestUpdatePoolRejectsCodingAgentStandard_spec_26_2_38 asserts the PUT
// path catches a runtimeRef change that would leave a standard-isolation
// pool bound to a coding-agent runtime.
func TestUpdatePoolRejectsCodingAgentStandard_spec_26_2_38(t *testing.T) {
	router, _, runtimes, _ := newPoolAdmin(t)
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "claude-code"})

	create := poolReq(t, router.Handler(), http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                   "p",
		RuntimeRef:             "echo",
		IsolationProfile:       "standard",
		AllowStandardIsolation: true,
		ExecutionMode:          "session",
		ResourceClass:          "small",
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status: %d, body=%s", create.Code, create.Body.String())
	}

	ref := "claude-code"
	rr := poolReq(t, router.Handler(), http.MethodPut, "/v1/admin/pools/p", admin.UpdatePoolRequest{
		RuntimeRef: &ref,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "coding-agent") {
		t.Errorf("error body should name the coding-agent rule: %s", rr.Body.String())
	}
}
