// SPDX-License-Identifier: MIT

//go:build contract

// §15.2.1 `RegisterAdapterUnderTest` test matrix — error classes.
// The spec (§15.2.1) requires the contract harness to
// exercise the full error-class set across the REST surface and its MCP
// `create_session` counterpart, asserting identical `code`, `category`,
// and `retryable` on each surface. Because both transports populate the
// error envelope through the one shared `pkg/gateway/errorclassify`
// table, parity holds by construction; these tests lock the matrix so a
// future divergence (a matrix code dropping out of the classifier, or
// the two surfaces resolving a code differently) fails the build.
package rest_mcp_consistency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// matrixErrorClasses is the §15.2.1 error-class list:
// the named classes plus the §15.4 session-creation rejection family.
// Per the spec the list MUST stay in lockstep with the §15.4 error code
// catalog — any session-creation rejection added to §15.4 is added here
// in the same change. spec: §15.2.1.
var matrixErrorClasses = []string{
	// Named error classes.
	"VALIDATION_ERROR",
	"QUOTA_EXCEEDED",
	"RATE_LIMITED",
	"RESOURCE_NOT_FOUND",
	"INVALID_STATE_TRANSITION",
	"PERMISSION_DENIED",
	"CREDENTIAL_REVOKED",
	"CREDENTIAL_POOL_EXHAUSTED",
	"ISOLATION_MONOTONICITY_VIOLATED",
	// §15.4 session-creation rejection family.
	"VARIANT_ISOLATION_UNAVAILABLE",
	"REGION_CONSTRAINT_UNRESOLVABLE",
	"GIT_CLONE_AUTH_UNSUPPORTED_HOST",
	"GIT_CLONE_AUTH_HOST_AMBIGUOUS",
	"GIT_CLONE_REF_UNRESOLVABLE",
	"GIT_CLONE_REF_RESOLVE_TRANSIENT",
	"ENV_VAR_BLOCKLISTED",
	"SDK_DEMOTION_NOT_SUPPORTED",
	"POOL_DRAINING",
	"CIRCUIT_BREAKER_OPEN",
	"ERASURE_IN_PROGRESS",
	"TENANT_SUSPENDED",
	"TENANT_NOT_ACTIVE",
	"USER_INVALIDATED",
}

// spec: §15.2.1
// diagnosis: the §15.2.1 `RegisterAdapterUnderTest` matrix requires the
// `category` and `retryable` flag to be identical across REST and every
// adapter surface for each error class. Both surfaces classify through
// the shared errorclassify table, so the machine-enforceable invariant
// is that every matrix code has an explicit (non-fallback) table entry —
// a code that fell through to the `(TRANSIENT, true)` default would
// resolve identically on both surfaces yet carry the wrong contract.
// This test fails if any matrix class is missing from the classifier,
// keeping the matrix in lockstep with the §15.4 catalog.
func TestRegisterAdapterUnderTestErrorMatrixLockstep(t *testing.T) {
	validCategories := map[errorclassify.Category]bool{
		errorclassify.CategoryTransient: true,
		errorclassify.CategoryPermanent: true,
		errorclassify.CategoryPolicy:    true,
		errorclassify.CategoryUpstream:  true,
	}
	for _, code := range matrixErrorClasses {
		if !errorclassify.Known(code) {
			t.Errorf("matrix error class %q has no explicit errorclassify entry; "+
				"it would fall back to (TRANSIENT, true) and silently break the §15.2.1 parity contract", code)
			continue
		}
		cat, _ := errorclassify.Classify(code)
		if !validCategories[cat] {
			t.Errorf("matrix error class %q classifies to invalid category %q", code, cat)
		}
		// Classification is a pure table lookup: a second call returns the
		// same pair, which is exactly why REST and MCP cannot diverge.
		cat2, retry2 := errorclassify.Classify(code)
		cat1, retry1 := errorclassify.Classify(code)
		if cat1 != cat2 || retry1 != retry2 {
			t.Errorf("matrix error class %q is non-deterministic: (%q,%v) vs (%q,%v)", code, cat1, retry1, cat2, retry2)
		}
	}
}

// restErrorTriple is the (code, category, retryable) projection of the
// §15.1 REST error envelope.
type restErrorTriple struct {
	Code      string
	Category  string
	Retryable bool
}

func decodeRESTError(t *testing.T, raw []byte) restErrorTriple {
	t.Helper()
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Category  string `json:"category"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("REST error decode: %v\nbody=%s", err, raw)
	}
	return restErrorTriple{body.Error.Code, body.Error.Category, body.Error.Retryable}
}

// decodeMCPError reads the `lenny/error` content block the §15.2.1
// rule-3 envelope rides in on a tool result and projects it onto the
// same triple as the REST side.
func decodeMCPError(t *testing.T, res map[string]any) restErrorTriple {
	t.Helper()
	if _, isErr := res["_error"]; isErr {
		t.Fatalf("MCP returned a JSON-RPC transport error rather than a tool error: %v", res)
	}
	if res["isError"] != true {
		t.Fatalf("MCP tool result missing isError=true: %v", res)
	}
	contents, _ := res["content"].([]any)
	var envelope string
	for _, c := range contents {
		block, _ := c.(map[string]any)
		if block["type"] == "lenny/error" {
			envelope, _ = block["text"].(string)
			break
		}
	}
	if envelope == "" {
		t.Fatalf("MCP tool result missing lenny/error content block: %v", contents)
	}
	var body struct {
		Code      string `json:"code"`
		Category  string `json:"category"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal([]byte(envelope), &body); err != nil {
		t.Fatalf("MCP lenny/error decode: %v\nbody=%s", err, envelope)
	}
	return restErrorTriple{body.Code, body.Category, body.Retryable}
}

// spec: §15.2.1
// diagnosis: the matrix requires each error class to be "exercised with
// a canonical triggering input on POST /v1/sessions and its MCP
// create_session counterpart", asserting identical code/category/
// retryable. This drives the triggerable subset end to end through both
// surfaces; the remaining classes (quota, credential, isolation, and the
// §15.4 git/region/env rejection family) require gateway state the
// in-process echo harness cannot synthesise and are covered by the
// classifier lockstep above plus their owning subsystems' unit suites.
func TestRegisterAdapterUnderTestErrorMatrixEndToEnd(t *testing.T) {
	tsREST, tsMCP, _ := newConsistencyServers(t, "acme")

	// Seed a created session so the state-transition trigger has a target
	// whose state (`created`) rejects /start (which requires `ready`).
	restBody := []byte(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`)
	resp, raw := postJSON(t, tsREST.URL+"/v1/sessions", "acme", restBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed create: status %d, body %s", resp.StatusCode, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("seed create decode: %v\nbody=%s", err, raw)
	}

	cases := []struct {
		name     string
		wantCode string
		rest     func() []byte
		mcp      func() map[string]any
	}{
		{
			name:     "VALIDATION_ERROR",
			wantCode: "VALIDATION_ERROR",
			rest: func() []byte {
				_, b := postJSON(t, tsREST.URL+"/v1/sessions", "acme", []byte(`{}`))
				return b
			},
			mcp: func() map[string]any {
				return mcpCall(t, tsMCP.URL+"/mcp", "lenny/create_session", map[string]any{})
			},
		},
		{
			name:     "RESOURCE_NOT_FOUND",
			wantCode: "RESOURCE_NOT_FOUND",
			rest: func() []byte {
				_, b := getJSON(t, tsREST.URL+"/v1/sessions/sess_does_not_exist", "acme")
				return b
			},
			mcp: func() map[string]any {
				return mcpCall(t, tsMCP.URL+"/mcp", "lenny/get_session_status", map[string]any{
					"sessionId": "sess_does_not_exist",
				})
			},
		},
		{
			name:     "INVALID_STATE_TRANSITION",
			wantCode: "INVALID_STATE_TRANSITION",
			rest: func() []byte {
				_, b := postJSON(t, tsREST.URL+"/v1/sessions/"+created.ID+"/start", "acme", nil)
				return b
			},
			mcp: func() map[string]any {
				return mcpCall(t, tsMCP.URL+"/mcp", "lenny/start_session", map[string]any{
					"sessionId": created.ID,
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rest := decodeRESTError(t, tc.rest())
			mcp := decodeMCPError(t, tc.mcp())

			if rest.Code != tc.wantCode {
				t.Errorf("REST code = %q, want %q", rest.Code, tc.wantCode)
			}
			// The §15.2.1 rule 5(d) contract: identical code, category, and
			// retryable across the two surfaces for the same trigger.
			if rest.Code != mcp.Code {
				t.Errorf("code parity: REST=%q MCP=%q", rest.Code, mcp.Code)
			}
			if rest.Category != mcp.Category {
				t.Errorf("category parity (%s): REST=%q MCP=%q", tc.wantCode, rest.Category, mcp.Category)
			}
			if rest.Retryable != mcp.Retryable {
				t.Errorf("retryable parity (%s): REST=%v MCP=%v", tc.wantCode, rest.Retryable, mcp.Retryable)
			}
			// Cross-check against the shared classifier: the surfaces must
			// agree with the one table the spec makes authoritative.
			wantCat, wantRetry := errorclassify.Classify(tc.wantCode)
			if rest.Category != string(wantCat) || rest.Retryable != wantRetry {
				t.Errorf("REST envelope diverges from errorclassify for %q: got (%q,%v) want (%q,%v)",
					tc.wantCode, rest.Category, rest.Retryable, wantCat, wantRetry)
			}
		})
	}
}

// newFencedConsistencyServers builds the REST and MCP surfaces of the
// consistency harness over a session store that runs the creation fence
// and over the tenant and user registries the early creation gate reads.
func newFencedConsistencyServers(t *testing.T, tenants tenantstore.Store, users userstore.Store) (*httptest.Server, *httptest.Server) {
	t.Helper()
	store := memstore.New(memstore.WithCreationMarkers(sessionstore.StoreMarkers(tenants, users)))
	rest := sessionserver.New(store, sessionserver.Options{Tenants: tenants, Users: users})
	tsREST := httptest.NewServer(rest.Handler())
	t.Cleanup(tsREST.Close)
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		Store: store, SessionCreator: rest, SessionService: rest,
		Executor: executor.NewEchoExecutor(), Users: users, Tenants: tenants,
		IDFunc: func() string { return "sess_mcp_fence" }, TenantID: "acme",
	})
	tsMCP := httptest.NewServer(mcpSrv.Handler())
	t.Cleanup(tsMCP.Close)
	return tsREST, tsMCP
}

// spec: 15.2.1 (REST/MCP Consistency Contract), 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API: Error code catalog)
// diagnosis: TENANT_NOT_ACTIVE and USER_INVALIDATED are creation-fence
// rejections in the matrix list. A failure means POST /v1/sessions and MCP
// lenny/create_session answer the same trigger with a different code,
// category, retryable flag, or status, most likely because the classifier
// lacks the code and MCP falls back to (TRANSIENT, true), or because the
// creation gate is not wired on one of the two surfaces.
func TestErrorMatrixCreationFenceCodes(t *testing.T) {
	cases := []struct {
		code  string
		setup func(*testing.T, *tenantstore.Memory, *userstore.Memory)
	}{
		{"TENANT_NOT_ACTIVE", func(t *testing.T, ts *tenantstore.Memory, _ *userstore.Memory) {
			if _, err := ts.Update(context.Background(), "acme", func(tn *tenantstore.Tenant) error {
				tn.State = tenantstore.TenantStateDeleting
				return nil
			}); err != nil {
				t.Fatalf("tenant state: %v", err)
			}
		}},
		{"USER_INVALIDATED", func(t *testing.T, _ *tenantstore.Memory, us *userstore.Memory) {
			if _, err := us.Update(context.Background(), "acme", "alice@acme.com", func(u *userstore.User) error {
				u.Disabled = true
				return nil
			}); err != nil {
				t.Fatalf("disable user: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			tenants := tenantstore.NewMemory()
			users := userstore.NewMemory()
			if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
				t.Fatalf("seed tenant: %v", err)
			}
			if err := users.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme"}); err != nil {
				t.Fatalf("seed user: %v", err)
			}
			tc.setup(t, tenants, users)
			tsREST, tsMCP := newFencedConsistencyServers(t, tenants, users)

			resp, raw := postJSON(t, tsREST.URL+"/v1/sessions", "acme",
				[]byte(`{"runtimeRef":"claude-code","userId":"alice@acme.com"}`))
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("REST status = %d, want 403; body %s", resp.StatusCode, raw)
			}
			rest := decodeRESTError(t, raw)
			mcpRes := decodeMCPError(t, mcpCall(t, tsMCP.URL+"/mcp", "lenny/create_session", map[string]any{
				"runtimeRef": "claude-code", "userId": "alice@acme.com",
			}))
			if rest.Code != tc.code || mcpRes.Code != tc.code {
				t.Fatalf("codes REST=%q MCP=%q, want %q", rest.Code, mcpRes.Code, tc.code)
			}
			if rest.Category != mcpRes.Category || rest.Retryable != mcpRes.Retryable {
				t.Errorf("parity: REST=(%s,%v) MCP=(%s,%v)", rest.Category, rest.Retryable, mcpRes.Category, mcpRes.Retryable)
			}
			if rest.Category != string(errorclassify.CategoryPolicy) || rest.Retryable {
				t.Errorf("REST = (%s,%v), want (POLICY,false)", rest.Category, rest.Retryable)
			}
		})
	}
}
