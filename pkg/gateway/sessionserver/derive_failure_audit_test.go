// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// failingCopier is a blobstore.Store whose chunk Copy always reports the
// source chunk missing, so the §7.1 derive copy stage fails after the lock
// is released, on the chunk-copy arm, which is the post-copy-attempt
// failure the §7.1 derive rule 2 audit row captures. F-15.1.14.
type failingCopier struct{ *blobstore.MemoryStore }

func (failingCopier) Copy(_, _ blobstore.URI) error { return blobstore.ErrNotFound }

// failingCopyOptions returns o wired with the source session's chunked
// checkpoint, its manifest reader and writer, and a chunk resolver, behind a
// blob store whose chunk Copy reports the chunk missing, so the derive
// reaches the chunk copy and answers 503 DERIVE_SNAPSHOT_UNAVAILABLE rather
// than the unwired 500.
func failingCopyOptions(t *testing.T, o sessionserver.Options) sessionserver.Options {
	t.Helper()
	snap := sourceSnapshot(t)
	snap.apply(&o)
	o.Blobs = failingCopier{snap.blobs}
	return o
}

// fenceStore simulates a §10.1 coordinator handoff: the source session's
// coordination_generation advances after the derive-admission read, so the
// derive-failure persist re-read observes a changed generation and the
// stale replica's INSERT is fenced out. F-15.1.14.
type fenceStore struct {
	sessionstore.Store
	target string
}

// CreateIfGeneration commits a replacement coordinator's handoff on the
// source row (a coordination_generation bump) after the derive's admission
// and before the failure write's fenced insert.
func (f *fenceStore) CreateIfGeneration(ctx context.Context, row sessionstore.Session, tenantID, sourceID string, wantGen int64) error {
	if sourceID == f.target {
		if _, err := f.Update(ctx, tenantID, sourceID, func(s *sessionstore.Session) error {
			s.CoordinationGeneration++
			return nil
		}); err != nil {
			return err
		}
	}
	return f.Store.CreateIfGeneration(ctx, row, tenantID, sourceID, wantGen)
}

// captureCounter records the §16.1 derive-failure audit outcomes.
type captureCounter struct{ outcomes []string }

func (c *captureCounter) inc(outcome string) { c.outcomes = append(c.outcomes, outcome) }

func containsID(rows []sessionserver.SessionResponse, id string) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

// spec: §7.1 derive rule 2 / §15.1 — under the
// persistDeriveFailureRows opt-in, a derive that fails at the copy stage
// persists a terminal failed row with failureClass=derive_failure, and the
// §16.1 audit counter bumps "persisted". F-15.1.14.
func TestDeriveFailurePersistsAuditRow_spec_7_1_derive_rule_2(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	ctr := &captureCounter{}
	srv := sessionserver.New(store, failingCopyOptions(t, sessionserver.Options{
		DevTenantHeader:          true,
		IDFunc:                   func() string { return "sess_df" },
		Clock:                    func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) },
		PersistDeriveFailureRows: true,
		IncDeriveFailureAudit:    ctr.inc,
	}))
	h := asOwner(srv.Handler())

	rr := deriveRequest(t, h, sessionserver.DeriveRequest{})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("derive status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if code, _, _ := decodeError(t, rr); code != "DERIVE_SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("error code: got %q, want DERIVE_SNAPSHOT_UNAVAILABLE", code)
	}
	if len(ctr.outcomes) != 1 || ctr.outcomes[0] != "persisted" {
		t.Fatalf("audit outcomes: got %v, want [persisted]", ctr.outcomes)
	}

	// §15.1 — GET returns 200 with the derive_failure envelope.
	got := sessionRequest(t, h, http.MethodGet, "/v1/sessions/sess_df")
	if got.Code != http.StatusOK {
		t.Fatalf("get derive_failure row: got %d, want 200; body=%s", got.Code, got.Body.String())
	}
	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(got.Body.Bytes(), &resp)
	if resp.State != string(session.StateFailed) || resp.FailureClass != string(session.FailureClassDeriveFailure) {
		t.Errorf("envelope: got state=%q failureClass=%q, want failed/derive_failure", resp.State, resp.FailureClass)
	}

	// §15.1 — included in the default list, excluded with the flag.
	// The seeded source session (sess_source) is also present; the
	// derive_failure row (sess_df) is the membership the matrix asserts.
	if rows := listSessions(t, h, ""); !containsID(rows, "sess_df") {
		t.Errorf("default list must include derive_failure row sess_df, got %v", listIDs(rows))
	}
	if rows := listSessions(t, h, "includeDeriveFailures=false"); containsID(rows, "sess_df") {
		t.Errorf("includeDeriveFailures=false must drop sess_df, got %v", listIDs(rows))
	}

	// §15.1 — action endpoints reject the terminal row with
	// 409 INVALID_STATE_TRANSITION.
	for _, ep := range []string{"terminate", "interrupt", "resume", "finalize", "start"} {
		ar := sessionRequest(t, h, http.MethodPost, "/v1/sessions/sess_df/"+ep)
		if ar.Code != http.StatusConflict {
			t.Errorf("POST /%s on derive_failure row: got %d, want 409; body=%s", ep, ar.Code, ar.Body.String())
		}
	}
}

// spec: §7.1 derive rule 2 — the default (opt-out) posture writes nothing
// on a derive failure: no derive_failure row, no audit bump. F-15.1.14.
func TestDeriveFailureNoPersistByDefault_spec_7_1_derive_rule_2(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	ctr := &captureCounter{}
	srv := sessionserver.New(store, failingCopyOptions(t, sessionserver.Options{
		DevTenantHeader:       true,
		IDFunc:                func() string { return "sess_df" },
		IncDeriveFailureAudit: ctr.inc,
		// PersistDeriveFailureRows left false.
	}))
	h := asOwner(srv.Handler())

	rr := deriveRequest(t, h, sessionserver.DeriveRequest{})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("derive status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if len(ctr.outcomes) != 0 {
		t.Errorf("audit outcomes with opt-in off: got %v, want none", ctr.outcomes)
	}
	if got := sessionRequest(t, h, http.MethodGet, "/v1/sessions/sess_df"); got.Code != http.StatusNotFound {
		t.Errorf("GET sess_df with opt-in off: got %d, want 404 (no row persisted)", got.Code)
	}
}

// spec: §7.1 derive rule 2 — the CAS fence: when a replacement coordinator
// has incremented the source's coordination_generation mid-copy, the stale
// replica's audit INSERT is fenced out (no orphan row, outcome "fenced").
// F-15.1.14.
func TestDeriveFailureCASFenceSkipsWrite_spec_7_1_derive_rule_2(t *testing.T) {
	base := memstore.New()
	newSourceSession(t, base)
	store := &fenceStore{Store: base, target: "sess_source"}
	ctr := &captureCounter{}
	srv := sessionserver.New(store, failingCopyOptions(t, sessionserver.Options{
		DevTenantHeader:          true,
		IDFunc:                   func() string { return "sess_df" },
		PersistDeriveFailureRows: true,
		IncDeriveFailureAudit:    ctr.inc,
	}))
	h := asOwner(srv.Handler())

	rr := deriveRequest(t, h, sessionserver.DeriveRequest{})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("derive status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if len(ctr.outcomes) != 1 || ctr.outcomes[0] != "fenced" {
		t.Fatalf("audit outcomes: got %v, want [fenced]", ctr.outcomes)
	}
	// No orphan failed row became visible.
	if got := sessionRequest(t, h, http.MethodGet, "/v1/sessions/sess_df"); got.Code != http.StatusNotFound {
		t.Errorf("GET sess_df after fence: got %d, want 404 (no orphan row)", got.Code)
	}
}

// resumingSourceStore moves the source row from resume_pending to resuming,
// as the resume driver's entry write does, after the derive's admission read
// and before its failure write's fenced insert.
type resumingSourceStore struct {
	sessionstore.Store
	target string
	moved  bool
}

func (r *resumingSourceStore) CreateIfGeneration(ctx context.Context, row sessionstore.Session, tenantID, sourceID string, wantGen int64) error {
	if sourceID == r.target && !r.moved {
		r.moved = true
		if _, err := r.Update(ctx, tenantID, sourceID, func(s *sessionstore.Session) error {
			s.State = session.StateResuming
			s.CoordinationGeneration++
			return nil
		}); err != nil {
			return err
		}
	}
	return r.Store.CreateIfGeneration(ctx, row, tenantID, sourceID, wantGen)
}

// spec: §7.1 (Normal Flow) derive rule 2, §7.3 (Resume driver) — a stale
// derive admitted from a resume_pending source whose row enters resuming
// before the derive-failure write persists no row and counts the fenced
// outcome, because the entry write advanced the source's generation.
func TestDeriveFailureFencedByAResumeEntry_spec_7_1(t *testing.T) {
	base := memstore.New()
	newSourceSession(t, base, func(r *sessionstore.Session) { r.State = session.StateResumePending })
	store := &resumingSourceStore{Store: base, target: "sess_source"}
	ctr := &captureCounter{}
	srv := sessionserver.New(store, failingCopyOptions(t, sessionserver.Options{
		DevTenantHeader:          true,
		IDFunc:                   func() string { return "sess_df" },
		PersistDeriveFailureRows: true,
		IncDeriveFailureAudit:    ctr.inc,
	}))
	h := asOwner(srv.Handler())
	rr := deriveRequest(t, h, sessionserver.DeriveRequest{AllowStale: true})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("derive status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	if len(ctr.outcomes) != 1 || ctr.outcomes[0] != "fenced" {
		t.Fatalf("audit outcomes: got %v, want [fenced]", ctr.outcomes)
	}
	if got := sessionRequest(t, h, http.MethodGet, "/v1/sessions/sess_df"); got.Code != http.StatusNotFound {
		t.Errorf("GET sess_df after the fence: got %d, want 404", got.Code)
	}
}

// TestDeriveFailureAuditRowIsNoTreeNode_spec_15_1 pins that the
// derive-failure audit row carries no delegation parent, keeps its lineage
// to the source in the parent workspace reference, and is absent from the
// source's tree.
//
// spec: §15.1 (Session Replay Semantics), §4.5 (Workspace lineage), §12.6 (StoreRouter)
func TestDeriveFailureAuditRowIsNoTreeNode_spec_15_1(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	src := newSourceSession(t, store)
	srv := sessionserver.New(store, failingCopyOptions(t, sessionserver.Options{
		DevTenantHeader:          true,
		IDFunc:                   func() string { return "sess_df" },
		PersistDeriveFailureRows: true,
	}))

	if rr := deriveRequest(t, asOwner(srv.Handler()), sessionserver.DeriveRequest{}); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("derive status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(ctx, "acme", "sess_df")
	if err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if row.ParentSessionID != "" {
		t.Errorf("ParentSessionID = %q, want empty", row.ParentSessionID)
	}
	if row.ParentWorkspaceRef != src.WorkspaceSnapshot.Ref {
		t.Errorf("ParentWorkspaceRef = %q, want the source snapshot %q", row.ParentWorkspaceRef, src.WorkspaceSnapshot.Ref)
	}
	tree, err := store.ListByRoot(ctx, "acme", src.ID)
	if err != nil {
		t.Fatalf("list source tree: %v", err)
	}
	for _, n := range tree {
		if n.ID == "sess_df" {
			t.Errorf("derive-failure audit row is a node in the source's tree")
		}
	}
}
