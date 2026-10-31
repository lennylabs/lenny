// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// stubVCSCreds is a vcscred.Resolver that returns a fixed credential or
// error and records the arguments of its last call.
type stubVCSCreds struct {
	cred    vcscred.Credential
	err     error
	gotURL  string
	gotScpe string
	calls   int
}

func (s *stubVCSCreds) Resolve(_ context.Context, _ /*tenantID*/, gitURL, leaseScope string) (vcscred.Credential, error) {
	s.calls++
	s.gotURL, s.gotScpe = gitURL, leaseScope
	return s.cred, s.err
}

// spec: §14 gitClone ref resolution — the gateway pins each gitClone
// ref to an immutable commit SHA at session creation.

const pinSHA = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

// pinStubResolver is a workspaceplan.RefResolver for the pinning tests.
// lastCred records the credential the resolver was handed so a test can
// assert the §14 credential threading.
type pinStubResolver struct {
	shas     map[string]string
	err      error
	lastCred workspaceplan.VCSCredential
}

func (r *pinStubResolver) Resolve(_ context.Context, src workspaceplan.GitClone, cred workspaceplan.VCSCredential) (string, error) {
	r.lastCred = cred
	if r.err != nil {
		return "", r.err
	}
	return r.shas[src.Ref], nil
}

func gitClonePlanJSON(ref string) json.RawMessage {
	return json.RawMessage(`{"schemaVersion":1,"sources":[` +
		`{"type":"gitClone","url":"https://example.com/r.git","ref":"` + ref + `"}]}`)
}

// storedPlanSource GETs a session and returns its workspacePlan's first
// source object.
func storedPlanSource(t *testing.T, h http.Handler, id string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/sessions/%s: status %d", id, rr.Code)
	}
	var resp struct {
		WorkspacePlan json.RawMessage `json:"workspacePlan"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	var plan map[string]any
	if err := json.Unmarshal(resp.WorkspacePlan, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	srcs, ok := plan["sources"].([]any)
	if !ok || len(srcs) == 0 {
		t.Fatalf("stored plan has no sources: %v", plan)
	}
	src, ok := srcs[0].(map[string]any)
	if !ok {
		t.Fatalf("source 0 is not an object: %v", srcs[0])
	}
	return src
}

func TestCreatePinsGitCloneShaRef(t *testing.T) {
	// A ref already in commit-SHA form is pinned directly — the resolver
	// is never consulted.
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_pin_sha" },
		RefResolver:     &pinStubResolver{},
	})
	h := asOwner(srv.Handler())
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON(pinSHA),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if got := storedPlanSource(t, h, "sess_pin_sha")["resolvedCommitSha"]; got != pinSHA {
		t.Errorf("stored resolvedCommitSha = %v, want %q", got, pinSHA)
	}
}

func TestCreatePinsGitCloneBranchRef(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_pin_branch" },
		RefResolver:     &pinStubResolver{shas: map[string]string{"main": pinSHA}},
	})
	h := asOwner(srv.Handler())
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON("main"),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if got := storedPlanSource(t, h, "sess_pin_branch")["resolvedCommitSha"]; got != pinSHA {
		t.Errorf("stored resolvedCommitSha = %v, want %q", got, pinSHA)
	}
}

// spec: §14 — the gateway resolves a private gitClone ref using
// the same credential the clone will, so create-time ref pinning threads
// the materialized VCS token to the ls-remote resolver.
func TestCreateThreadsVCSCredentialToResolver(t *testing.T) {
	stub := &pinStubResolver{shas: map[string]string{"main": pinSHA}}
	creds := &stubVCSCreds{cred: vcscred.Credential{Username: "x-access-token", Token: "ghs_secret"}}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_vcs_thread" },
		RefResolver:     stub,
		VCSCredentials:  creds,
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitCloneAuthPlanJSON("github.com", "main"),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if creds.calls != 1 {
		t.Errorf("VCS resolver called %d times, want 1", creds.calls)
	}
	if creds.gotScpe != "vcs.github.read" {
		t.Errorf("resolver got leaseScope %q, want vcs.github.read", creds.gotScpe)
	}
	if stub.lastCred.Token != "ghs_secret" {
		t.Errorf("ls-remote resolver got token %q, want ghs_secret", stub.lastCred.Token)
	}
}

// spec: §14 — a credential-materialization failure fails the
// create as a non-retryable GIT_CLONE_REF_UNRESOLVABLE, not later at
// clone time.
func TestCreateVCSCredentialFailureIsUnresolvable(t *testing.T) {
	stub := &pinStubResolver{shas: map[string]string{"main": pinSHA}}
	creds := &stubVCSCreds{err: errors.New("pool exhausted")}
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_vcs_fail" },
		RefResolver:     stub,
		VCSCredentials:  creds,
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitCloneAuthPlanJSON("github.com", "main"),
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 for a credential failure; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "GIT_CLONE_REF_UNRESOLVABLE") {
		t.Errorf("body = %s, want GIT_CLONE_REF_UNRESOLVABLE", body)
	}
	if stub.lastCred.Token != "" {
		t.Error("ls-remote resolver was called despite a credential failure")
	}
}

func TestCreateGitCloneTransientResolveError(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_transient" },
		RefResolver: &pinStubResolver{err: &workspaceplan.ResolveError{
			Reason: workspaceplan.ResolveNetworkError, Err: errors.New("connection reset"),
		}},
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON("main"),
	})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 for a transient resolve failure; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "GIT_CLONE_REF_RESOLVE_TRANSIENT") {
		t.Errorf("body = %s, want GIT_CLONE_REF_RESOLVE_TRANSIENT", body)
	}
}

func TestCreateGitCloneUnresolvableError(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_unresolvable" },
		RefResolver: &pinStubResolver{err: &workspaceplan.ResolveError{
			Reason: workspaceplan.ResolveRefNotFound, Err: errors.New("ref not found"),
		}},
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON("no-such-ref"),
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 for an unresolvable ref; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "GIT_CLONE_REF_UNRESOLVABLE") {
		t.Errorf("body = %s, want GIT_CLONE_REF_UNRESOLVABLE", body)
	}
}

func TestCreateGitCloneNoResolverStoresVerbatim(t *testing.T) {
	// With no RefResolver wired the gateway stores the submitted plan
	// unchanged — a non-SHA gitClone ref is not pinned and not rejected.
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_noresolver" },
	})
	h := asOwner(srv.Handler())
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON("main"),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, pinned := storedPlanSource(t, h, "sess_noresolver")["resolvedCommitSha"]; pinned {
		t.Error("a session created without a RefResolver must not carry a pinned resolvedCommitSha")
	}
}

// spec: §14 gitClone auth host-to-pool binding — the gateway binds an
// authenticated gitClone source to exactly one VCS credential pool.

func vcsPoolStore(t *testing.T, pools ...credentialpoolstore.CredentialPool) credentialpoolstore.Store {
	t.Helper()
	store := credentialpoolstore.NewMemory()
	for _, p := range pools {
		if err := store.Create(context.Background(), p); err != nil {
			t.Fatalf("seed pool %s: %v", p.Name, err)
		}
	}
	return store
}

func githubPool(name string, patterns ...string) credentialpoolstore.CredentialPool {
	return credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: name, Provider: "github", HostPatterns: patterns,
	}
}

func gitCloneAuthPlanJSON(host, ref string) json.RawMessage {
	return json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"gitClone",` +
		`"url":"https://` + host + `/acme/r.git","ref":"` + ref + `",` +
		`"auth":{"mode":"credential-lease","leaseScope":"vcs.github.read"}}]}`)
}

func TestCreateGitCloneAuthBindsToPool(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_auth_ok" },
		RefResolver:     &pinStubResolver{},
		CredentialPools: vcsPoolStore(t, githubPool("github-pool", "github.com")),
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitCloneAuthPlanJSON("github.com", pinSHA),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateGitCloneAuthUnsupportedHost(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_auth_unsupported" },
		CredentialPools: vcsPoolStore(t, githubPool("github-pool", "github.com")),
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitCloneAuthPlanJSON("git.acme.internal", pinSHA),
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "GIT_CLONE_AUTH_UNSUPPORTED_HOST") {
		t.Errorf("body = %s, want GIT_CLONE_AUTH_UNSUPPORTED_HOST", body)
	}
}

func TestCreateGitCloneAuthAmbiguousHost(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_auth_ambiguous" },
		CredentialPools: vcsPoolStore(
			t,
			githubPool("gh-a", "github.com"),
			githubPool("gh-b", "*.com"),
		),
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitCloneAuthPlanJSON("github.com", pinSHA),
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "GIT_CLONE_AUTH_HOST_AMBIGUOUS") {
		t.Errorf("body = %s, want GIT_CLONE_AUTH_HOST_AMBIGUOUS", body)
	}
}

func TestCreateGitCloneAuthNoPoolStoreSkipsCheck(t *testing.T) {
	// With no CredentialPools store wired the §14 auth binding check is
	// skipped, so a gateway without one is unchanged.
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_auth_skip" },
		RefResolver:     &pinStubResolver{},
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitCloneAuthPlanJSON("github.com", pinSHA),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201 (auth check skipped); body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateGitClonePublicSkipsAuthCheck(t *testing.T) {
	// A gitClone source with no auth block is public; the binding check
	// does not apply even when a pool store is wired.
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return "sess_public" },
		RefResolver:     &pinStubResolver{},
		CredentialPools: vcsPoolStore(t, githubPool("github-pool", "github.com")),
	})
	rr := createRequest(t, asOwner(srv.Handler()), sessionserver.CreateSessionRequest{
		RuntimeRef: "echo", WorkspacePlan: gitClonePlanJSON(pinSHA),
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201 (a public gitClone needs no auth binding); body %s",
			rr.Code, rr.Body.String())
	}
}

// createPlanlessThenFinalize creates a session without a plan under id and
// finalizes it with a body carrying plan, returning the finalize response.
func createPlanlessThenFinalize(t *testing.T, h http.Handler, id string, plan json.RawMessage) *httptest.ResponseRecorder {
	t.Helper()
	if rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "echo"}); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	body, err := json.Marshal(map[string]json.RawMessage{"workspacePlan": plan})
	if err != nil {
		t.Fatalf("marshal finalize body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/finalize", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema)
//
// A plan bound at finalize pins each gitClone ref when it binds, exactly
// as a plan bound at creation does: the stored plan of a session created
// without a plan carries the resolver's commit SHA after finalize.
func TestFinalizePinsGitCloneBranchRef_spec_14_1(t *testing.T) {
	const id = "sess_fin_pin"
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true,
		IDFunc:          func() string { return id },
		RefResolver:     &pinStubResolver{shas: map[string]string{"main": pinSHA}},
	})
	h := asOwner(srv.Handler())
	if rr := createPlanlessThenFinalize(t, h, id, gitClonePlanJSON("main")); rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body %s", rr.Code, rr.Body.String())
	}
	if got := storedPlanSource(t, h, id)["resolvedCommitSha"]; got != pinSHA {
		t.Errorf("stored resolvedCommitSha = %v, want %q", got, pinSHA)
	}
}

// spec: 14.1 (WorkspacePlan Schema Versioning), 14 (Workspace Plan Schema), 15.1
//
// A finalize-bound plan whose gitClone source fails the auth binding or
// the ref pin is rejected with the create-time code, and the rejection
// precedes the created → finalizing write: the row stays created and binds
// no plan. The empty-plan assertion catches an ordering that persists the
// plan before the resolver rejects it.
func TestFinalizeGitCloneRejectionBindsNothing_spec_14_1(t *testing.T) {
	cases := []struct {
		name       string
		resolver   workspaceplan.RefResolver
		pools      bool
		plan       json.RawMessage
		wantStatus int
		wantCode   string
	}{
		{
			name: "unresolvable ref",
			resolver: &pinStubResolver{err: &workspaceplan.ResolveError{
				Reason: workspaceplan.ResolveRefNotFound, Err: errors.New("ref not found"),
			}},
			plan:       gitClonePlanJSON("no-such-ref"),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "GIT_CLONE_REF_UNRESOLVABLE",
		},
		{
			name: "transient resolve failure",
			resolver: &pinStubResolver{err: &workspaceplan.ResolveError{
				Reason: workspaceplan.ResolveNetworkError, Err: errors.New("connection reset"),
			}},
			plan:       gitClonePlanJSON("main"),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "GIT_CLONE_REF_RESOLVE_TRANSIENT",
		},
		{
			name:       "auth host matches no pool",
			pools:      true,
			plan:       gitCloneAuthPlanJSON("git.acme.internal", pinSHA),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "GIT_CLONE_AUTH_UNSUPPORTED_HOST",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const id = "sess_fin_reject"
			store := memstore.New()
			opts := sessionserver.Options{
				DevTenantHeader: true,
				IDFunc:          func() string { return id },
				RefResolver:     tc.resolver,
			}
			if tc.pools {
				opts.CredentialPools = vcsPoolStore(t, githubPool("github-pool", "github.com"))
			}
			rr := createPlanlessThenFinalize(t, asOwner(sessionserver.New(store, opts).Handler()), id, tc.plan)
			if rr.Code != tc.wantStatus {
				t.Fatalf("finalize: status %d, want %d; body %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if body := rr.Body.String(); !strings.Contains(body, tc.wantCode) {
				t.Errorf("body = %s, want %s", body, tc.wantCode)
			}
			row, err := store.Get(context.Background(), "acme", id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if row.State != session.StateCreated {
				t.Errorf("state = %q, want created after the rejected finalize", row.State)
			}
			if len(row.WorkspacePlan) != 0 {
				t.Errorf("stored plan = %s, want none after the rejected finalize", row.WorkspacePlan)
			}
		})
	}
}
