// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// readHookStore runs after once after the first primary read returns.
type readHookStore struct {
	sessionstore.Store
	once  sync.Once
	after func()
}

func (r *readHookStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := r.Get(ctx, tenantID, id)
	if r.after != nil {
		r.once.Do(r.after)
	}
	return row, err
}

// TestReportOnASuspendedRowNeedsItsPod_spec_6_2 asserts that a failure report
// on a suspended row whose pod was released, cleared after the report's read
// or podless before it, is a no-op for every mutator outcome (retries left,
// retries exhausted, and non-retryable), while a report on a suspended row
// that still names the reported pod takes its edge and releases the binding
// once.
//
// spec: §6.2 (Pod State Machine)
func TestReportOnASuspendedRowNeedsItsPod_spec_6_2(t *testing.T) {
	outcomes := []struct {
		name    string
		reason  string
		retries int64
		want    session.State
	}{
		{"retries left", string(session.FailureRuntimeCrash), 0, session.StateResumePending},
		{"retries exhausted", string(session.FailureRuntimeCrash), 2, session.StateAwaitingClientAction},
		{"non-retryable", string(session.FailureWorkspaceValidationFailed), 0, session.StateFailed},
	}
	for _, oc := range outcomes {
		for _, pre := range []string{"podless before the read", "cleared after the read", "still holds its pod"} {
			t.Run(oc.name+"/"+pre, func(t *testing.T) {
				f := newFunnelFixture(t, funnelOpts{})
				_, bind := f.seed(t, "s", session.StateSuspended, oc.retries, false)
				pod := bind.SandboxName
				if pre == "podless before the read" {
					pod = ""
				}
				if _, err := f.store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
					r.PodAssignment = pod
					return nil
				}); err != nil {
					t.Fatalf("seed pod: %v", err)
				}
				if pre == "cleared after the read" {
					f.srv.store = &readHookStore{Store: f.store, after: func() {
						_, _ = f.store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
							r.PodAssignment = ""
							return nil
						})
					}}
				}
				before := f.state(t, "s")
				_, err := f.srv.ReportSessionFailure(context.Background(), FailureReport{
					TenantID: "acme", SessionID: "s", Reason: oc.reason, PodAssignment: bind.SandboxName,
				})
				if err != nil {
					t.Fatalf("ReportSessionFailure: %v", err)
				}
				got := f.state(t, "s")
				if pre == "still holds its pod" {
					if got.State != oc.want {
						t.Errorf("state = %q, want %q", got.State, oc.want)
					}
					deadline := 0
					for len(f.exec.callList()) == 0 && deadline < 500 {
						deadline++
						waitShort()
					}
					if n := len(f.exec.callList()); n != 1 {
						t.Errorf("releases = %d, want 1", n)
					}
					return
				}
				if got.State != session.StateSuspended || got.RetryCount != before.RetryCount {
					t.Errorf("row = %q retry %d, want suspended retry %d unchanged", got.State, got.RetryCount, before.RetryCount)
				}
				if n := len(f.exec.callList()); n != 0 {
					t.Errorf("releases = %d, want none for a podless suspended row", n)
				}
			})
		}
	}
}
