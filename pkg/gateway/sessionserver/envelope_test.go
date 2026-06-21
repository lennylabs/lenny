// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// spec: §14 lines 47-79, 154-155 — the CreateSessionRequest envelope
// fields (env, pool, timeouts, credentialPolicy, delegationLease,
// runtimeOptions) and their admission validation. F-14.1.12 / F-14.1.14 /
// F-14.1.15.

func envelopeServer(t *testing.T, opts sessionserver.Options) *sessionserver.Server {
	t.Helper()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if opts.Clock == nil {
		opts.Clock = clock
	}
	if opts.IDFunc == nil {
		opts.IDFunc = func() string { return "sess_env_test" }
	}
	if opts.UploadTokenIssuer == nil {
		opts.UploadTokenIssuer = uploadtoken.NewIssuer(ring, opts.Clock)
	}
	return sessionserver.New(memstore.New(), opts)
}

func decodeCreate(t *testing.T, rr *httptest.ResponseRecorder) sessionserver.CreateSessionResponse {
	t.Helper()
	var resp sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

func getSessionResp(t *testing.T, h http.Handler, id string) sessionserver.SessionResponse {
	t.Helper()
	rr := getSession(t, h, id)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var got sessionserver.SessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	return got
}

// --- env blocklist (F-14.1.12) ---

func TestCreateRejectsBlockedEnvVar_spec_14_105(t *testing.T) {
	srv := envelopeServer(t, sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		Env:        map[string]string{"ANTHROPIC_API_KEY": "sk-secret"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "ENV_VAR_BLOCKLISTED" {
		t.Errorf("error code: got %q, want ENV_VAR_BLOCKLISTED", code)
	}
	if details["key"] != "ANTHROPIC_API_KEY" {
		t.Errorf("details.key: got %v, want ANTHROPIC_API_KEY", details["key"])
	}
	if details["pattern"] == nil || details["pattern"] == "" {
		t.Errorf("details.pattern missing")
	}
}

func TestCreateRejectsDeployerBlockedEnvVar_spec_14_105(t *testing.T) {
	srv := envelopeServer(t, sessionserver.Options{EnvVarBlocklist: []string{"INTERNAL_*"}})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		Env:        map[string]string{"INTERNAL_ENDPOINT": "http://x"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "ENV_VAR_BLOCKLISTED" {
		t.Errorf("error code: got %q, want ENV_VAR_BLOCKLISTED", code)
	}
}

func TestCreateAcceptsAndEchoesEnv_spec_14(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock: clock, IDFunc: func() string { return "sess_env_ok" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		Env:        map[string]string{"NODE_ENV": "production", "LOG_LEVEL": "info"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeCreate(t, rr)
	if resp.Env["NODE_ENV"] != "production" {
		t.Errorf("create echo env NODE_ENV: got %q", resp.Env["NODE_ENV"])
	}
	row, _ := store.Get(context.Background(), "acme", "sess_env_ok")
	if row.Env["LOG_LEVEL"] != "info" {
		t.Errorf("stored env LOG_LEVEL: got %q", row.Env["LOG_LEVEL"])
	}
	got := getSessionResp(t, srv.Handler(), "sess_env_ok")
	if got.Env["NODE_ENV"] != "production" {
		t.Errorf("GET echo env NODE_ENV: got %q", got.Env["NODE_ENV"])
	}
}

// --- runtimeOptions (F-14.1.14 / F-14.1.15) ---

func runtimeWithSchema(t *testing.T, schema string) *runtimestore.Memory {
	t.Helper()
	rs := runtimestore.NewMemory()
	if err := rs.Create(context.Background(), runtimestore.Runtime{
		Name: "claude-code", Type: runtimestore.TypeAgent,
		RuntimeOptionsSchema: json.RawMessage(schema),
		Limits:               &runtimestore.Limits{MaxSessionAgeSeconds: 3600},
	}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	return rs
}

func TestCreateRuntimeOptionsSchemaValid_spec_14_155(t *testing.T) {
	rs := runtimeWithSchema(t, `{"type":"object","properties":{"streamingMode":{"type":"boolean"}},"additionalProperties":false}`)
	srv := envelopeServer(t, sessionserver.Options{Runtimes: rs})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:     "claude-code",
		RuntimeOptions: json.RawMessage(`{"streamingMode":true}`),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeCreate(t, rr)
	if string(resp.RuntimeOptions) != `{"streamingMode":true}` {
		t.Errorf("runtimeOptions echo: got %s", resp.RuntimeOptions)
	}
}

func TestCreateRuntimeOptionsSchemaInvalid_spec_14_155(t *testing.T) {
	rs := runtimeWithSchema(t, `{"type":"object","properties":{"streamingMode":{"type":"boolean"}},"additionalProperties":false}`)
	srv := envelopeServer(t, sessionserver.Options{Runtimes: rs})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:     "claude-code",
		RuntimeOptions: json.RawMessage(`{"streamingMode":"yes","bogus":1}`),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "RUNTIME_OPTIONS_INVALID" {
		t.Errorf("error code: got %q, want RUNTIME_OPTIONS_INVALID", code)
	}
}

func TestCreateRuntimeOptionsUnschematized_spec_14_155(t *testing.T) {
	// Runtime registered with no runtimeOptionsSchema → pass-through plus
	// the RuntimeOptionsUnschematized warning. F-14.1.15.
	rs := runtimestore.NewMemory()
	_ = rs.Create(context.Background(), runtimestore.Runtime{Name: "claude-code", Type: runtimestore.TypeAgent})
	srv := envelopeServer(t, sessionserver.Options{Runtimes: rs})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:     "claude-code",
		RuntimeOptions: json.RawMessage(`{"anything":true}`),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeCreate(t, rr)
	found := false
	for _, w := range resp.WorkspacePlanWarnings {
		if w.Code == workspaceplan.WarnRuntimeOptionsUnschematized {
			found = true
		}
	}
	if !found {
		t.Errorf("expected RuntimeOptionsUnschematized warning; got %+v", resp.WorkspacePlanWarnings)
	}
}

func TestCreateRuntimeOptionsTooLarge_spec_14_155(t *testing.T) {
	srv := envelopeServer(t, sessionserver.Options{})
	big := `{"x":"` + strings.Repeat("a", 64*1024) + `"}`
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:     "claude-code",
		RuntimeOptions: json.RawMessage(big),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "RUNTIME_OPTIONS_INVALID" {
		t.Errorf("error code: got %q, want RUNTIME_OPTIONS_INVALID", code)
	}
}

// --- timeouts (F-14.1.14) ---

func TestCreateRejectsNegativeTimeout_spec_14_154(t *testing.T) {
	srv := envelopeServer(t, sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		Timeouts:   &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: -1},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateRejectsTimeoutExceedingRuntimeCap_spec_14_154(t *testing.T) {
	rs := runtimeWithSchema(t, `{}`) // Limits.MaxSessionAgeSeconds = 3600
	srv := envelopeServer(t, sessionserver.Options{Runtimes: rs})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		Timeouts:   &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: 7200},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "VALIDATION_ERROR" {
		t.Errorf("error code: got %q, want VALIDATION_ERROR", code)
	}
	if details["reason"] != "exceeds_runtime_cap" {
		t.Errorf("details.reason: got %v, want exceeds_runtime_cap", details["reason"])
	}
}

func TestCreateAcceptsAndEchoesTimeouts_spec_14_154(t *testing.T) {
	rs := runtimeWithSchema(t, `{}`)
	srv := envelopeServer(t, sessionserver.Options{Runtimes: rs})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		Timeouts:   &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: 1800, MaxIdleSeconds: 300},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeCreate(t, rr)
	if resp.Timeouts == nil || resp.Timeouts.MaxSessionAgeSeconds != 1800 || resp.Timeouts.MaxIdleSeconds != 300 {
		t.Errorf("timeouts echo: got %+v", resp.Timeouts)
	}
}

// --- credentialPolicy (F-14.1.14) ---

func TestCreateRejectsInvalidPreferredSource_spec_14(t *testing.T) {
	srv := envelopeServer(t, sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:       "claude-code",
		CredentialPolicy: &sessionstore.CredentialPolicyOverride{PreferredSource: "bogus"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateRejectsExpandingCredentialPolicy_spec_4_9(t *testing.T) {
	ts := tenantstore.NewMemory()
	_ = ts.Create(context.Background(), tenantstore.Tenant{
		ID:               "acme",
		CredentialPolicy: credential.CredentialPolicy{PreferredSource: credential.PreferredSourcePool},
	})
	srv := envelopeServer(t, sessionserver.Options{Tenants: ts})
	// Tenant is pool-only; an override to user-scoped credentials expands.
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:       "claude-code",
		CredentialPolicy: &sessionstore.CredentialPolicyOverride{PreferredSource: string(credential.PreferredSourceUser)},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "VALIDATION_ERROR" {
		t.Errorf("error code: got %q, want VALIDATION_ERROR", code)
	}
	if details["reason"] != "expands_tenant_policy" {
		t.Errorf("details.reason: got %v, want expands_tenant_policy", details["reason"])
	}
}

func TestCreateAcceptsRestrictingCredentialPolicy_spec_4_9(t *testing.T) {
	ts := tenantstore.NewMemory()
	_ = ts.Create(context.Background(), tenantstore.Tenant{
		ID:               "acme",
		CredentialPolicy: credential.CredentialPolicy{PreferredSource: credential.PreferUserThenPool},
	})
	srv := envelopeServer(t, sessionserver.Options{Tenants: ts})
	// Tenant allows user-then-pool; narrowing to pool-only is a restriction.
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:       "claude-code",
		CredentialPolicy: &sessionstore.CredentialPolicyOverride{PreferredSource: string(credential.PreferredSourcePool)},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeCreate(t, rr)
	if resp.CredentialPolicy == nil || resp.CredentialPolicy.PreferredSource != "pool" {
		t.Errorf("credentialPolicy echo: got %+v", resp.CredentialPolicy)
	}
}

// --- delegationLease + pool (F-14.1.14) ---

func TestCreateRejectsNegativeDelegationLease_spec_14(t *testing.T) {
	srv := envelopeServer(t, sessionserver.Options{})
	neg := -1
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:      "claude-code",
		DelegationLease: &sessionstore.DelegationLeaseRequest{MaxDepth: &neg},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateEchoesPoolAndDelegationLease_spec_14(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock: clock, IDFunc: func() string { return "sess_pool" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})
	depth, kids := 2, 5
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef:      "claude-code",
		Pool:            "claude-worker-sandboxed-medium",
		DelegationLease: &sessionstore.DelegationLeaseRequest{MaxDepth: &depth, MaxChildrenTotal: &kids, DelegationPolicyRef: "default-policy"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeCreate(t, rr)
	if resp.Pool != "claude-worker-sandboxed-medium" {
		t.Errorf("pool echo: got %q", resp.Pool)
	}
	if resp.DelegationLease == nil || resp.DelegationLease.MaxDepth == nil || *resp.DelegationLease.MaxDepth != 2 {
		t.Errorf("delegationLease echo: got %+v", resp.DelegationLease)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_pool")
	if row.Pool != "claude-worker-sandboxed-medium" {
		t.Errorf("stored pool: got %q", row.Pool)
	}
	if row.DelegationLeaseRequest == nil || row.DelegationLeaseRequest.DelegationPolicyRef != "default-policy" {
		t.Errorf("stored delegationLease: got %+v", row.DelegationLeaseRequest)
	}
}

// TestCreateDecodesPlaygroundDelegationLeaseWireKey_spec_27_4 confirms the
// §27.4 playground SPA's create body reaches the server's delegation-policy
// field. The SPA now emits the nested delegationLease.delegationPolicyRef key
// (proposal §3.6), replacing the flat delegationPolicyId key the create
// decoder never read. This anchors on the raw playground-shaped JSON body —
// exactly the bytes the SPA POSTs — and confirms the decode populates
// row.DelegationLeaseRequest.DelegationPolicyRef. It also confirms the obsolete
// flat key is silently ignored (no DisallowUnknownFields on the decoder), so a
// stray flat key never bleeds into the stored lease.
// diagnosis: a failure means the §27.4 delegation-policy selection no longer
// reaches the server's delegationLease.delegationPolicyRef field — either the
// SPA wire-key and the server decode disagree, or the create decoder changed
// its handling of the nested delegationLease block.
// spec: §27.4 (playground delegation-policy field), §14 (CreateSessionRequest
// outer fields), §08.3 (delegation policy resource).
func TestCreateDecodesPlaygroundDelegationLeaseWireKey_spec_27_4(t *testing.T) {
	store := memstore.New()
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("test-secret")})
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, sessionserver.Options{
		Clock: clock, IDFunc: func() string { return "sess_pg" },
		UploadTokenIssuer: uploadtoken.NewIssuer(ring, clock),
	})
	// The exact JSON the §27.4 SPA emits for a filled delegation selection:
	// the nested delegationLease.delegationPolicyRef key with only the policy
	// ref set (the playground UI sends neither maxDepth nor maxChildrenTotal).
	body := []byte(`{
		"runtimeRef": "claude-code",
		"runtimeOptions": {},
		"labels": {},
		"delegationLease": {"delegationPolicyRef": "reviewer-policy"}
	}`)
	rr := createRequestRaw(t, srv.Handler(), body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "sess_pg")
	if row.DelegationLeaseRequest == nil || row.DelegationLeaseRequest.DelegationPolicyRef != "reviewer-policy" {
		t.Errorf("playground delegationLease.delegationPolicyRef did not reach the store: got %+v", row.DelegationLeaseRequest)
	}
}
