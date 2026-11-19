// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// seamTestRow is the session the upload seam tests seed: owned by bob in
// acme, delegated from a parent under a root, with a pool and events.
func seamTestRow() sessionstore.Session {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	return sessionstore.Session{
		ID: "sess-seam", TenantID: "acme", UserID: "bob@acme.com", State: session.StateRunning,
		RuntimeRef: "rt", PoolRef: "pool-a", ParentSessionID: "sess-parent", RootSessionID: "sess-root",
		LastSeq: 7, CreatedAt: now, UpdatedAt: now,
	}
}

func seedSeamSession(t *testing.T) sessionstore.Store {
	t.Helper()
	store := memstore.New()
	if err := store.Create(context.Background(), seamTestRow()); err != nil {
		t.Fatal(err)
	}
	return store
}

// TestLookupSessionCarriesTheSessionRecordAndReportsAMissingSession checks
// that the core returns the session record fields a seam reads, and a
// missing session as ErrSessionNotFound.
//
// spec: GWSEAM-LookupSession
func TestLookupSessionCarriesTheSessionRecordAndReportsAMissingSession(t *testing.T) {
	core := sessionserver.New(seedSeamSession(t), sessionserver.Options{}).UploadSeam()
	got, err := core.LookupSession(context.Background(), "acme", "sess-seam")
	if err != nil {
		t.Fatal(err)
	}
	want := seam.Session{
		ID: "sess-seam", TenantID: "acme", UserID: "bob@acme.com", ParentSessionID: "sess-parent",
		RootSessionID: "sess-root", RuntimeRef: "rt", Pool: "pool-a", State: "running", LastSeq: 7,
		CreatedAt: seamTestRow().CreatedAt, UpdatedAt: seamTestRow().UpdatedAt,
	}
	if got != want {
		t.Errorf("record = %+v, want %+v", got, want)
	}
	if _, err := core.LookupSession(context.Background(), "acme", "sess-none"); !errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("missing session: %v, want ErrSessionNotFound", err)
	}
	if _, err := core.LookupSession(context.Background(), "globex", "sess-seam"); !errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("another tenant's session: %v, want ErrSessionNotFound", err)
	}
}

// TestAuthorizeSessionAppliesThePermissionGateAndTheOwnershipRule checks the
// order and the outcomes of the authorization operation: the permission
// gate refuses with ErrForbidden, a caller with no subject is
// ErrUnauthenticated, and a session the caller may not use answers as a
// missing one.
//
// spec: GWSEAM-AuthorizeSession; §10.2 (Session ownership) rules 3, 5, 6,
// and 7.
func TestAuthorizeSessionAppliesThePermissionGateAndTheOwnershipRule(t *testing.T) {
	core := sessionserver.New(seedSeamSession(t), sessionserver.Options{MultiTenant: true}).UploadSeam()
	as := func(subject string, roles ...auth.Role) context.Context {
		return authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: subject, TenantID: "acme", Roles: roles})
	}
	cases := []struct {
		name    string
		ctx     context.Context
		id      string
		action  seam.SessionAction
		wantErr error
	}{
		{"no principal", context.Background(), "sess-seam", seam.SessionRead, seam.ErrUnauthenticated},
		{"principal without a subject", as("", auth.RoleUser), "sess-seam", seam.SessionRead, seam.ErrUnauthenticated},
		{"role without the manage permission", as("bob@acme.com", auth.RoleTenantViewer), "sess-seam", seam.SessionManage, seam.ErrForbidden},
		{"unknown action", as("bob@acme.com", auth.RoleUser), "sess-seam", seam.SessionAction(99), seam.ErrForbidden},
		{"missing session", as("bob@acme.com", auth.RoleUser), "sess-none", seam.SessionRead, seam.ErrSessionNotFound},
		{"another user's session", as("alice@acme.com", auth.RoleUser), "sess-seam", seam.SessionManage, seam.ErrSessionNotFound},
		{"tenant reader manages", as("carol@acme.com", auth.RoleTenantViewer, auth.RoleUser), "sess-seam", seam.SessionManage, seam.ErrSessionNotFound},
		{"owner's subject in another tenant", authmw.WithPrincipal(context.Background(), authmw.Principal{Subject: "bob@acme.com", TenantID: "globex", Roles: []auth.Role{auth.RoleUser}}), "sess-seam", seam.SessionManage, seam.ErrSessionNotFound},
		{"owner manages", as("bob@acme.com", auth.RoleUser), "sess-seam", seam.SessionManage, nil},
		{"tenant reader reads", as("carol@acme.com", auth.RoleTenantViewer), "sess-seam", seam.SessionRead, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := core.AuthorizeSession(tc.ctx, "acme", tc.id, tc.action)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || got.ID != "sess-seam" {
				t.Fatalf("got (%+v, %v), want the session record", got, err)
			}
		})
	}
}

// TestVerifyUploadTokenMapsEachRefusalToItsSeamError checks that each
// verifier refusal matches its seam error and keeps the verifier's
// message, and that a server with no verifier admits every token.
//
// spec: GWSEAM-VerifyUploadToken
func TestVerifyUploadTokenMapsEachRefusalToItsSeamError(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	current := now
	clock := func() time.Time { return current }
	ring := uploadtoken.NewKeyRing(uploadtoken.SigningKey{KeyID: "k", Secret: []byte("s")})
	issuer := uploadtoken.NewIssuer(ring, clock)
	verifier := uploadtoken.NewVerifier(ring, uploadtoken.NewMemoryTracker(), clock)
	core := sessionserver.New(memstore.New(), sessionserver.Options{UploadTokenVerifier: verifier}).UploadSeam()
	ctx := context.Background()

	tok, parsed, err := issuer.IssueDetailed("sess-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := core.VerifyUploadToken(ctx, "sess-a", tok); err != nil {
		t.Fatalf("a valid token: %v", err)
	}
	err = core.VerifyUploadToken(ctx, "sess-a", "")
	if !errors.Is(err, seam.ErrUploadTokenInvalid) || err.Error() != uploadtoken.ErrInvalid.Error() {
		t.Errorf("no token: %v, want ErrUploadTokenInvalid with the verifier's message", err)
	}
	if err := core.VerifyUploadToken(ctx, "sess-a", "garbage"); !errors.Is(err, seam.ErrUploadTokenInvalid) {
		t.Errorf("malformed token: %v, want ErrUploadTokenInvalid", err)
	}
	if err := core.VerifyUploadToken(ctx, "sess-b", tok); !errors.Is(err, seam.ErrUploadTokenMismatch) {
		t.Errorf("another session's token: %v, want ErrUploadTokenMismatch", err)
	}
	if err := verifier.ConsumeDigest(parsed.Digest, parsed.Expiry); err != nil {
		t.Fatal(err)
	}
	if err := core.VerifyUploadToken(ctx, "sess-a", tok); !errors.Is(err, seam.ErrUploadTokenConsumed) {
		t.Errorf("consumed token: %v, want ErrUploadTokenConsumed", err)
	}
	fresh, _ := issuer.Issue("sess-a", time.Minute)
	current = now.Add(2 * time.Minute)
	if err := core.VerifyUploadToken(ctx, "sess-a", fresh); !errors.Is(err, seam.ErrUploadTokenExpired) {
		t.Errorf("expired token: %v, want ErrUploadTokenExpired", err)
	}

	open := sessionserver.New(memstore.New(), sessionserver.Options{}).UploadSeam()
	if err := open.VerifyUploadToken(ctx, "sess-a", ""); err != nil {
		t.Errorf("no verifier: %v, want nil", err)
	}
}

// unreadableCounter is a storage quota counter in the dual-store outage.
type unreadableCounter struct{ storagequota.Counter }

func (unreadableCounter) Reserve(context.Context, string, int64, int64) (int64, error) {
	return 0, storagequota.ErrUnavailable
}

// TestReserveStorageHoldsAnUploadAgainstItsTenantQuota checks each outcome
// of the reservation: no quota wired, no tenant row, and no limit hold
// nothing; a limited tenant needs a declared size; a reservation past the
// limit carries the usage and the limit; an unreadable counter fails
// closed; and a held reservation carries the headroom.
//
// spec: GWSEAM-ReserveStorage, GWSEAM-AdjustStorage; §11.2; §12.4.
func TestReserveStorageHoldsAnUploadAgainstItsTenantQuota(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	for _, tn := range []tenantstore.Tenant{{ID: "acme", StorageQuotaBytes: 100}, {ID: "globex"}} {
		if err := tenants.Create(ctx, tn); err != nil {
			t.Fatal(err)
		}
	}
	counter := storagequota.NewMemory()
	core := sessionserver.New(memstore.New(), sessionserver.Options{Tenants: tenants, StorageQuota: counter}).UploadSeam()

	none := sessionserver.New(memstore.New(), sessionserver.Options{}).UploadSeam()
	if res, err := none.ReserveStorage(ctx, "acme", 10); err != nil || res.Held {
		t.Errorf("no quota wired: (%+v, %v), want no reservation", res, err)
	}
	if err := none.AdjustStorage(ctx, "acme", 10); err != nil {
		t.Errorf("adjust with no quota wired: %v", err)
	}
	if res, err := core.ReserveStorage(ctx, "initech", 10); err != nil || res.Held {
		t.Errorf("no tenant row: (%+v, %v), want no reservation", res, err)
	}
	if res, err := core.ReserveStorage(ctx, "globex", 10); err != nil || res.Held {
		t.Errorf("no limit: (%+v, %v), want no reservation", res, err)
	}
	if _, err := core.ReserveStorage(ctx, "acme", -1); !errors.Is(err, seam.ErrContentLengthRequired) {
		t.Errorf("undeclared size: %v, want ErrContentLengthRequired", err)
	}
	res, err := core.ReserveStorage(ctx, "acme", 60)
	if err != nil || res != (seam.StorageReservation{Held: true, Reserved: 60, Headroom: 100}) {
		t.Fatalf("first reservation: (%+v, %v)", res, err)
	}
	var qe *seam.StorageQuotaError
	if _, err := core.ReserveStorage(ctx, "acme", 50); !errors.As(err, &qe) || qe.CurrentBytes != 60 || qe.LimitBytes != 100 {
		t.Errorf("reservation past the limit: %v, want a quota error at 60 of 100", err)
	}
	if err := core.AdjustStorage(ctx, "acme", -60); err != nil {
		t.Fatal(err)
	}
	if used, _ := counter.Used(ctx, "acme"); used != 0 {
		t.Errorf("usage after the release = %d, want 0", used)
	}

	down := sessionserver.New(memstore.New(), sessionserver.Options{Tenants: tenants, StorageQuota: unreadableCounter{}}).UploadSeam()
	if _, err := down.ReserveStorage(ctx, "acme", 10); !errors.Is(err, seam.ErrStorageQuotaUnavailable) {
		t.Errorf("unreadable counter: %v, want ErrStorageQuotaUnavailable", err)
	}
	broken := sessionserver.New(memstore.New(), sessionserver.Options{Tenants: failingTenants{}, StorageQuota: counter}).UploadSeam()
	if _, err := broken.ReserveStorage(ctx, "acme", 10); err == nil || !strings.HasPrefix(err.Error(), "storage-quota check failed: ") {
		t.Errorf("tenant read failure: %v, want the storage-quota check failure", err)
	}
}

// TestForwardRequestRefusesAKindTheCoreHasNoRowFor checks that an unknown
// forward kind answers INTERNAL_ERROR without forwarding, and that a
// mid-session upload outside a forwarded state runs on this replica.
//
// spec: GWSEAM-ForwardRequest
func TestForwardRequestRefusesAKindTheCoreHasNoRowFor(t *testing.T) {
	core := sessionserver.New(memstore.New(), sessionserver.Options{}).UploadSeam()
	s := seam.Session{ID: "sess-a", TenantID: "acme", State: "running"}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-a/upload-to-session", nil)
	if got := core.ForwardRequest(rr, req, s, seam.ForwardKind("teleport"), req.Body); got != seam.ForwardNotDelivered {
		t.Errorf("unknown kind outcome = %v, want ForwardNotDelivered", got)
	}
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "INTERNAL_ERROR") {
		t.Errorf("unknown kind answer = %d %s, want INTERNAL_ERROR", rr.Code, rr.Body.String())
	}

	s.State = "created"
	rr = httptest.NewRecorder()
	if got := core.ForwardRequest(rr, req, s, seam.ForwardUpload, req.Body); got != seam.ForwardLocal {
		t.Errorf("upload in created outcome = %v, want ForwardLocal", got)
	}
}

// TestWriteWorkspaceNeedsALiveBinding checks that a workspace write to a
// session with no adapter binding on this replica is ErrNoLiveBinding,
// with no pod registry and with a registry that holds no binding.
//
// spec: GWSEAM-WriteWorkspace
func TestWriteWorkspaceNeedsALiveBinding(t *testing.T) {
	files := []seam.WorkspaceFile{{Path: "a.txt", Content: []byte("x")}}
	for name, opts := range map[string]sessionserver.Options{
		"no registry":       {},
		"no binding":        {PodRegistry: podsession.NewRegistry()},
		"binding no client": {PodRegistry: registryWith(&podsession.BindResult{SessionID: "sess-a"})},
	} {
		t.Run(name, func(t *testing.T) {
			core := sessionserver.New(memstore.New(), opts).UploadSeam()
			if _, err := core.WriteWorkspace(context.Background(), "sess-a", files); !errors.Is(err, seam.ErrNoLiveBinding) {
				t.Fatalf("err = %v, want ErrNoLiveBinding", err)
			}
		})
	}
}

func registryWith(b *podsession.BindResult) *podsession.Registry {
	reg := podsession.NewRegistry()
	reg.Put(b)
	return reg
}

// TestAuditUploadWritesTheSessionUploadEventUnderTheSessionsTenant checks
// that the core writes the upload audit event with the session record's
// scope, the caller's outcome, reason, and detail, and its own clock, and
// that it records nothing without a sink.
//
// spec: GWSEAM-AuditUpload
func TestAuditUploadWritesTheSessionUploadEventUnderTheSessionsTenant(t *testing.T) {
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	sink := &lifecycleSink{}
	core := sessionserver.New(memstore.New(), sessionserver.Options{
		LifecycleAuditSink: sink, Clock: func() time.Time { return at },
	}).UploadSeam()
	core.AuditUpload(context.Background(), seam.UploadAuditEvent{
		Session: seam.Session{ID: "sess-a", TenantID: "acme", UserID: "bob@acme.com", RuntimeRef: "rt", State: "created"},
		Outcome: seam.UploadRejected, Reason: "size_limit", Detail: "d",
	})
	evs := sink.eventsOf("session.upload")
	if len(evs) != 1 {
		t.Fatalf("session.upload events = %d, want 1", len(evs))
	}
	ev := evs[0]
	if ev.TenantID != "acme" || ev.SessionID != "sess-a" || ev.UserID != "bob@acme.com" || ev.RuntimeRef != "rt" ||
		ev.State != "created" || ev.Outcome != "rejected" || ev.Reason != "size_limit" || ev.Detail != "d" || !ev.At.Equal(at) {
		t.Errorf("event = %+v", ev)
	}

	quiet := sessionserver.New(memstore.New(), sessionserver.Options{}).UploadSeam()
	quiet.AuditUpload(context.Background(), seam.UploadAuditEvent{Session: seam.Session{ID: "sess-a"}})
}
