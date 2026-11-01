// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 credential-handling check for the gateway VCS credential read at
// plan binding. The gateway reads a gitClone source's token through the Token
// Service keyed read, and a read the Token Service cannot answer fails that
// read closed: session creation is refused with the retryable transient code,
// no session row is written, the ls-remote never runs, and no earlier token is
// served. The test drives the production session handler with the production
// vcscred.StoreResolver over an in-memory credential-pool store, and a
// SecretReader that serves a token once and then fails as the Token Service
// keyed read fails when the Token Service is unreachable.
//
// spec: §4.9 (Token Service secret resolution), §14, §15.1 (Error codes).
package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

const (
	vcsFailClosedToken = "ghs_failclosed_value"
	vcsFailClosedSHA   = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
)

// flakySecrets serves token on the first read and fails every later read
// with err, so a reader that cached the first value would serve it again.
type flakySecrets struct {
	token string
	err   error
	reads int
}

func (f *flakySecrets) Resolve(context.Context, string) (string, error) {
	f.reads++
	if f.reads == 1 {
		return f.token, nil
	}
	return "", f.err
}

// credRecordingLsRemote records the credential each ls-remote received.
type credRecordingLsRemote struct{ creds []workspaceplan.VCSCredential }

func (r *credRecordingLsRemote) Resolve(_ context.Context, _ workspaceplan.GitClone, cred workspaceplan.VCSCredential) (string, error) {
	r.creds = append(r.creds, cred)
	return vcsFailClosedSHA, nil
}

func vcsFailClosedCreate(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo",
		WorkspacePlan: json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"gitClone",` +
			`"url":"https://github.com/acme/private.git","ref":"main",` +
			`"auth":{"mode":"credential-lease","leaseScope":"vcs.github.read"}}]}`),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §4.9 (Token Service secret resolution) rules 2 and 3, §14, §15.1
// diagnosis: a 201 on the second create, an ls-remote carrying the first
// token, or a persisted second session means the gateway served a cached or
// earlier VCS token, or fell back to an unauthenticated clone, after the Token
// Service read failed; a 422 means a transient outage is reported as a
// configuration failure; the token in the body is a credential leak.
func TestVCSCredentialReadFailsClosedWithNoEarlierValue(t *testing.T) {
	pools := credentialpoolstore.NewMemory()
	if err := pools.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "github-pool", Provider: "github", HostPatterns: []string{"github.com"},
		Credentials: []credentialpoolstore.Credential{{ID: "c1", SecretRef: "lenny-credentials/gh-token"}},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	secrets := &flakySecrets{
		token: vcsFailClosedToken,
		err:   fmt.Errorf("read VCS token Secret %q through the Token Service: %w", "gh-token", status.Error(codes.Unavailable, "unavailable")),
	}
	ls := &credRecordingLsRemote{}
	minted, lastID := 0, ""
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		IDFunc: func() string {
			minted++
			lastID = fmt.Sprintf("sess-vcs-%d", minted)
			return lastID
		},
		RefResolver:     ls,
		CredentialPools: pools,
		VCSCredentials:  &vcscred.StoreResolver{Pools: pools, Secrets: secrets},
	})
	t.Cleanup(srv.Close)

	if rr := vcsFailClosedCreate(t, srv.Handler()); rr.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if len(ls.creds) != 1 || ls.creds[0].Token != vcsFailClosedToken {
		t.Fatalf("first ls-remote credentials = %+v, want the served token", ls.creds)
	}

	firstLastID := lastID
	rr := vcsFailClosedCreate(t, srv.Handler())
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "GIT_CLONE_REF_RESOLVE_TRANSIENT") {
		t.Fatalf("second create = %d %s, want 503 GIT_CLONE_REF_RESOLVE_TRANSIENT", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), vcsFailClosedToken) {
		t.Fatalf("error body carries the token: %s", rr.Body.String())
	}
	if len(ls.creds) != 1 {
		t.Fatalf("ls-remote ran %d times, want 1 (none after the failed read)", len(ls.creds))
	}
	if secrets.reads != 2 {
		t.Fatalf("Secret reads = %d, want 2 (one per create, no cache)", secrets.reads)
	}
	if lastID != firstLastID {
		if _, err := store.Get(context.Background(), "acme", lastID); err == nil {
			t.Fatalf("session %s was written for the refused create", lastID)
		}
	}
}
