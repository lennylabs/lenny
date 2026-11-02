// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

const (
	rowTokenTenant  = "acme"
	rowTokenSession = "sess_row"
	rowTokenTTL     = time.Hour
)

var rowTokenNow = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// rowTokenFixture is a server over a store the test supplies, with the blob
// store and §11.2 quota counter a case asserts the unwind against.
type rowTokenFixture struct {
	h       http.Handler
	store   *countingSessionStore
	blobs   blobstore.Store
	counter *storagequota.Memory
	issuer  *uploadtoken.Issuer
}

// contextBoundCounter refuses a shift on a done context, as the Redis counter
// does. storagequota.Memory ignores its context, so without this wrapper a
// release on a cancelled request context reads as a successful release.
type contextBoundCounter struct {
	*storagequota.Memory
}

func (c contextBoundCounter) Adjust(ctx context.Context, tenantID string, delta int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Memory.Adjust(ctx, tenantID, delta)
}

func newRowTokenFixture(t *testing.T, inner sessionstore.Store) *rowTokenFixture {
	t.Helper()
	clock := func() time.Time { return rowTokenNow }
	issuer := uploadtoken.NewIssuer(clock)
	blobs := blobstore.NewMemoryStore(nil)
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: rowTokenTenant, StorageQuotaBytes: 1 << 20}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	counter := storagequota.NewMemory()
	store := &countingSessionStore{Store: inner}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:     true,
		Clock:               clock,
		UploadTokenIssuer:   issuer,
		UploadTokenVerifier: uploadtoken.NewVerifier(clock),
		Blobs:               blobs,
		Tenants:             tenants,
		StorageQuota:        contextBoundCounter{Memory: counter},
	})
	return &rowTokenFixture{h: asOwner(srv.Handler()), store: store, blobs: blobs, counter: counter, issuer: issuer}
}

// seedRow creates the session in state and returns a token matching the row's
// digest and expiry.
func (f *rowTokenFixture) seedRow(t *testing.T, inner sessionstore.Store, state session.State) string {
	t.Helper()
	tok, parsed, err := f.issuer.IssueDetailed(rowTokenSession, rowTokenTTL)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := inner.Create(context.Background(), sessionstore.Session{
		ID: rowTokenSession, TenantID: rowTokenTenant, UserID: testOwner,
		RuntimeRef: "claude-code", State: state, CreatedAt: rowTokenNow, UpdatedAt: rowTokenNow,
		UploadTokenDigest: parsed.Digest, UploadTokenExpiry: parsed.Expiry,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return tok
}

// post sends the action ("upload" or "finalize") with token.
func (f *rowTokenFixture) post(t *testing.T, action, token string) *httptest.ResponseRecorder {
	t.Helper()
	return f.postContext(t, context.Background(), action, token)
}

// postContext is post with the request bound to ctx.
func (f *rowTokenFixture) postContext(t *testing.T, ctx context.Context, action, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rowTokenSession+"/"+action, strings.NewReader("payload"))
	req.Header.Set("X-Lenny-Tenant-ID", rowTokenTenant)
	req.Header.Set("Content-Type", "text/plain")
	if action == "finalize" {
		req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+rowTokenSession+"/finalize", nil)
		req.Header.Set("X-Lenny-Tenant-ID", rowTokenTenant)
	}
	req.Header.Set("X-Lenny-Upload-Token", token)
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

// liveBlobs counts the committed blobs that are not soft-deleted.
func (f *rowTokenFixture) liveBlobs(t *testing.T) int {
	t.Helper()
	deleted, total := countSessionBlobs(t, f.blobs, rowTokenTenant, rowTokenSession)
	return total - deleted
}

// wantRefusal asserts the status and code and that nothing was written.
func (f *rowTokenFixture) wantRefusal(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rr.Code, status, rr.Body.String())
	}
	if got := finalizeErrorCode(t, rr); got != code {
		t.Errorf("code = %q, want %q", got, code)
	}
	if n := f.store.updates.Load(); n != 0 {
		t.Errorf("store Update calls = %d, want 0", n)
	}
}

// tokenActions are the endpoints the §7.1 checks govern.
var tokenActions = []string{"upload", "finalize"}

// Each state answers a valid token by the §7.1 rule. The assertion that
// discriminates is 410 on `ready` and 409 on a terminal row: a terminal row
// does not record that finalize committed.
//
// spec: §7.1 (Single-use invalidation, Check order), §15.1 (finalize and upload preconditions)
func TestValidTokenOutcomeByRowState_spec_7_1(t *testing.T) {
	type outcome struct {
		status int
		code   string
	}
	wantFor := func(action string, s session.State) outcome {
		switch {
		case s == session.StateCreated && action == "upload":
			return outcome{status: http.StatusCreated}
		case s == session.StateCreated:
			return outcome{status: http.StatusOK}
		case s == session.StateFinalizing, session.IsTerminal(s):
			return outcome{http.StatusConflict, "INVALID_STATE_TRANSITION"}
		default:
			return outcome{http.StatusGone, "UPLOAD_TOKEN_CONSUMED"}
		}
	}
	for _, state := range append(session.AllStates(), session.StateResuming) {
		for _, action := range tokenActions {
			t.Run(action+"/"+string(state), func(t *testing.T) {
				inner := memstore.New()
				f := newRowTokenFixture(t, inner)
				tok := f.seedRow(t, inner, state)
				rr := f.post(t, action, tok)
				want := wantFor(action, state)
				if rr.Code != want.status {
					t.Fatalf("status = %d, want %d; body %s", rr.Code, want.status, rr.Body.String())
				}
				if want.code != "" {
					if got := finalizeErrorCode(t, rr); got != want.code {
						t.Errorf("code = %q, want %q", got, want.code)
					}
				}
			})
		}
	}
}

// One case per adjacent pair of the §7.1 order, each writing nothing.
//
// spec: §7.1 (Check order), §15.1 (Error code catalog)
func TestTokenCheckOrder_spec_7_1(t *testing.T) {
	type rowEdit func(row *sessionstore.Session)
	expire := func(row *sessionstore.Session) { row.UploadTokenExpiry = rowTokenNow.Add(-time.Minute) }
	guessedSecret := strings.Repeat("ab", 32)
	cases := []struct {
		name       string
		state      session.State
		edit       rowEdit
		token      func(valid string) string
		wantStatus int
		wantCode   string
	}{
		{
			name: "scope precedes digest", state: session.StateCreated,
			token:      func(string) string { return "sess_foreign.1893456000." + guessedSecret },
			wantStatus: http.StatusForbidden, wantCode: "UPLOAD_TOKEN_MISMATCH",
		},
		{
			name: "digest precedes expiry", state: session.StateCreated, edit: expire,
			token:      func(string) string { return rowTokenSession + ".1893456000." + guessedSecret },
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
		{
			name: "expiry precedes consumption", state: session.StateReady, edit: expire,
			token:      func(valid string) string { return valid },
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_EXPIRED",
		},
		{
			name: "expiry precedes the precondition", state: session.StateCompleted, edit: expire,
			token:      func(valid string) string { return valid },
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_EXPIRED",
		},
		{
			name: "digest precedes the precondition", state: session.StateCompleted,
			token:      func(string) string { return rowTokenSession + ".1893456000." + guessedSecret },
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
		{
			name: "row with no digest", state: session.StateCreated,
			edit:       func(row *sessionstore.Session) { row.UploadTokenDigest = "" },
			token:      func(valid string) string { return valid },
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
		{
			name: "derive_failure row", state: session.StateFailed,
			edit: func(row *sessionstore.Session) {
				row.FailureClass = session.FailureClassDeriveFailure
				row.UploadTokenDigest = ""
			},
			token:      func(valid string) string { return valid },
			wantStatus: http.StatusUnauthorized, wantCode: "UPLOAD_TOKEN_INVALID",
		},
	}
	for _, tc := range cases {
		for _, action := range tokenActions {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				inner := memstore.New()
				f := newRowTokenFixture(t, inner)
				tok := f.seedRow(t, inner, tc.state)
				if tc.edit != nil {
					if _, err := inner.Update(context.Background(), rowTokenTenant, rowTokenSession, func(row *sessionstore.Session) error {
						tc.edit(row)
						return nil
					}); err != nil {
						t.Fatalf("edit row: %v", err)
					}
				}
				f.wantRefusal(t, f.post(t, action, tc.token(tok)), tc.wantStatus, tc.wantCode)
			})
		}
	}
}

// The token POST /v1/sessions/start returns is consumed when it is issued.
//
// spec: §7.1 (Single-use invalidation)
func TestCreateAndStartTokenIsConsumedOnIssue_spec_7_1(t *testing.T) {
	for _, action := range tokenActions {
		t.Run(action, func(t *testing.T) {
			inner := memstore.New()
			f := newRowTokenFixture(t, inner)
			tok := f.seedRow(t, inner, session.StateRunning)
			f.wantRefusal(t, f.post(t, action, tok), http.StatusGone, "UPLOAD_TOKEN_CONSUMED")
		})
	}
}

// laggingStore answers Get from a replica that stays at `created`, and scripts
// the primary reads in order. Past the script's end the last entry repeats.
type laggingStore struct {
	*memstore.Store
	mu      sync.Mutex
	primary []func(row sessionstore.Session) (sessionstore.Session, error)
}

func (l *laggingStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := l.Store.Get(ctx, tenantID, id)
	if err == nil {
		row.State = session.StateCreated
	}
	return row, err
}

func (l *laggingStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	l.mu.Lock()
	next := l.primary[0]
	if len(l.primary) > 1 {
		l.primary = l.primary[1:]
	}
	l.mu.Unlock()
	row, err := l.Store.GetPrimary(ctx, tenantID, id)
	if err != nil {
		return row, err
	}
	return next(row)
}

func primaryIn(state session.State) func(sessionstore.Session) (sessionstore.Session, error) {
	return func(row sessionstore.Session) (sessionstore.Session, error) {
		row.State = state
		return row, nil
	}
}

func primaryFails(err error) func(sessionstore.Session) (sessionstore.Session, error) {
	return func(sessionstore.Session) (sessionstore.Session, error) {
		return sessionstore.Session{}, err
	}
}

// A lagging replica row in `created` admits nothing, which fails if either
// site reads Get.
//
// spec: §7.1 (Single-use invalidation), §12.3 (Postgres HA Requirements)
func TestTokenChecksReadThePrimaryRow_spec_7_1(t *testing.T) {
	for _, action := range tokenActions {
		t.Run(action, func(t *testing.T) {
			inner := &laggingStore{Store: memstore.New()}
			inner.primary = append(inner.primary, primaryIn(session.StateReady))
			f := newRowTokenFixture(t, inner)
			tok := f.seedRow(t, inner.Store, session.StateCreated)
			f.wantRefusal(t, f.post(t, action, tok), http.StatusGone, "UPLOAD_TOKEN_CONSUMED")
		})
	}
}

// spec: §7.1 (Check order), §12.3 (Postgres HA Requirements)
func TestPrimaryReadErrorRefusesTheRequest_spec_7_1(t *testing.T) {
	for _, action := range tokenActions {
		t.Run(action, func(t *testing.T) {
			inner := &laggingStore{Store: memstore.New()}
			inner.primary = append(inner.primary, primaryFails(errors.New("primary down")))
			f := newRowTokenFixture(t, inner)
			tok := f.seedRow(t, inner.Store, session.StateCreated)
			f.wantRefusal(t, f.post(t, action, tok), http.StatusInternalServerError, "INTERNAL_ERROR")
		})
	}
}

// Every refused recheck soft-deletes the blob and releases the reservation.
// The 404 and 500 rows assert the corrected outcome: a failed read used to fall
// through to 201 with the blob committed.
//
// spec: §7.4 (Upload Safety), §7.1 (Check order), §12.3 (Postgres HA Requirements)
func TestPostPutRecheckRefusesEveryFailedRead_spec_7_4(t *testing.T) {
	cases := []struct {
		name       string
		recheck    func(sessionstore.Session) (sessionstore.Session, error)
		wantStatus int
		wantCode   string
	}{
		{"row moved to ready", primaryIn(session.StateReady), http.StatusGone, "UPLOAD_CHANNEL_CLOSED"},
		{"row vanished", primaryFails(sessionstore.ErrNotFound), http.StatusNotFound, "RESOURCE_NOT_FOUND"},
		{"primary read failed", primaryFails(errors.New("primary down")), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := &laggingStore{Store: memstore.New()}
			inner.primary = append(inner.primary, primaryIn(session.StateCreated), tc.recheck)
			f := newRowTokenFixture(t, inner)
			tok := f.seedRow(t, inner.Store, session.StateCreated)

			f.wantRefusal(t, f.post(t, "upload", tok), tc.wantStatus, tc.wantCode)

			deleted, total := countSessionBlobs(t, f.blobs, rowTokenTenant, rowTokenSession)
			if total != 1 || deleted != 1 {
				t.Errorf("blobs: total %d, soft-deleted %d; want the staged blob soft-deleted", total, deleted)
			}
			if used, _ := f.counter.Used(context.Background(), rowTokenTenant); used != 0 {
				t.Errorf("reserved bytes after the refusal = %d, want 0", used)
			}
		})
	}
}

// A client that disconnects after its body is consumed fails the recheck read
// with context.Canceled, and that cancellation must not reach the release. The
// counter honors its context as the Redis counter does, so a release on the
// request context leaves the bytes held against the tenant.
//
// spec: §7.4 (Upload Safety), §11.2 (Budgets and Quotas)
func TestPostPutRecheckReleasesAfterRequestCancellation_spec_7_4(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := &laggingStore{Store: memstore.New()}
	inner.primary = append(inner.primary, primaryIn(session.StateCreated),
		func(sessionstore.Session) (sessionstore.Session, error) {
			cancel()
			return sessionstore.Session{}, context.Canceled
		})
	f := newRowTokenFixture(t, inner)
	tok := f.seedRow(t, inner.Store, session.StateCreated)

	f.wantRefusal(t, f.postContext(t, ctx, "upload", tok), http.StatusInternalServerError, "INTERNAL_ERROR")

	deleted, total := countSessionBlobs(t, f.blobs, rowTokenTenant, rowTokenSession)
	if total != 1 || deleted != 1 {
		t.Errorf("blobs: total %d, soft-deleted %d; want the staged blob soft-deleted", total, deleted)
	}
	if used, _ := f.counter.Used(context.Background(), rowTokenTenant); used != 0 {
		t.Errorf("reserved bytes after the cancelled refusal = %d, want 0", used)
	}
}

// The recheck passes on a `created` primary row, and the upload commits.
//
// spec: §7.4 (Upload Safety)
func TestPostPutRecheckAdmitsCreatedRow_spec_7_4(t *testing.T) {
	inner := &laggingStore{Store: memstore.New()}
	inner.primary = append(inner.primary, primaryIn(session.StateCreated))
	f := newRowTokenFixture(t, inner)
	tok := f.seedRow(t, inner.Store, session.StateCreated)
	if rr := f.post(t, "upload", tok); rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if n := f.store.primaryReads.Load(); n != 2 {
		t.Errorf("primary reads = %d, want the admission read and the recheck", n)
	}
	if n := f.liveBlobs(t); n != 1 {
		t.Errorf("live blobs = %d, want 1", n)
	}
}
