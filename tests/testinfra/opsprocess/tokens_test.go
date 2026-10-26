// SPDX-License-Identifier: MIT

package opsprocess

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeOps answers GET /v1/admin/me with the stale-set refusal for its first
// staleCalls requests and then with 200 for the bearer "good", the revoked
// response for "revoked", the role gate's 403 for "roleless", the bad-kid
// refusal for "forged", and the playground refusal for "playground".
func fakeOps(t *testing.T, staleCalls int32) *Process {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) <= staleCalls {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"token_validation_unavailable","details":{"retryable":true}}}`))
			return
		}
		switch strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
		case "good":
			_, _ = w.Write([]byte(`{"identity":{"sub":"carol@acme.com"}}`))
		case "roleless":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN"}}`))
		case "forged":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"TOKEN_INVALID"}}`))
		case "playground":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED","details":{"reason":"bearer_revoked"}}}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"TOKEN_REVOKED"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return &Process{baseURL: srv.URL}
}

// spec: 25.4 (The lenny-ops Service: Authentication, revocation set rules 3, 4, and 7)
// diagnosis: the token helpers misread lenny-ops's answers, so a tier-4 or
// higher test would wait on the wrong condition: an acceptance assertion
// running before the first revocation set read, or a refusal decoded with
// the wrong code or reason.
func TestTokenHelpersReadLennyOpsAnswers(t *testing.T) {
	p := fakeOps(t, 2)
	if o := p.Me(t, "good"); o.Status != http.StatusServiceUnavailable || o.Code != CodeStale {
		t.Fatalf("first answer = %+v, want the stale-set refusal", o)
	}
	if o := p.WaitAccepted(t, "good", 5*time.Second); o.Subject != "carol@acme.com" {
		t.Errorf("WaitAccepted subject = %q", o.Subject)
	}
	if o := p.WaitRefused(t, "revoked", http.StatusUnauthorized, CodeRevoked, time.Second); o.Code != CodeRevoked {
		t.Errorf("WaitRefused code = %q", o.Code)
	}
	if o := p.Me(t, "playground"); o.Code != CodeUnauthorized || o.Reason != "bearer_revoked" {
		t.Errorf("playground refusal = %+v", o)
	}
	if code := p.Healthz(t); code != http.StatusOK {
		t.Errorf("Healthz = %d", code)
	}
	p.RequireRefusedThroughout(t, "revoked", http.StatusUnauthorized, CodeRevoked, 600*time.Millisecond)
}

// spec: 25.4 (The lenny-ops Service: Authentication, revocation set rules 1, 3, and 4)
// diagnosis: WaitAuthenticated or Outcome.Authenticated misclassifies an
// answer, so a tier-4 or higher revocation check of a token without an
// admin role would either start before the first revocation set read
// (counting the stale-set 503 as admitted) or count a refusal by the key
// set or the revocation set as an authentication pass.
func TestAuthenticatedSeparatesTheRoleGateFromAuthenticationRefusals(t *testing.T) {
	p := fakeOps(t, 2)
	if o := p.Me(t, "roleless"); o.Authenticated() {
		t.Fatalf("stale-set refusal %+v counted as authenticated", o)
	}
	if o := p.WaitAuthenticated(t, "roleless", 5*time.Second); o.Status != http.StatusForbidden || o.Code != CodeForbidden {
		t.Errorf("WaitAuthenticated(roleless) = %+v, want the role gate's 403", o)
	}
	if o := p.WaitAuthenticated(t, "good", time.Second); o.Status != http.StatusOK {
		t.Errorf("WaitAuthenticated(good) = %+v, want 200", o)
	}
	for _, bearer := range []string{"revoked", "forged", "playground"} {
		if o := p.Me(t, bearer); o.Authenticated() {
			t.Errorf("refusal of %q (%+v) counted as authenticated", bearer, o)
		}
	}
	ft := &failRecorder{TB: t}
	func() {
		defer func() { _ = recover() }()
		p.WaitAuthenticated(ft, "revoked", 600*time.Millisecond)
	}()
	if !ft.failed {
		t.Error("WaitAuthenticated did not fail for a bearer lenny-ops refuses as revoked")
	}
}

// spec: 25.4 (The lenny-ops Service: Authentication, revocation set rule 4)
// diagnosis: RequireRefusedThroughout passes when the refusal holds only
// for part of its window, so a tier-4 rule-4 check would not catch a
// lenny-ops that accepts a token before its first successful read.
func TestRequireRefusedThroughoutFailsWhenTheRefusalEnds(t *testing.T) {
	p := fakeOps(t, 2)
	ft := &failRecorder{TB: t}
	func() {
		defer func() { _ = recover() }()
		p.RequireRefusedThroughout(ft, "good", http.StatusServiceUnavailable, CodeStale, 2*time.Second)
	}()
	if !ft.failed {
		t.Fatal("RequireRefusedThroughout did not fail when the stale-set refusal ended inside its window")
	}
	if len(AuthArgs(t)) != 1 || !strings.HasPrefix(AuthArgs(t)[0], "--bearer-trust-hmac-key-file=") {
		t.Errorf("AuthArgs = %v", AuthArgs(t))
	}
}

// failRecorder records a Fatalf and stops the calling goroutine with a
// panic the test recovers, in place of runtime.Goexit.
type failRecorder struct {
	testing.TB
	failed bool
}

func (f *failRecorder) Fatalf(string, ...any) {
	f.failed = true
	panic("fatal")
}

func (f *failRecorder) Helper() {}
