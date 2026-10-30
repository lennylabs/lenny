// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// reasonCodeCase is one stored failure reason and the §8.8 error code,
// category, and retryable flag the child's TaskResult and child_failed
// event must carry for it.
type reasonCodeCase struct {
	reason    string
	code      string
	category  string
	retryable bool
}

// everyReasonCodeCase enumerates every reason the platform stamps on a
// terminal row, the per-state empty-reason fallback, and reasons stored
// outside the FailureReason enum. Categories come from the §15.1
// catalog; a code the catalog does not list takes the §15.2.1 fallback
// (TRANSIENT, retryable).
func everyReasonCodeCase() []reasonCodeCase {
	return []reasonCodeCase{
		{string(session.FailureDeadlockTimeout), "DEADLOCK_TIMEOUT", "TRANSIENT", true},
		{string(session.FailureRuntimeCrash), "RUNTIME_CRASH", "TRANSIENT", true},
		{string(session.FailureSetupCommandFailed), "SETUP_COMMAND_FAILED", "PERMANENT", false},
		{string(session.FailureExpiredDeadline), "expired:deadline", "TRANSIENT", true},
		{string(session.FailureExpiredBudget), "expired:budget", "TRANSIENT", true},
		{string(session.FailureExpiredLease), "expired:lease", "TRANSIENT", true},
		{string(session.FailureExpiredIdle), "expired:idle", "TRANSIENT", true},
		{string(session.FailurePodEvicted), "pod_evicted", "TRANSIENT", true},
		{string(session.FailureNodeLost), "node_lost", "TRANSIENT", true},
		{string(session.FailureWorkspaceValidationFailed), "workspace_validation_failed", "TRANSIENT", true},
		{string(session.FailureOrphanPodTerminated), "orphan_pod_terminated", "TRANSIENT", true},
		{"workspace_seal_timeout", "workspace_seal_timeout", "TRANSIENT", true},
		{"", "CHILD_FAILED", "TRANSIENT", true},
	}
}

// TestTaskErrorForSessionEveryReason_spec_8_8 drives every stored reason
// through the archived TaskResult error block. deadlock_timeout must
// surface as DEADLOCK_TIMEOUT, the code §8.8 names for a detector
// failure, rather than the stored reason.
//
// spec: §8.8 (TaskRecord and TaskResult Schema), §15.2.1 (REST/MCP
// Consistency Contract).
func TestTaskErrorForSessionEveryReason_spec_8_8(t *testing.T) {
	for _, tc := range everyReasonCodeCase() {
		t.Run("reason="+tc.reason, func(t *testing.T) {
			got := taskErrorForSession(sessionstore.Session{
				UserID: testOwner,
				ID:     "child", State: session.StateFailed, FailureReason: tc.reason,
			}, 3)
			if got.Code != tc.code || got.Category != tc.category {
				t.Fatalf("error = {code %q category %q}, want {code %q category %q}",
					got.Code, got.Category, tc.code, tc.category)
			}
		})
	}
}

// TestEmitChildFailedCarriesErrorCodeEveryReason_spec_8_10 drives every
// stored reason through the §8.10 child_failed event and asserts the
// event carries the same code and category as the child's TaskResult,
// plus the retryable flag and a message, the shared taxonomy the MCP
// notifications/lenny/error frame projects. The stored reason is kept
// alongside so the §7.3 classification stays inspectable.
//
// spec: §8.10 (Delegation Tree Recovery), §8.8 (TaskRecord and
// TaskResult Schema), §15.2 (MCP API).
func TestEmitChildFailedCarriesErrorCodeEveryReason_spec_8_10(t *testing.T) {
	for _, tc := range everyReasonCodeCase() {
		t.Run("reason="+tc.reason, func(t *testing.T) {
			store := memstore.New()
			bus := sessionevents.NewBus(0)
			srv := New(store, Options{DevTenantHeader: true, Events: bus})
			now := time.Now()
			mustCreate(t, store, sessionstore.Session{UserID: testOwner, ID: "parent", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now})
			child := sessionstore.Session{
				UserID: testOwner,
				ID:     "child", TenantID: "acme", State: session.StateFailed,
				ParentSessionID: "parent", FailureClass: session.FailureClassRuntime,
				FailureReason: tc.reason, CreatedAt: now, UpdatedAt: now,
			}
			mustCreate(t, store, child)
			srv.emitChildFailed(context.Background(), child)

			ev := childFailedEvent(t, bus, "parent")
			if ev == nil {
				t.Fatal("no child_failed event")
			}
			if ev["code"] != tc.code {
				t.Errorf("code = %v, want %q", ev["code"], tc.code)
			}
			if ev["category"] != tc.category {
				t.Errorf("category = %v, want %q", ev["category"], tc.category)
			}
			if ev["retryable"] != tc.retryable {
				t.Errorf("retryable = %v, want %v", ev["retryable"], tc.retryable)
			}
			if msg, _ := ev["message"].(string); msg == "" {
				t.Error("message is empty")
			}
			if tc.reason != "" && ev["failure_reason"] != tc.reason {
				t.Errorf("failure_reason = %v, want the stored %q", ev["failure_reason"], tc.reason)
			}
			// The TaskResult error block and the event agree.
			tr := taskErrorForSession(child, 3)
			if ev["code"] != tr.Code || ev["category"] != tr.Category {
				t.Errorf("event {%v %v} disagrees with TaskResult {%s %s}", ev["code"], ev["category"], tr.Code, tr.Category)
			}
		})
	}
}

// TestEmitChildFailedOnlyForFailedChild_spec_8_10 covers the guard
// paths: a non-failed terminal child, a root session, and a server with
// no event sink publish no child_failed event, whatever the reason.
//
// spec: §8.10 (Delegation Tree Recovery).
func TestEmitChildFailedOnlyForFailedChild_spec_8_10(t *testing.T) {
	now := time.Now()
	reason := string(session.FailureDeadlockTimeout)
	cases := []struct {
		name  string
		child sessionstore.Session
		sink  bool
	}{
		{"expired child", sessionstore.Session{UserID: testOwner, ID: "c", TenantID: "acme", State: session.StateExpired, ParentSessionID: "parent", FailureReason: reason, CreatedAt: now, UpdatedAt: now}, true},
		{"cancelled child", sessionstore.Session{UserID: testOwner, ID: "c", TenantID: "acme", State: session.StateCancelled, ParentSessionID: "parent", FailureReason: reason, CreatedAt: now, UpdatedAt: now}, true},
		{"root session", sessionstore.Session{UserID: testOwner, ID: "c", TenantID: "acme", State: session.StateFailed, FailureReason: reason, CreatedAt: now, UpdatedAt: now}, true},
		{"no event sink", sessionstore.Session{UserID: testOwner, ID: "c", TenantID: "acme", State: session.StateFailed, ParentSessionID: "parent", FailureReason: reason, CreatedAt: now, UpdatedAt: now}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			bus := sessionevents.NewBus(0)
			opts := Options{DevTenantHeader: true}
			if tc.sink {
				opts.Events = bus
			}
			srv := New(store, opts)
			srv.emitChildFailed(context.Background(), tc.child)
			if ev := childFailedEvent(t, bus, "parent"); ev != nil {
				t.Fatalf("child_failed published for %s: %v", tc.name, ev)
			}
		})
	}
}
