// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestToTaskResultErrorCodeEveryReason_spec_8_8 drives every stored
// failure reason, and every non-completed terminal state with no reason,
// through the row-only TaskResult the await path returns before the
// archived body exists. deadlock_timeout must surface as
// DEADLOCK_TIMEOUT. A completed child carries no error block.
//
// spec: §8.8 (TaskRecord and TaskResult Schema), §15.2.1 (REST/MCP
// Consistency Contract).
func TestToTaskResultErrorCodeEveryReason_spec_8_8(t *testing.T) {
	cases := []struct {
		state    session.State
		reason   string
		code     string
		category string
	}{
		{session.StateFailed, string(session.FailureDeadlockTimeout), "DEADLOCK_TIMEOUT", "TRANSIENT"},
		{session.StateFailed, string(session.FailureRuntimeCrash), "RUNTIME_CRASH", "TRANSIENT"},
		{session.StateFailed, string(session.FailureSetupCommandFailed), "SETUP_COMMAND_FAILED", "PERMANENT"},
		{session.StateFailed, string(session.FailurePodEvicted), "pod_evicted", "TRANSIENT"},
		{session.StateFailed, string(session.FailureNodeLost), "node_lost", "TRANSIENT"},
		{session.StateFailed, string(session.FailureWorkspaceValidationFailed), "workspace_validation_failed", "TRANSIENT"},
		{session.StateFailed, string(session.FailureOrphanPodTerminated), "orphan_pod_terminated", "TRANSIENT"},
		{session.StateExpired, string(session.FailureExpiredDeadline), "expired:deadline", "TRANSIENT"},
		{session.StateExpired, string(session.FailureExpiredBudget), "expired:budget", "TRANSIENT"},
		{session.StateExpired, string(session.FailureExpiredLease), "expired:lease", "TRANSIENT"},
		{session.StateExpired, string(session.FailureExpiredIdle), "expired:idle", "TRANSIENT"},
		{session.StateFailed, "", "CHILD_FAILED", "TRANSIENT"},
		{session.StateExpired, "", "CHILD_EXPIRED", "TRANSIENT"},
		{session.StateCancelled, "", "CHILD_CANCELLED", "TRANSIENT"},
	}
	for _, tc := range cases {
		t.Run(string(tc.state)+"/"+tc.reason, func(t *testing.T) {
			tr := toTaskResult(sessionstore.Session{ID: "child", State: tc.state, FailureReason: tc.reason}, 3)
			if tr.Error == nil {
				t.Fatal("non-completed terminal child has no error block")
			}
			if tr.Error.Code != tc.code || tr.Error.Category != tc.category {
				t.Fatalf("error = {code %q category %q}, want {code %q category %q}",
					tr.Error.Code, tr.Error.Category, tc.code, tc.category)
			}
		})
	}
	if tr := toTaskResult(sessionstore.Session{ID: "child", State: session.StateCompleted}, 3); tr.Error != nil {
		t.Fatalf("completed child carries error %+v, want nil", tr.Error)
	}
}

// TestResolveChildDeadlockTimeoutCode_spec_8_8 resolves a child the §8.8
// deadlock detector failed through the live-row await path, with no
// archive, and asserts the DEADLOCK_TIMEOUT code reaches the parent.
//
// spec: §8.8 (TaskRecord and TaskResult Schema).
func TestResolveChildDeadlockTimeoutCode_spec_8_8(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "child", TenantID: "acme", State: session.StateFailed,
		FailureClass: session.FailureClassRuntime, FailureReason: string(session.FailureDeadlockTimeout),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	oc, err := resolveChild(context.Background(), store, nil, nil, nil, "acme", "child")
	if err != nil {
		t.Fatalf("resolveChild: %v", err)
	}
	if oc.result.Error == nil || oc.result.Error.Code != "DEADLOCK_TIMEOUT" {
		t.Fatalf("resolved error = %+v, want code DEADLOCK_TIMEOUT", oc.result.Error)
	}
}
