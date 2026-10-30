// SPDX-License-Identifier: MIT

package responsesstub_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"
)

// releaser is an executor whose Release records each disposition it is
// handed and returns err.
type releaser struct {
	executor.Executor
	err   error
	disps []executor.Disposition
}

func (r *releaser) Release(_ context.Context, _ string, disp executor.Disposition) error {
	r.disps = append(r.disps, disp)
	return r.err
}

// failedRow seeds a failed session row and returns it.
func failedRow(t *testing.T, store *memstore.Store) sessionstore.Session {
	t.Helper()
	row := sessionstore.Session{ID: "sess-1", TenantID: "acme", State: session.StateFailed}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	return row
}

// released reports whether the row carries a pod release marker.
func released(t *testing.T, store *memstore.Store) bool {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", "sess-1")
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	return !row.PodReleasedAt.IsZero()
}

// TestLocalForwarderServesEveryTurnLocally_spec_29_3 pins the stub
// forwarder: every turn is local with nothing written, and no request is
// forwarded.
//
// spec: §29.3 (Interactive message send) Forwarding, §15 (Open Responses
// conversation sessions)
func TestLocalForwarderServesEveryTurnLocally_spec_29_3(t *testing.T) {
	f := responsesstub.LocalForwarder{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	refused := 0
	if !f.ForwardResponsesTurn(rec, req, sessionstore.Session{ID: "sess-1"}, []byte("{}"),
		func(http.ResponseWriter, string) { refused++ }) {
		t.Error("ForwardResponsesTurn = false, want the turn local")
	}
	if refused != 0 || rec.Body.Len() != 0 {
		t.Errorf("refusals %d, body %q; want none written", refused, rec.Body.String())
	}
	if f.Forwarded(context.Background()) {
		t.Error("Forwarded = true, want false")
	}
}

// TestReleasingTerminalReleasesAFailedSession_spec_15 pins the stub
// terminal's post-terminal hook: it releases the binding with the failed
// disposition and records the pod release, it records the release when the
// executor has no release operation, it records nothing when the release
// fails, and it tolerates a nil executor and a nil store. BeginTerminal opens
// no claim.
//
// spec: §15 (Open Responses conversation sessions), §6.2 (Pod State Machine)
func TestReleasingTerminalReleasesAFailedSession_spec_15(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantRelease bool
	}{
		{"release succeeds", nil, true},
		{"executor has no release operation", executor.ErrUnsupported, true},
		{"release fails", errors.New("pod unreachable"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			row := failedRow(t, store)
			exec := &releaser{err: tc.err}
			term := responsesstub.ReleasingTerminal{Exec: exec, Store: store}
			term.BeginTerminal(row.ID)()
			got := term.AfterTerminalWrite(context.Background(), session.StateFailed, row, nil)
			if got.ID != row.ID {
				t.Errorf("AfterTerminalWrite returned %q, want the session row", got.ID)
			}
			if len(exec.disps) != 1 || exec.disps[0] != executor.DispositionFailed {
				t.Errorf("dispositions = %v, want one failed release", exec.disps)
			}
			if r := released(t, store); r != tc.wantRelease {
				t.Errorf("pod release recorded = %v, want %v", r, tc.wantRelease)
			}
		})
	}

	t.Run("nil executor and store", func(t *testing.T) {
		term := responsesstub.ReleasingTerminal{}
		row := sessionstore.Session{ID: "sess-1", TenantID: "acme", State: session.StateFailed, UpdatedAt: time.Now()}
		if got := term.AfterTerminalWrite(context.Background(), session.StateFailed, row, nil); got.ID != row.ID {
			t.Errorf("AfterTerminalWrite returned %q, want the session row", got.ID)
		}
	})
}
