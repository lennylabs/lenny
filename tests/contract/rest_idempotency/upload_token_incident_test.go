// SPDX-License-Identifier: MIT

//go:build contract

package rest_idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// incidentClock is the settable clock the replicas of one test share.
type incidentClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *incidentClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *incidentClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// incidentReplica is one gateway replica on a shared clock: its REST
// session surface, its key ring, and an issuer over that ring, which
// stands for a holder of the ring's secrets.
type incidentReplica struct {
	ts     *httptest.Server
	ring   *signingkeys.Ring
	forger *uploadtoken.Issuer
}

// clockedReplica starts one replica over the shared state with clock.
func (s sharedState) clockedReplica(t *testing.T, clock *incidentClock) incidentReplica {
	t.Helper()
	provider, err := kms.NewLocal(s.master)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := signingkeys.Open(context.Background(),
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow, uploadtoken.DefaultTTL),
		s.keys, sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	keys := uploadtoken.NewSharedKeys(ring)
	srv := sessionserver.New(s.sessions, sessionserver.Options{
		DevTenantHeader:     true,
		Clock:               clock.Now,
		UploadTokenIssuer:   uploadtoken.NewIssuer(keys, clock.Now),
		UploadTokenVerifier: uploadtoken.NewVerifier(keys, nil, clock.Now),
	})
	ts := httptest.NewServer(ownerprincipal.Wrap(idempotencymw.Wrap(apifront.Handler(srv),
		idempotencymw.WithCredentials(s.idem, uploadtoken.NewRedactor(keys)),
		idempotencymw.Options{PrincipalFromRequest: ownerprincipal.Scope}), ownerprincipal.Alice))
	t.Cleanup(ts.Close)
	return incidentReplica{ts: ts, ring: ring, forger: uploadtoken.NewIssuer(keys, clock.Now)}
}

func newIncidentState() sharedState {
	return sharedState{
		sessions: memstore.New(),
		idem:     idempotencymw.NewMemoryStore(),
		keys:     signingkeys.NewMemoryStore(),
		master:   bytes.Repeat([]byte{0x42}, 32),
	}
}

// createSession creates a session on ts and returns its id and token.
func createSession(t *testing.T, ts *httptest.Server, path string) (string, string) {
	t.Helper()
	resp, body := post(t, ts, path, "", map[string]any{"runtimeRef": "claude-code"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s: status %d %v", path, resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	tok, _ := body["uploadToken"].(string)
	if id == "" || tok == "" {
		t.Fatalf("POST %s: id=%q uploadToken=%q, want both", path, id, tok)
	}
	return id, tok
}

// spec: §7.1 (uploadToken format and security properties), §7.4 (Upload
// Safety)
// diagnosis: a token signed with a ring key for a live session, but not the
// token the session was issued, was admitted at finalize. The verifier does
// not compare the presented token's digest with the digest the session row
// records (isIssuedToken in pkg/gateway/core/session/sessionserver), so a
// holder of a leaked key can mint tokens with any expiry for any session.
func TestAForgedTokenForALiveSessionIsRefusedOnEveryReplica(t *testing.T) {
	clock := &incidentClock{now: time.Now().UTC().Truncate(time.Second)}
	state := newIncidentState()
	a := state.clockedReplica(t, clock)
	b := state.clockedReplica(t, clock)
	id, issued := createSession(t, a.ts, "/v1/sessions")
	forged, err := a.forger.Issue(id, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]incidentReplica{"a": a, "b": b} {
		if code, errCode := finalizeWithToken(t, r.ts, id, forged); code != http.StatusUnauthorized || errCode != "UPLOAD_TOKEN_INVALID" {
			t.Errorf("forged token on replica %s: %d %s, want 401 UPLOAD_TOKEN_INVALID", name, code, errCode)
		}
	}
	if code, errCode := finalizeWithToken(t, b.ts, id, issued); code != http.StatusOK {
		t.Fatalf("the issued token on replica b: %d %s, want 200", code, errCode)
	}
}

// spec: §7.1 (uploadToken format and security properties), §15.1 (REST
// API)
// diagnosis: the uploadToken that POST /v1/sessions/start returned was
// admitted after the session started. The create-and-start row write does
// not set upload_token_consumed_at (consumeAtStart in
// pkg/gateway/core/session/sessionserver), so the token stays usable for
// its lifetime on a session that never had an upload phase.
func TestACreateAndStartTokenIsRefusedAfterStart(t *testing.T) {
	clock := &incidentClock{now: time.Now().UTC().Truncate(time.Second)}
	state := newIncidentState()
	a := state.clockedReplica(t, clock)
	b := state.clockedReplica(t, clock)
	id, tok := createSession(t, a.ts, "/v1/sessions/start")
	for name, r := range map[string]incidentReplica{"a": a, "b": b} {
		if code, errCode := finalizeWithToken(t, r.ts, id, tok); code != http.StatusGone || errCode != "UPLOAD_TOKEN_CONSUMED" {
			t.Errorf("create-and-start token on replica %s: %d %s, want 410 UPLOAD_TOKEN_CONSUMED", name, code, errCode)
		}
	}
}

// spec: §7.1 (uploadToken format and security properties), §15.1 (REST
// API)
// diagnosis: a forced rotation through POST /v1/admin/upload-token-keys/rotate
// made a legitimate token fail: a token issued before the rotation was
// refused after the new key became active, a token issued after it was
// refused on the other replica, or the route did not answer with the key it
// wrote. The rotation does not go through the scheduled write path with its
// publish lead (signingkeys.Ring.RotateNow), or the replaced key left the
// ring before its verification time.
func TestAForcedRotationKeepsEveryLegitimateTokenValid(t *testing.T) {
	clock := &incidentClock{now: time.Now().UTC().Truncate(time.Second)}
	state := newIncidentState()
	a := state.clockedReplica(t, clock)
	b := state.clockedReplica(t, clock)
	before, beforeTok := createSession(t, a.ts, "/v1/sessions")

	adminTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := authmw.WithPrincipal(r.Context(), authmw.Principal{Subject: "admin@acme.com", TenantID: "platform", Roles: []auth.Role{auth.RolePlatformAdmin}})
		admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).WithUploadTokenKeyRotator(b.ring).Handler().ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(adminTS.Close)
	resp, err := adminTS.Client().Post(adminTS.URL+"/v1/admin/upload-token-keys/rotate", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var rot struct {
		KeyID        string    `json:"keyId"`
		RotationSlot int64     `json:"rotationSlot"`
		ActiveFrom   time.Time `json:"activeFrom"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&rot)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || rot.KeyID == "" || !rot.ActiveFrom.Equal(clock.Now().Add(signingkeys.DefaultPublishLead)) {
		t.Fatalf("rotate: status %d body %+v, want 200 with a key active one publish lead ahead", resp.StatusCode, rot)
	}

	// Each replica reloads within the publish lead, then the key activates.
	clock.Advance(signingkeys.DefaultRefreshInterval)
	for _, r := range []incidentReplica{a, b} {
		if err := r.ring.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(signingkeys.DefaultPublishLead)
	for _, r := range []incidentReplica{a, b} {
		if err := r.ring.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	after, afterTok := createSession(t, a.ts, "/v1/sessions")
	if code, errCode := finalizeWithToken(t, b.ts, before, beforeTok); code != http.StatusOK {
		t.Errorf("a token issued before the rotation, on replica b after it: %d %s, want 200", code, errCode)
	}
	if code, errCode := finalizeWithToken(t, b.ts, after, afterTok); code != http.StatusOK {
		t.Errorf("a token issued after the rotation, on the other replica: %d %s, want 200", code, errCode)
	}
}
