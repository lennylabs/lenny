// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
)

// stubVCSResolver is a vcscred.Resolver recording its inputs and
// returning a scripted credential/error.
type stubVCSResolver struct {
	cred                        vcscred.Credential
	err                         error
	gotTenant, gotURL, gotScope string
}

func (s *stubVCSResolver) Resolve(_ context.Context, tenantID, gitURL, leaseScope string) (vcscred.Credential, error) {
	s.gotTenant, s.gotURL, s.gotScope = tenantID, gitURL, leaseScope
	return s.cred, s.err
}

type stubVCSAuditor struct {
	rec mcptools.VCSLeaseRecord
	n   int
}

func (a *stubVCSAuditor) RecordVCSLease(_ context.Context, lease mcptools.VCSLeaseRecord) {
	a.rec = lease
	a.n++
}

func vcsTokenServer(t *testing.T, r vcscred.Resolver, a mcptools.VCSLeaseAuditor) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		TenantID:        "fallback",
		VCSCreds:        r,
		VCSLeaseAuditor: a,
	})
	return srv
}

func dispatchVCS(t *testing.T, srv *mcp.Server, p authmw.Principal, args string) mcp.ToolResult {
	t.Helper()
	ctx := authmw.WithPrincipal(context.Background(), p)
	res, ok, err := srv.DispatchTool(ctx, "lenny/vcs_token", json.RawMessage(args))
	if !ok {
		t.Fatal("lenny/vcs_token not registered")
	}
	if err != nil {
		t.Fatalf("dispatch lenny/vcs_token: %v", err)
	}
	return res
}

// TestVCSTokenMintsSessionBoundToken_spec_26_2_119 pins the §26.2 in-pod
// token path: an in-pod caller (a session-bound principal) names a host
// and receives the HTTP Basic credential the git-credential helper feeds
// to git, resolved against the session tenant's VCS pool at scope
// vcs.github.read, with the lease bound to the originating session id.
// F-26.2.5.
func TestVCSTokenMintsSessionBoundToken_spec_26_2_119(t *testing.T) {
	r := &stubVCSResolver{cred: vcscred.Credential{Username: "x-access-token", Token: "ghs_abc"}}
	a := &stubVCSAuditor{}
	srv := vcsTokenServer(t, r, a)

	res := dispatchVCS(t, srv,
		authmw.Principal{TenantID: "acme", SessionID: "sess-1", Subject: "u", CallerType: "agent"},
		`{"host":"github.com"}`)

	if res.IsError {
		t.Fatalf("unexpected isError result: %+v", res)
	}
	var out struct {
		Host, Username, Token string
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode result: %v (%s)", err, res.Content[0].Text)
	}
	if out.Host != "github.com" || out.Username != "x-access-token" || out.Token != "ghs_abc" {
		t.Fatalf("result = %+v, want github.com/x-access-token/ghs_abc", out)
	}
	// The resolver is asked for the right tenant, an HTTPS URL, and the
	// read scope (§26.2 line 119: gitClone.url is HTTPS-only in v1).
	if r.gotTenant != "acme" || r.gotURL != "https://github.com" || r.gotScope != "vcs.github.read" {
		t.Fatalf("resolver inputs = (%q,%q,%q), want (acme, https://github.com, vcs.github.read)",
			r.gotTenant, r.gotURL, r.gotScope)
	}
	// The §4.9.2 lease audit binds to the originating session id.
	if a.n != 1 || a.rec.SessionID != "sess-1" || a.rec.Host != "github.com" || a.rec.Provider != "github" || a.rec.Mode != "read" {
		t.Fatalf("audit = %d %+v, want one record bound to sess-1/github.com/github/read", a.n, a.rec)
	}
}

// TestVCSTokenWriteModeScope_spec_26_2_119 verifies mode:write maps onto
// the vcs.github.write lease scope (push). F-26.2.5.
func TestVCSTokenWriteModeScope_spec_26_2_119(t *testing.T) {
	r := &stubVCSResolver{cred: vcscred.Credential{Username: "x-access-token", Token: "ghs_w"}}
	srv := vcsTokenServer(t, r, nil)

	dispatchVCS(t, srv,
		authmw.Principal{TenantID: "acme", SessionID: "sess-1"},
		`{"host":"github.com","mode":"write"}`)

	if r.gotScope != "vcs.github.write" {
		t.Fatalf("lease scope = %q, want vcs.github.write", r.gotScope)
	}
}

// TestVCSTokenRequiresSessionPrincipal_spec_26_2_119 pins the §26.2
// session binding: a caller with no session principal (e.g. a
// gateway-edge /mcp caller, not a pod) cannot mint a session-bound VCS
// token. F-26.2.5.
func TestVCSTokenRequiresSessionPrincipal_spec_26_2_119(t *testing.T) {
	r := &stubVCSResolver{cred: vcscred.Credential{Token: "ghs_abc"}}
	srv := vcsTokenServer(t, r, nil)

	res := dispatchVCS(t, srv,
		authmw.Principal{TenantID: "acme"}, // no SessionID
		`{"host":"github.com"}`)

	if code := errorCode(t, res); code != "VALIDATION_ERROR" {
		t.Fatalf("error code = %q, want VALIDATION_ERROR", code)
	}
	if r.gotURL != "" {
		t.Error("resolver was consulted despite missing session principal")
	}
}

// TestVCSTokenRequiresHost_spec_26_2_119 verifies a missing host is a
// VALIDATION_ERROR with no resolver call. F-26.2.5.
func TestVCSTokenRequiresHost_spec_26_2_119(t *testing.T) {
	r := &stubVCSResolver{}
	srv := vcsTokenServer(t, r, nil)

	res := dispatchVCS(t, srv,
		authmw.Principal{TenantID: "acme", SessionID: "sess-1"},
		`{}`)

	if code := errorCode(t, res); code != "VALIDATION_ERROR" {
		t.Fatalf("error code = %q, want VALIDATION_ERROR", code)
	}
	if r.gotURL != "" {
		t.Error("resolver was consulted despite missing host")
	}
}

// TestVCSTokenUnsupportedHost_spec_26_2_119 maps a VCSHostUnsupported
// resolution failure onto the §15.1 GIT_CLONE_AUTH_UNSUPPORTED_HOST code,
// matching the gateway-side gitClone path. F-26.2.5.
func TestVCSTokenUnsupportedHost_spec_26_2_119(t *testing.T) {
	r := &stubVCSResolver{err: &credentialpoolstore.VCSResolveError{
		Host: "ghe.acme.com", Provider: "github", Reason: credentialpoolstore.VCSHostUnsupported,
	}}
	srv := vcsTokenServer(t, r, nil)

	res := dispatchVCS(t, srv,
		authmw.Principal{TenantID: "acme", SessionID: "sess-1"},
		`{"host":"ghe.acme.com"}`)

	if code := errorCode(t, res); code != "GIT_CLONE_AUTH_UNSUPPORTED_HOST" {
		t.Fatalf("error code = %q, want GIT_CLONE_AUTH_UNSUPPORTED_HOST", code)
	}
}

// TestVCSTokenAmbiguousHost_spec_26_2_119 maps a VCSHostAmbiguous failure
// onto GIT_CLONE_AUTH_HOST_AMBIGUOUS. F-26.2.5.
func TestVCSTokenAmbiguousHost_spec_26_2_119(t *testing.T) {
	r := &stubVCSResolver{err: &credentialpoolstore.VCSResolveError{
		Host: "github.com", Provider: "github", Reason: credentialpoolstore.VCSHostAmbiguous,
		MatchingPools: []string{"gh-a", "gh-b"},
	}}
	srv := vcsTokenServer(t, r, nil)

	res := dispatchVCS(t, srv,
		authmw.Principal{TenantID: "acme", SessionID: "sess-1"},
		`{"host":"github.com"}`)

	if code := errorCode(t, res); code != "GIT_CLONE_AUTH_HOST_AMBIGUOUS" {
		t.Fatalf("error code = %q, want GIT_CLONE_AUTH_HOST_AMBIGUOUS", code)
	}
}

// TestVCSTokenNoUsableCredentialIsConfigError_spec_26_2_119 verifies that
// a pool with no usable token (zero credential) surfaces as a
// configuration failure rather than a silent public clone, because the
// helper only calls this tool when git demanded credentials. F-26.2.5.
func TestVCSTokenNoUsableCredentialIsConfigError_spec_26_2_119(t *testing.T) {
	r := &stubVCSResolver{cred: vcscred.Credential{}} // zero: no token
	srv := vcsTokenServer(t, r, nil)

	res := dispatchVCS(t, srv,
		authmw.Principal{TenantID: "acme", SessionID: "sess-1"},
		`{"host":"github.com"}`)

	if code := errorCode(t, res); code != "GIT_CLONE_AUTH_UNSUPPORTED_HOST" {
		t.Fatalf("error code = %q, want GIT_CLONE_AUTH_UNSUPPORTED_HOST", code)
	}
}

// TestVCSTokenNotRegisteredWithoutResolver_spec_26_2_119 verifies the
// tool is absent when no resolver is wired (the minimal-gateway default).
// F-26.2.5.
func TestVCSTokenNotRegisteredWithoutResolver_spec_26_2_119(t *testing.T) {
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{TenantID: "fallback"})
	if _, ok, _ := srv.DispatchTool(context.Background(), "lenny/vcs_token", json.RawMessage(`{"host":"github.com"}`)); ok {
		t.Fatal("lenny/vcs_token registered without a VCSCreds resolver")
	}
}
