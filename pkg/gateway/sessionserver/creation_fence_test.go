// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// refusalStore wraps a session store so a test can inject the error each
// insert or reservation method returns, or run a hook before it delegates.
// It records which insert method each create reached.
type refusalStore struct {
	sessionstore.Store

	mu                 sync.Mutex
	createErr          error
	createReservedErr  error
	createWithTransErr error
	reserveErr         error
	beforeInsert       func()
	calls              []string
	transcriptSources  []string
}

func (f *refusalStore) record(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	if f.beforeInsert != nil && name != "Reserve" {
		f.beforeInsert()
	}
}

func (f *refusalStore) callsOf(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

func (f *refusalStore) Create(ctx context.Context, s sessionstore.Session) error {
	f.record("Create")
	if f.createErr != nil {
		return f.createErr
	}
	return f.Store.Create(ctx, s)
}

func (f *refusalStore) CreateReserved(ctx context.Context, s sessionstore.Session) error {
	f.record("CreateReserved")
	if f.createReservedErr != nil {
		return f.createReservedErr
	}
	return f.Store.CreateReserved(ctx, s)
}

func (f *refusalStore) CreateWithTranscript(ctx context.Context, s sessionstore.Session, src string) error {
	f.record("CreateWithTranscript")
	f.mu.Lock()
	f.transcriptSources = append(f.transcriptSources, src)
	f.mu.Unlock()
	if f.createWithTransErr != nil {
		return f.createWithTransErr
	}
	return f.Store.CreateWithTranscript(ctx, s, src)
}

func (f *refusalStore) Reserve(ctx context.Context, tenantID, owner, sessionID string, child bool) error {
	f.record("Reserve")
	if f.reserveErr != nil {
		return f.reserveErr
	}
	return f.Store.Reserve(ctx, tenantID, owner, sessionID, child)
}

// fenceFixture holds the stores a fence test reads back.
type fenceFixture struct {
	tenants *tenantstore.Memory
	users   *userstore.Memory
	mem     *memstore.Store
	store   *refusalStore
	blobs   *blobstore.MemoryStore
}

func newFenceFixture(t *testing.T, users ...userstore.User) *fenceFixture {
	t.Helper()
	f := &fenceFixture{
		tenants: tenantstore.NewMemory(),
		users:   userstore.NewMemory(),
		blobs:   blobstore.NewMemoryStore(nil),
	}
	if err := f.tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	for _, u := range users {
		if err := f.users.Create(context.Background(), u); err != nil {
			t.Fatalf("seed user %s: %v", u.Subject, err)
		}
	}
	f.mem = memstore.New(memstore.WithCreationMarkers(sessionstore.StoreMarkers(f.tenants, f.users)))
	f.store = &refusalStore{Store: f.mem}
	return f
}

func (f *fenceFixture) restrict(t *testing.T, subject, jobID string) {
	t.Helper()
	if _, err := f.users.Update(context.Background(), "acme", subject, func(u *userstore.User) error {
		u.ProcessingRestricted, u.ErasureJobID = true, jobID
		return nil
	}); err != nil {
		t.Fatalf("restrict %s: %v", subject, err)
	}
}

func (f *fenceFixture) reserved(t *testing.T, subject string) []string {
	t.Helper()
	ids, err := f.mem.ReservedSessionIDs(context.Background(), "acme", subject)
	if err != nil {
		t.Fatalf("ReservedSessionIDs: %v", err)
	}
	return ids
}

// postCreate posts a create to path as principal, naming owner as the
// body's userId.
func postCreate(t *testing.T, h http.Handler, path, principal, owner string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"runtimeRef": "claude-code", "userId": owner})
	req := asUser(httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)), principal, "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// countingRateCounter records every §11.1 admission-rate increment.
type countingRateCounter struct {
	mu sync.Mutex
	n  int
}

func (c *countingRateCounter) Incr(context.Context, string, time.Time) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return 1, nil
}

func (c *countingRateCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestCreateStoreRefusalAnswers403_spec_12_8 pins the refusal mapping at
// the store.Create branch of the two-step create and the combined
// create-and-start path: a *CreationRefusal from the insert answers 403
// with its code and details rather than the retryable 503 the branch
// answered before.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API: Error code catalog)
func TestCreateStoreRefusalAnswers403_spec_12_8(t *testing.T) {
	for _, path := range []string{"/v1/sessions", "/v1/sessions/start"} {
		t.Run(path, func(t *testing.T) {
			f := newFenceFixture(t)
			f.store.createErr = &sessionstore.CreationRefusal{
				Code: "TENANT_SUSPENDED", Message: "suspended", Details: map[string]any{"tenantId": "acme"},
			}
			h := sessionserver.New(f.store, sessionserver.Options{}).Handler()
			rr := postCreate(t, h, path, "alice@acme.com", "alice@acme.com")
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
			}
			code, _, details := decodeError(t, rr)
			if code != "TENANT_SUSPENDED" || details["tenantId"] != "acme" {
				t.Errorf("code %q details %v, want TENANT_SUSPENDED with tenantId", code, details)
			}
			if f.store.callsOf("Create") != 1 {
				t.Errorf("the create did not reach the store insert")
			}
		})
	}
}

// TestCreateStoreNonRefusalErrorStays5xx_spec_12_8 keeps a store error
// that is not a fence refusal on the retryable path.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestCreateStoreNonRefusalErrorStays5xx_spec_12_8(t *testing.T) {
	f := newFenceFixture(t)
	f.store.createErr = errors.New("connection reset")
	h := sessionserver.New(f.store, sessionserver.Options{}).Handler()
	rr := postCreate(t, h, "/v1/sessions", "alice@acme.com", "alice@acme.com")
	if rr.Code < 500 {
		t.Fatalf("status = %d, want 5xx", rr.Code)
	}
}

// TestCreationGateMatchesFencePredicate_spec_12_8 runs every combination
// of tenant markers and owner markers through the early creation gate on
// both create paths and asserts the code CreationMarkers.Refusal answers
// for the same state, that a refused create reaches no insert, and that
// it moves no admission-rate counter.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation)
func TestCreationGateMatchesFencePredicate_spec_12_8(t *testing.T) {
	type tenantCase struct {
		name      string
		suspended bool
		state     string
	}
	type ownerCase struct {
		name       string
		disabled   bool
		deleted    bool
		restricted bool
	}
	tenants := []tenantCase{
		{"active", false, tenantstore.TenantStateActive},
		{"suspended", true, tenantstore.TenantStateActive},
		{"disabling", false, tenantstore.TenantStateDisabling},
		{"suspended-deleting", true, tenantstore.TenantStateDeleting},
	}
	owners := []ownerCase{
		{"active", false, false, false},
		{"soft-disabled", true, false, false},
		{"revoked", false, true, false},
		{"restricted", false, false, true},
		{"disabled-restricted", true, false, true},
	}
	for _, path := range []string{"/v1/sessions", "/v1/sessions/start"} {
		for _, tc := range tenants {
			for _, oc := range owners {
				t.Run(path+"/"+tc.name+"/"+oc.name, func(t *testing.T) {
					f := newFenceFixture(t,
						userstore.User{Subject: "alice@acme.com", TenantID: "acme"},
						userstore.User{
							Subject: "bob@acme.com", TenantID: "acme",
							Disabled: oc.disabled, ProcessingRestricted: oc.restricted, ErasureJobID: "job-1",
						})
					if oc.deleted {
						if err := f.users.SoftDelete(context.Background(), "acme", "bob@acme.com", time.Now()); err != nil {
							t.Fatalf("soft-delete: %v", err)
						}
					}
					if _, err := f.tenants.Update(context.Background(), "acme", func(tn *tenantstore.Tenant) error {
						tn.Suspended, tn.State = tc.suspended, tc.state
						return nil
					}); err != nil {
						t.Fatalf("tenant update: %v", err)
					}
					want, err := sessionstore.StoreMarkers(f.tenants, f.users)(context.Background(), "acme", "bob@acme.com")
					if err != nil {
						t.Fatalf("markers: %v", err)
					}
					ref := want.Refusal("acme", "bob@acme.com")
					rc := &countingRateCounter{}
					h := sessionserver.New(f.store, sessionserver.Options{
						Tenants: f.tenants, Users: f.users,
						AdmissionRateLimitCounter: rc, PerRuntimePerMinute: 1000,
					}).Handler()
					rr := postCreate(t, h, path, "alice@acme.com", "bob@acme.com")
					if ref == nil {
						if rr.Code >= 400 && rr.Code != http.StatusServiceUnavailable {
							code, _, _ := decodeError(t, rr)
							if code == "TENANT_SUSPENDED" || code == "TENANT_NOT_ACTIVE" ||
								code == "USER_INVALIDATED" || code == "ERASURE_IN_PROGRESS" {
								t.Fatalf("admitted state refused with %s", code)
							}
						}
						return
					}
					if rr.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
					}
					code, _, details := decodeError(t, rr)
					if code != ref.Code {
						t.Fatalf("code = %s, want %s", code, ref.Code)
					}
					if ref.Code == "ERASURE_IN_PROGRESS" && details["userId"] != "bob@acme.com" {
						t.Errorf("ERASURE_IN_PROGRESS names %v, want the owner", details["userId"])
					}
					if n := f.store.callsOf("Create"); n != 0 {
						t.Errorf("a refused create reached the insert %d times", n)
					}
					if n := rc.count(); n != 0 {
						t.Errorf("a refused create moved the admission-rate counter %d times", n)
					}
				})
			}
		}
	}
}

// TestCreationGateReadErrorAnswers500_spec_12_8 pins the Gates column
// rule: a marker read error answers 500 INTERNAL_ERROR before any insert
// and before the rate counter moves.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestCreationGateReadErrorAnswers500_spec_12_8(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tenants tenantstore.Store
		users   userstore.Store
	}{
		{"tenant", erroringTenants{tenantstore.NewMemory()}, userstore.NewMemory()},
		{"user", tenantstore.NewMemory(), erroringUsers{userstore.NewMemory()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFenceFixture(t)
			rc := &countingRateCounter{}
			h := sessionserver.New(f.store, sessionserver.Options{
				Tenants: tc.tenants, Users: tc.users,
				AdmissionRateLimitCounter: rc, PerRuntimePerMinute: 1000,
			}).Handler()
			// The principal has no subject, so the caller check reads
			// nothing and the creation gate is the first reader.
			body, _ := json.Marshal(map[string]string{"runtimeRef": "claude-code", "userId": "bob@acme.com"})
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
			req.Header.Set("X-Lenny-Tenant-ID", "acme")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body %s", rr.Code, rr.Body.String())
			}
			if code, _, _ := decodeError(t, rr); code != "INTERNAL_ERROR" {
				t.Errorf("code = %s, want INTERNAL_ERROR", code)
			}
			if f.store.callsOf("Create") != 0 || rc.count() != 0 {
				t.Errorf("the read error did not stop the create before its side effects")
			}
		})
	}
}

type erroringTenants struct{ tenantstore.Store }

func (erroringTenants) Get(context.Context, string) (tenantstore.Tenant, error) {
	return tenantstore.Tenant{}, errors.New("tenant registry unavailable")
}

type erroringUsers struct{ userstore.Store }

func (erroringUsers) Get(context.Context, string, string) (userstore.User, error) {
	return userstore.User{}, errors.New("user registry unavailable")
}

// deriveFixture seeds a terminal source session owned by alice whose
// legacy snapshot object exists, so a derive copies an object under the
// derived ID.
func deriveFixture(t *testing.T, users ...userstore.User) *fenceFixture {
	t.Helper()
	f := newFenceFixture(t, users...)
	src := blobstore.URI{
		TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID: "sess_source", PartID: "snap", TTL: time.Hour,
	}
	if _, err := f.blobs.Put(src, "application/x-tar", strings.NewReader("workspace-bytes")); err != nil {
		t.Fatalf("seed parent blob: %v", err)
	}
	newSourceSession(t, f.mem, func(s *sessionstore.Session) { s.WorkspaceSnapshot.Ref = src.String() })
	return f
}

func derivedObject() blobstore.URI {
	return blobstore.URI{
		TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace,
		SessionID: "sess_derived", PartID: "derived-from-sess_source", TTL: time.Hour,
	}
}

func (f *fenceFixture) deriveServer(blobs blobstore.Store, opts ...func(*sessionserver.Options)) http.Handler {
	o := sessionserver.Options{
		IDFunc: func() string { return "sess_derived" },
		Blobs:  blobs, Users: f.users, Tenants: f.tenants,
	}
	for _, m := range opts {
		m(&o)
	}
	return sessionserver.New(f.store, o).Handler()
}

func asPrincipal(subject string) func(*http.Request) {
	return func(r *http.Request) {
		*r = *r.WithContext(authmw.WithPrincipal(r.Context(), authmw.Principal{Subject: subject, TenantID: "acme"}))
	}
}

// TestDeriveRefusalUndoesCopyAndReleasesReservation_spec_12_8 pins the
// derive's refusal mapping and undo: a fence refusal from CreateReserved
// answers 403 with its code, deletes the object copied under the derived
// ID, and releases the reservation, while a non-refusal insert error
// keeps both.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDeriveRefusalUndoesCopyAndReleasesReservation_spec_12_8(t *testing.T) {
	t.Run("refusal", func(t *testing.T) {
		f := deriveFixture(t)
		f.store.createReservedErr = sessionstore.ReservationEndedRefusal("alice@acme.com")
		rr := deriveRequest(t, f.deriveServer(f.blobs), sessionserver.DeriveRequest{})
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
		}
		if code, _, _ := decodeError(t, rr); code != "ERASURE_IN_PROGRESS" {
			t.Errorf("code = %s, want ERASURE_IN_PROGRESS", code)
		}
		if _, _, err := f.blobs.Get(derivedObject()); !errors.Is(err, blobstore.ErrNotFound) {
			t.Errorf("the copied object survived the refusal: %v", err)
		}
		if ids := f.reserved(t, "alice@acme.com"); len(ids) != 0 {
			t.Errorf("reservation kept after a successful undo: %v", ids)
		}
	})
	t.Run("non-refusal error keeps the objects and the reservation", func(t *testing.T) {
		f := deriveFixture(t)
		f.store.createReservedErr = errors.New("commit outcome unknown")
		rr := deriveRequest(t, f.deriveServer(f.blobs), sessionserver.DeriveRequest{})
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
		if _, _, err := f.blobs.Get(derivedObject()); err != nil {
			t.Errorf("a non-refusal error deleted the copied object: %v", err)
		}
		if ids := f.reserved(t, "alice@acme.com"); len(ids) != 1 {
			t.Errorf("reservation = %v, want the derived ID kept", ids)
		}
	})
	t.Run("blob store without per-session delete keeps the reservation", func(t *testing.T) {
		f := deriveFixture(t)
		f.store.createReservedErr = sessionstore.ReservationEndedRefusal("alice@acme.com")
		rr := deriveRequest(t, f.deriveServer(newCopyOnlyBlobs(f.blobs)), sessionserver.DeriveRequest{})
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rr.Code)
		}
		if ids := f.reserved(t, "alice@acme.com"); len(ids) != 1 {
			t.Errorf("reservation = %v, want it kept when the objects cannot be deleted", ids)
		}
	})
	t.Run("deletion error keeps the reservation", func(t *testing.T) {
		f := deriveFixture(t)
		f.store.createReservedErr = sessionstore.ReservationEndedRefusal("alice@acme.com")
		rr := deriveRequest(t, f.deriveServer(failingDeleteBlobs{f.blobs}), sessionserver.DeriveRequest{})
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rr.Code)
		}
		if ids := f.reserved(t, "alice@acme.com"); len(ids) != 1 {
			t.Errorf("reservation = %v, want it kept after a failed deletion", ids)
		}
	})
}

// copyOnlyBlobs is a blob store that copies but cannot delete by session:
// embedding the interface hides the memory store's DeleteBySession.
type copyOnlyBlobs struct {
	blobstore.Store
	copier blobstore.Copier
}

func newCopyOnlyBlobs(m *blobstore.MemoryStore) copyOnlyBlobs {
	return copyOnlyBlobs{Store: m, copier: m}
}

func (c copyOnlyBlobs) Copy(src, dst blobstore.URI) error { return c.copier.Copy(src, dst) }

// recordingDeleteBlobs records each per-session deletion and performs it.
type recordingDeleteBlobs struct {
	*blobstore.MemoryStore
	deleted []string
}

func (r *recordingDeleteBlobs) DeleteBySession(ctx context.Context, tenantID, sessionID string) (int, error) {
	r.deleted = append(r.deleted, sessionID)
	return r.MemoryStore.DeleteBySession(ctx, tenantID, sessionID)
}

// failingDeleteBlobs copies and fails every per-session deletion.
type failingDeleteBlobs struct{ *blobstore.MemoryStore }

func (failingDeleteBlobs) DeleteBySession(context.Context, string, string) (int, error) {
	return 0, errors.New("object store unavailable")
}

// TestDeriveReserveRefusalCopiesNothing_spec_12_8 pins the reservation
// gate of a derive: a Reserve refusal answers 403 with the refusal's code
// and details, and any other Reserve error answers 500, each before any
// object is copied.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDeriveReserveRefusalCopiesNothing_spec_12_8(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"refusal", &sessionstore.CreationRefusal{Code: "TENANT_NOT_ACTIVE", Message: "m", Details: map[string]any{"tenantId": "acme", "state": "deleting"}}, http.StatusForbidden, "TENANT_NOT_ACTIVE"},
		{"error", errors.New("reservation insert failed"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := deriveFixture(t)
			f.store.reserveErr = tc.err
			rr := deriveRequest(t, f.deriveServer(f.blobs), sessionserver.DeriveRequest{})
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d", rr.Code, tc.status)
			}
			code, _, details := decodeError(t, rr)
			if code != tc.code {
				t.Errorf("code = %s, want %s", code, tc.code)
			}
			if tc.code == "TENANT_NOT_ACTIVE" && details["state"] != "deleting" {
				t.Errorf("details = %v, want the refusal's details", details)
			}
			if _, _, err := f.blobs.Get(derivedObject()); !errors.Is(err, blobstore.ErrNotFound) {
				t.Errorf("an object was copied after the reservation failed")
			}
			if f.store.callsOf("CreateReserved") != 0 {
				t.Errorf("the derive inserted after its reservation failed")
			}
		})
	}
}

// chunkedDerive wires a parent chunked checkpoint whose single chunk
// exists, and a manifest writer whose Finalise fails, so the derive copies
// the chunk and writes the derived manifest row before it fails.
type failingFinalise struct {
	*partialmanifeststore.MemoryStore
}

func (failingFinalise) Finalise(context.Context, string, string, bool, string) error {
	return errors.New("manifest finalise failed")
}

type parentManifest struct{ rec partialmanifeststore.Record }

func (p parentManifest) Get(context.Context, string, string) (partialmanifeststore.Record, error) {
	return p.rec, nil
}

func (p parentManifest) LatestActiveAny(context.Context, string, string) (partialmanifeststore.Record, error) {
	return p.rec, nil
}

func (p parentManifest) LatestFull(context.Context, string, string) (partialmanifeststore.Record, error) {
	return p.rec, nil
}

// TestDeriveCopyFailureUndoesChunksAndManifest_spec_12_8 pins the undo in
// the derive's copy-failure branch: the chunk copied under the derived ID
// and the derived manifest row are deleted and the reservation released
// before the failure answers, including when the request context is
// cancelled, and the derive-failure audit row is then written under the
// fence.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 16.1 (Metrics)
func TestDeriveCopyFailureUndoesChunksAndManifest_spec_12_8(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "live context"
		if cancelled {
			name = "cancelled context"
		}
		t.Run(name, func(t *testing.T) {
			f := newFenceFixture(t)
			enc := partialmanifeststore.ChunkEncodingTar
			srcChunk := resumechunks.ChunkObjectURI("acme", "sess_source", "ckpt_parent", 0, enc)
			srcChunk.TTL = time.Hour
			rec := &recordingDeleteBlobs{MemoryStore: f.blobs}
			if _, err := f.blobs.Put(srcChunk, "application/x-tar", strings.NewReader("chunk-0")); err != nil {
				t.Fatalf("seed chunk: %v", err)
			}
			newSourceSession(t, f.mem, func(s *sessionstore.Session) {
				s.WorkspaceSnapshot.Ref = "/acme/checkpoints/sess_source/ckpt_parent"
			})
			manifests := partialmanifeststore.NewMemoryStore(nil)
			ids := []string{"sess_derived", "ckpt_derived"}
			var outcomes []string
			h := sessionserver.New(f.store, sessionserver.Options{
				IDFunc: func() string { id := ids[0]; ids = append(ids[1:], "x"); return id },
				Blobs:  rec, Users: f.users, Tenants: f.tenants,
				CheckpointManifestReader: parentManifest{partialmanifeststore.Record{
					TenantID: "acme", CheckpointID: "ckpt_parent", SessionID: "sess_source", ChunkCount: 1, ChunkEncoding: enc,
				}},
				CheckpointManifestWriter: failingFinalise{manifests},
				PersistDeriveFailureRows: true,
				IncDeriveFailureAudit:    func(o string) { outcomes = append(outcomes, o) },
			}).Handler()
			rr := deriveRequest(t, h, sessionserver.DeriveRequest{}, func(r *http.Request) {
				if cancelled {
					ctx, cancel := context.WithCancel(r.Context())
					cancel()
					*r = *r.WithContext(ctx)
				}
			})
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body %s", rr.Code, rr.Body.String())
			}
			if len(rec.deleted) != 1 || rec.deleted[0] != "sess_derived" {
				t.Errorf("objects deleted under %v, want the derived session's", rec.deleted)
			}
			if _, err := manifests.Get(context.Background(), "acme", "ckpt_derived"); err == nil {
				t.Errorf("the derived manifest row survived the failure")
			}
			if ids := f.reserved(t, "alice@acme.com"); len(ids) != 0 {
				t.Errorf("reservation kept after the undo: %v", ids)
			}
			if len(outcomes) != 1 {
				t.Fatalf("derive-failure audit outcomes = %v", outcomes)
			}
		})
	}
}

// TestDeriveFailureRowRefusedRecordsError_spec_12_8 pins the derive-failure
// audit row under the fence: when the source owner is restricted after
// the derive was admitted, the fence refuses the terminal row, nothing is
// written, and the outcome is error.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 16.1 (Metrics)
func TestDeriveFailureRowRefusedRecordsError_spec_12_8(t *testing.T) {
	f := newFenceFixture(t, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	newSourceSession(t, f.mem, func(s *sessionstore.Session) {
		s.WorkspaceSnapshot.Ref = (blobstore.URI{
			TenantID: "acme", ObjectType: blobstore.ObjectTypeWorkspace,
			SessionID: "sess_source", PartID: "missing", TTL: time.Hour,
		}).String()
	})
	// The flag commits after the reservation admitted the derive and
	// before the failure row's insert.
	f.store.beforeInsert = func() { f.restrict(t, "alice@acme.com", "job-9") }
	var outcomes []string
	h := f.deriveServer(f.blobs, func(o *sessionserver.Options) {
		o.PersistDeriveFailureRows = true
		o.IncDeriveFailureAudit = func(v string) { outcomes = append(outcomes, v) }
	})
	rr := deriveRequest(t, h, sessionserver.DeriveRequest{})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rr.Code, rr.Body.String())
	}
	if len(outcomes) != 1 || outcomes[0] != "error" {
		t.Fatalf("outcomes = %v, want [error]", outcomes)
	}
	if _, err := f.mem.Get(context.Background(), "acme", "sess_derived"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("the refused failure row was written: %v", err)
	}
}

// TestDeriveAndReplaySourceOwnerRestricted_spec_12_8 pins the source-owner
// check of a derive and a replay: a source session whose owner has
// processing_restricted set is refused with ERASURE_IN_PROGRESS naming the
// source owner, whatever the new owner's and the tenant's markers, and
// before the reservation, the copy, and the insert. A derive into an
// active user's session is the case the fence alone would admit.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
func TestDeriveAndReplaySourceOwnerRestricted_spec_12_8(t *testing.T) {
	cases := []struct {
		name      string
		replay    bool
		newOwner  string
		disableBo bool
		suspend   bool
	}{
		{name: "derive into an active user's session", newOwner: "bob@acme.com"},
		{name: "derive with no userId"},
		{name: "derive into a disabled owner's session", newOwner: "bob@acme.com", disableBo: true},
		{name: "derive under a suspended tenant", newOwner: "bob@acme.com", suspend: true},
		{name: "replay", replay: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := deriveFixture(t,
				userstore.User{Subject: "alice@acme.com", TenantID: "acme"},
				userstore.User{Subject: "bob@acme.com", TenantID: "acme", Disabled: tc.disableBo},
				userstore.User{Subject: "carol@acme.com", TenantID: "acme"})
			f.restrict(t, "alice@acme.com", "job-7")
			if tc.suspend {
				if _, err := f.tenants.Update(context.Background(), "acme", func(tn *tenantstore.Tenant) error {
					tn.Suspended = true
					return nil
				}); err != nil {
					t.Fatalf("suspend: %v", err)
				}
			}
			var rr *httptest.ResponseRecorder
			if tc.replay {
				rr = replayAs(t, f.deriveServer(f.blobs), "carol@acme.com", sessionserver.ReplayRequest{TargetRuntime: "x"})
			} else {
				rr = deriveRequest(t, f.deriveServer(f.blobs), sessionserver.DeriveRequest{UserID: tc.newOwner}, asPrincipal("carol@acme.com"))
			}
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
			}
			code, msg, details := decodeError(t, rr)
			if code != "ERASURE_IN_PROGRESS" || details["userId"] != "alice@acme.com" || details["jobId"] != "job-7" {
				t.Fatalf("code %s details %v, want ERASURE_IN_PROGRESS naming the source owner and job", code, details)
			}
			if !strings.Contains(msg, "source session") {
				t.Errorf("message %q does not name the source session's owner", msg)
			}
			if n := len(f.store.calls); n != 0 {
				t.Errorf("the refused request reached the store %d times: %v", n, f.store.calls)
			}
		})
	}
}

// TestSourceOwnerCheckAdmitsAndFailsClosed_spec_12_8 covers the remaining
// inputs of the source-owner check: a source owner with no registry row
// admits, a read error answers 500, and a restricted caller is refused by
// the caller check, naming the caller.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18))
func TestSourceOwnerCheckAdmitsAndFailsClosed_spec_12_8(t *testing.T) {
	t.Run("no registry row admits", func(t *testing.T) {
		f := deriveFixture(t)
		rr := deriveRequest(t, f.deriveServer(f.blobs), sessionserver.DeriveRequest{})
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
		}
	})
	t.Run("read error answers 500", func(t *testing.T) {
		f := deriveFixture(t)
		h := sessionserver.New(f.store, sessionserver.Options{Users: erroringUsers{f.users}, Blobs: f.blobs}).Handler()
		rr := deriveRequest(t, h, sessionserver.DeriveRequest{})
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
		if f.store.callsOf("Reserve") != 0 {
			t.Errorf("the read error did not stop the derive before its reservation")
		}
	})
	t.Run("restricted caller replaying an active user's session", func(t *testing.T) {
		f := deriveFixture(t,
			userstore.User{Subject: "alice@acme.com", TenantID: "acme"},
			userstore.User{Subject: "dave@acme.com", TenantID: "acme", ProcessingRestricted: true, ErasureJobID: "job-3"})
		rr := replayAs(t, f.deriveServer(f.blobs), "dave@acme.com", sessionserver.ReplayRequest{TargetRuntime: "x"})
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rr.Code)
		}
		code, msg, details := decodeError(t, rr)
		if code != "ERASURE_IN_PROGRESS" || details["userId"] != "dave@acme.com" {
			t.Errorf("code %s details %v, want the caller refusal", code, details)
		}
		if !strings.Contains(msg, "cannot create sessions") {
			t.Errorf("message %q does not name session creation", msg)
		}
	})
}

// TestDeriveSourceOwnerRestrictedAfterAdmission_spec_12_8 pins the
// admission-time scope of the source-owner check: with the source owner's
// flag committed after the check admitted the derive and before the
// insert, a derive whose new owner is the source owner is refused at
// CreateReserved and undone, while a derive into an active user's session
// inserts.
// spec: 12.8 (Compliance Interfaces: Processing restriction during erasure (GDPR Article 18), Lifecycle fence and drain)
func TestDeriveSourceOwnerRestrictedAfterAdmission_spec_12_8(t *testing.T) {
	for _, tc := range []struct {
		name     string
		newOwner string
		status   int
	}{
		{"new owner is the source owner", "", http.StatusForbidden},
		{"new owner is an active user", "bob@acme.com", http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := deriveFixture(t,
				userstore.User{Subject: "alice@acme.com", TenantID: "acme"},
				userstore.User{Subject: "bob@acme.com", TenantID: "acme"})
			f.store.beforeInsert = func() { f.restrict(t, "alice@acme.com", "job-5") }
			rr := deriveRequest(t, f.deriveServer(f.blobs), sessionserver.DeriveRequest{UserID: tc.newOwner})
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.status, rr.Body.String())
			}
			if tc.status != http.StatusForbidden {
				return
			}
			if code, _, details := decodeError(t, rr); code != "ERASURE_IN_PROGRESS" || details["userId"] != "alice@acme.com" {
				t.Errorf("code %s details %v", code, details)
			}
			if _, _, err := f.blobs.Get(derivedObject()); !errors.Is(err, blobstore.ErrNotFound) {
				t.Errorf("the refused derive left its copied object")
			}
			if ids := f.reserved(t, "alice@acme.com"); len(ids) != 0 {
				t.Errorf("the refused derive left its reservation: %v", ids)
			}
		})
	}
}

func replayAs(t *testing.T, h http.Handler, subject string, body sessionserver.ReplayRequest) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_source/replay", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	asPrincipal(subject)(req)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// replayFixture seeds a terminal source session owned by alice with one
// transcript entry, over a fenced session store that copies transcripts.
func replayFixture(t *testing.T) (*fenceFixture, *transcriptstore.Memory, http.Handler) {
	t.Helper()
	f := newFenceFixture(t, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	transcripts := transcriptstore.NewMemory()
	f.mem = memstore.New(
		memstore.WithCreationMarkers(sessionstore.StoreMarkers(f.tenants, f.users)),
		memstore.WithTranscriptCopier(transcripts.CopySession),
	)
	f.store.Store = f.mem
	newSourceSession(t, f.mem)
	if err := transcripts.Append(context.Background(), "acme", "sess_source",
		transcriptstore.Entry{Role: "user", Content: "original prompt"}); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	h := sessionserver.New(f.store, sessionserver.Options{
		IDFunc:      func() string { return "sess_replay" },
		Transcripts: transcripts, Users: f.users, Tenants: f.tenants,
	}).Handler()
	return f, transcripts, h
}

// TestReplayCopiesTranscriptInInsert_spec_12_8 pins the replay insert: a
// prompt_history replay inserts through CreateWithTranscript with the
// source's ID and makes no transcript write after the insert, a
// workspace_derive replay inserts through Create, a copy error answers
// 500 with no row, and a replay whose owner's flag commits after
// admission is refused at the insert and leaves no transcript entry.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API)
func TestReplayCopiesTranscriptInInsert_spec_12_8(t *testing.T) {
	t.Run("prompt_history", func(t *testing.T) {
		f, transcripts, h := replayFixture(t)
		rr := replayAs(t, h, "", sessionserver.ReplayRequest{ReplayMode: sessionserver.ReplayPromptHistory, TargetRuntime: "x"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d; body %s", rr.Code, rr.Body.String())
		}
		if f.store.callsOf("CreateWithTranscript") != 1 || f.store.callsOf("Create") != 0 {
			t.Fatalf("insert calls = %v, want one CreateWithTranscript", f.store.calls)
		}
		if f.store.transcriptSources[0] != "sess_source" {
			t.Errorf("copy source = %s, want sess_source", f.store.transcriptSources[0])
		}
		entries, _ := transcripts.Get(context.Background(), "acme", "sess_replay")
		if len(entries) != 1 {
			t.Errorf("replayed transcript = %v, want the source's single entry", entries)
		}
	})
	t.Run("workspace_derive", func(t *testing.T) {
		f, transcripts, h := replayFixture(t)
		rr := replayAs(t, h, "", sessionserver.ReplayRequest{ReplayMode: sessionserver.ReplayWorkspaceDerive, TargetRuntime: "x"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d; body %s", rr.Code, rr.Body.String())
		}
		if f.store.callsOf("Create") != 1 || f.store.callsOf("CreateWithTranscript") != 0 {
			t.Fatalf("insert calls = %v, want one Create", f.store.calls)
		}
		if entries, _ := transcripts.Get(context.Background(), "acme", "sess_replay"); len(entries) != 0 {
			t.Errorf("a workspace_derive replay copied the transcript: %v", entries)
		}
	})
	t.Run("copy error", func(t *testing.T) {
		f, _, h := replayFixture(t)
		f.store.createWithTransErr = errors.New("transcript copy failed")
		rr := replayAs(t, h, "", sessionserver.ReplayRequest{TargetRuntime: "x"})
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rr.Code)
		}
		if _, err := f.mem.Get(context.Background(), "acme", "sess_replay"); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("the failed replay left a row")
		}
	})
	t.Run("owner restricted after admission", func(t *testing.T) {
		f, transcripts, h := replayFixture(t)
		f.store.beforeInsert = func() { f.restrict(t, "alice@acme.com", "job-2") }
		rr := replayAs(t, h, "", sessionserver.ReplayRequest{TargetRuntime: "x"})
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
		}
		if code, _, _ := decodeError(t, rr); code != "ERASURE_IN_PROGRESS" {
			t.Errorf("code = %s", code)
		}
		if entries, _ := transcripts.Get(context.Background(), "acme", "sess_replay"); len(entries) != 0 {
			t.Errorf("the refused replay left transcript entries: %v", entries)
		}
	})
}
