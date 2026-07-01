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

// failingCopier is a blobstore.Store whose Copy always reports the source
// object missing, so the §7.1 derive copy stage fails after the lock is
// released — the post-copy-attempt failure the §7.1 derive rule 2 audit
// row captures. F-15.1.14.
type failingCopier struct{ *blobstore.MemoryStore }

func (failingCopier) Copy(_, _ blobstore.URI) error { return blobstore.ErrNotFound }

func newFailingBlobs() failingCopier {
	return failingCopier{blobstore.NewMemoryStore(func() time.Time { return time.Unix(0, 0).UTC() })}
}

// fenceStore simulates a §10.1 coordinator handoff: the source session's
// coordination_generation advances after the derive-admission read, so the
// derive-failure persist re-read observes a changed generation and the
// stale replica's INSERT is fenced out. F-15.1.14.
type fenceStore struct {
	sessionstore.Store
	target string
	calls  int
}

func (f *fenceStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := f.Store.Get(ctx, tenantID, id)
	if err == nil && id == f.target {
		f.calls++
		if f.calls >= 2 {
			row.CoordinationGeneration++
		}
	}
	return row, err
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

// spec: §7.1 derive rule 2 / §15.1 lines 647-663 — under the
// persistDeriveFailureRows opt-in, a derive that fails at the copy stage
// persists a terminal failed row with failureClass=derive_failure, and the
// §16.1 audit counter bumps "persisted". F-15.1.14.
func TestDeriveFailurePersistsAuditRow_spec_7_1_derive_rule_2(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	ctr := &captureCounter{}
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                   func() string { return "sess_df" },
		Clock:                    func() time.Time { return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC) },
		Blobs:                    newFailingBlobs(),
		PersistDeriveFailureRows: true,
		IncDeriveFailureAudit:    ctr.inc,
	})
	h := srv.Handler()

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

	// §15.1 line 651 — GET returns 200 with the derive_failure envelope.
	got := sessionRequest(t, h, http.MethodGet, "/v1/sessions/sess_df")
	if got.Code != http.StatusOK {
		t.Fatalf("get derive_failure row: got %d, want 200; body=%s", got.Code, got.Body.String())
	}
	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(got.Body.Bytes(), &resp)
	if resp.State != string(session.StateFailed) || resp.FailureClass != string(session.FailureClassDeriveFailure) {
		t.Errorf("envelope: got state=%q failureClass=%q, want failed/derive_failure", resp.State, resp.FailureClass)
	}

	// §15.1 line 652 — included in the default list, excluded with the flag.
	// The seeded source session (sess_source) is also present; the
	// derive_failure row (sess_df) is the membership the matrix asserts.
	if rows := listSessions(t, h, ""); !containsID(rows, "sess_df") {
		t.Errorf("default list must include derive_failure row sess_df, got %v", listIDs(rows))
	}
	if rows := listSessions(t, h, "includeDeriveFailures=false"); containsID(rows, "sess_df") {
		t.Errorf("includeDeriveFailures=false must drop sess_df, got %v", listIDs(rows))
	}

	// §15.1 lines 654-658 — action endpoints reject the terminal row with
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
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                func() string { return "sess_df" },
		Blobs:                 newFailingBlobs(),
		IncDeriveFailureAudit: ctr.inc,
		// PersistDeriveFailureRows left false.
	})
	h := srv.Handler()

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
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:                   func() string { return "sess_df" },
		Blobs:                    newFailingBlobs(),
		PersistDeriveFailureRows: true,
		IncDeriveFailureAudit:    ctr.inc,
	})
	h := srv.Handler()

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
