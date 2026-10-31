// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// countingSessionStore counts the session-row reads and writes that reach
// the store, so a test can assert that a refused request touched no state.
type countingSessionStore struct {
	sessionstore.Store
	gets    atomic.Int64
	updates atomic.Int64
}

func (c *countingSessionStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	c.gets.Add(1)
	return c.Store.Get(ctx, tenantID, id)
}

func (c *countingSessionStore) Update(ctx context.Context, tenantID, id string, fn func(*sessionstore.Session) error) (sessionstore.Session, error) {
	c.updates.Add(1)
	return c.Store.Update(ctx, tenantID, id, fn)
}

// finalizeTokenFixture is a sessionserver wired with an upload-token issuer
// and verifier over a counting memstore, holding one `created` session
// whose row records the digest of the token the issuer minted for it.
type finalizeTokenFixture struct {
	h       http.Handler
	store   *countingSessionStore
	issuer  *uploadtoken.Issuer
	tracker *uploadtoken.MemoryTracker
	now     time.Time
	token   string
}

const (
	finalizeTokenTenant  = "acme"
	finalizeTokenSession = "sess_fin_tok"
)

func newFinalizeTokenFixture(t *testing.T) *finalizeTokenFixture {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("finalize-secret")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	tracker := uploadtoken.NewMemoryTracker()
	store := &countingSessionStore{Store: memstore.New()}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:     true,
		Clock:               clock,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: uploadtoken.NewVerifier(ring, tracker, clock),
	})
	tok, parsed, err := issuer.IssueDetailed(finalizeTokenSession, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	for _, id := range []string{finalizeTokenSession, "sess_other"} {
		row := sessionstore.Session{
			ID: id, TenantID: finalizeTokenTenant, UserID: "alice",
			RuntimeRef: "claude-code", State: session.StateCreated,
			CreatedAt: now, UpdatedAt: now,
		}
		if id == finalizeTokenSession {
			row.UploadTokenDigest = parsed.Digest
			row.UploadTokenExpiry = parsed.Expiry
		}
		if err := store.Store.Create(context.Background(), row); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	return &finalizeTokenFixture{h: srv.Handler(), store: store, issuer: issuer, tracker: tracker, now: now, token: tok}
}

func (f *finalizeTokenFixture) finalize(t *testing.T, token string, setHeader bool, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+finalizeTokenSession+"/finalize", nil)
	} else {
		req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+finalizeTokenSession+"/finalize", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Lenny-Tenant-ID", finalizeTokenTenant)
	if setHeader {
		req.Header.Set("X-Lenny-Upload-Token", token)
	}
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr
}

func (f *finalizeTokenFixture) row(t *testing.T) sessionstore.Session {
	t.Helper()
	row, err := f.store.Store.Get(context.Background(), finalizeTokenTenant, finalizeTokenSession)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return row
}

func finalizeErrorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body %s", err, rr.Body.String())
	}
	return env.Error.Code
}

// A finalize request is admitted only with the uploadToken issued at session
// creation. Every refused token answers the envelope admitUpload uses and
// leaves the session row unread and unwritten: the row stays `created` and
// binds no plan.
// spec: 7.4 (Upload Safety, Upload authorization), 7.1 (Normal Flow, uploadToken format and security properties), 15.1 (finalize)
func TestFinalizeRequiresUploadToken_spec_7_4(t *testing.T) {
	plan := `{"workspacePlan":{"schemaVersion":1,"sources":[{"type":"inlineFile","path":"a.txt","content":"x"}]}}`
	cases := []struct {
		name       string
		token      func(f *finalizeTokenFixture) string
		setHeader  bool
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing header",
			token:      func(*finalizeTokenFixture) string { return "" },
			setHeader:  false,
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
		{
			name:       "empty header with a plan body",
			token:      func(*finalizeTokenFixture) string { return "" },
			setHeader:  true,
			body:       plan,
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
		{
			name:       "malformed token",
			token:      func(*finalizeTokenFixture) string { return "not-a-token" },
			setHeader:  true,
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
		{
			name: "token signed under a key outside the ring",
			token: func(f *finalizeTokenFixture) string {
				other := uploadtoken.NewIssuer(uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "kx", Secret: []byte("wrong")}), func() time.Time { return f.now })
				tok, err := other.Issue(finalizeTokenSession, uploadtoken.DefaultTTL)
				if err != nil {
					panic(err)
				}
				return tok
			},
			setHeader:  true,
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
		{
			name: "tampered signature",
			token: func(f *finalizeTokenFixture) string {
				last := f.token[len(f.token)-1]
				repl := byte('0')
				if last == '0' {
					repl = '1'
				}
				return f.token[:len(f.token)-1] + string(repl)
			},
			setHeader:  true,
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
		{
			name: "expired token",
			token: func(f *finalizeTokenFixture) string {
				// Issued an hour before the verifier's clock with a one-minute TTL.
				past := uploadtoken.NewIssuer(uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("finalize-secret")}), func() time.Time { return f.now.Add(-time.Hour) })
				tok, err := past.Issue(finalizeTokenSession, time.Minute)
				if err != nil {
					panic(err)
				}
				return tok
			},
			setHeader:  true,
			body:       plan,
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_EXPIRED",
		},
		{
			name: "token for another session",
			token: func(f *finalizeTokenFixture) string {
				tok, err := f.issuer.Issue("sess_other", uploadtoken.DefaultTTL)
				if err != nil {
					panic(err)
				}
				return tok
			},
			setHeader:  true,
			body:       plan,
			wantStatus: http.StatusForbidden, wantCode: "UPLOAD_TOKEN_MISMATCH",
		},
		{
			name: "consumed token",
			token: func(f *finalizeTokenFixture) string {
				p, err := uploadtoken.NewVerifier(uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k1", Secret: []byte("finalize-secret")}), nil, func() time.Time { return f.now }).Verify(f.token, finalizeTokenSession)
				if err != nil {
					panic(err)
				}
				if err := f.tracker.MarkConsumed(p.Digest, p.Expiry); err != nil {
					panic(err)
				}
				return f.token
			},
			setHeader:  true,
			wantStatus: http.StatusGone, wantCode: "UPLOAD_TOKEN_CONSUMED",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFinalizeTokenFixture(t)
			tok := tc.token(f)
			rr := f.finalize(t, tok, tc.setHeader, tc.body)
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if got := finalizeErrorCode(t, rr); got != tc.wantCode {
				t.Errorf("code = %q, want %q", got, tc.wantCode)
			}
			if n := f.store.gets.Load(); n != 0 {
				t.Errorf("store Get calls = %d, want 0 before the token check", n)
			}
			if n := f.store.updates.Load(); n != 0 {
				t.Errorf("store Update calls = %d, want 0 before the token check", n)
			}
			row := f.row(t)
			if row.State != session.StateCreated {
				t.Errorf("state = %q, want created", row.State)
			}
			if len(row.WorkspacePlan) != 0 {
				t.Errorf("WorkspacePlan = %s, want none bound", row.WorkspacePlan)
			}
		})
	}
}

// The token issued for the session admits the finalize, which reaches
// `ready` and consumes the token, so a second finalize with the same token is
// refused as consumed rather than reaching the precondition check.
// spec: 7.4 (Upload Safety, Upload authorization), 7.1 (Normal Flow, single-use invalidation), 15.1 (finalize)
func TestFinalizeAdmitsIssuedTokenThenRefusesItsReuse_spec_7_4(t *testing.T) {
	f := newFinalizeTokenFixture(t)
	rr := f.finalize(t, f.token, true, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := f.row(t).State; got != session.StateReady {
		t.Fatalf("state = %q, want ready", got)
	}
	gets, updates := f.store.gets.Load(), f.store.updates.Load()

	rr = f.finalize(t, f.token, true, "")
	if rr.Code != http.StatusGone {
		t.Fatalf("second finalize: status %d, want 410; body %s", rr.Code, rr.Body.String())
	}
	if got := finalizeErrorCode(t, rr); got != "UPLOAD_TOKEN_CONSUMED" {
		t.Errorf("code = %q, want UPLOAD_TOKEN_CONSUMED", got)
	}
	if f.store.gets.Load() != gets || f.store.updates.Load() != updates {
		t.Errorf("refused finalize touched the store: gets %d->%d, updates %d->%d",
			gets, f.store.gets.Load(), updates, f.store.updates.Load())
	}
	if got := f.row(t).State; got != session.StateReady {
		t.Errorf("state after refused finalize = %q, want ready", got)
	}
}
