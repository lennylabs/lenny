// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §8.9 line 1010 — ProjectNodeAttributes surfaces the
// row-persisted per-node tracking attributes (generation, pod, lease)
// onto the tree node. F-8.9.1.
func TestProjectNodeAttributesSurfacesIdentityAndLease_spec_8_9_1010(t *testing.T) {
	t.Parallel()
	lease := &sessionstore.DelegationLease{MaxTokenBudget: 5000, MaxParallelChildren: 4}
	attrs := sessionstore.ProjectNodeAttributes(sessionstore.Session{
		ID:                 "child1",
		RecoveryGeneration: 2,
		PodAssignment:      "pod-abc",
		DelegationLease:    lease,
	})
	if attrs.Generation != 2 {
		t.Errorf("Generation = %d, want 2", attrs.Generation)
	}
	if attrs.Pod != "pod-abc" {
		t.Errorf("Pod = %q, want pod-abc", attrs.Pod)
	}
	if attrs.Lease != lease {
		t.Errorf("Lease = %v, want the granted ceiling", attrs.Lease)
	}
	if attrs.FailureHistory != nil {
		t.Errorf("FailureHistory = %v, want nil for a clean node", attrs.FailureHistory)
	}
}

// spec: §8.9 line 1010 — a clean node (no retry, no failure) projects no
// FailureHistory object, so the wire response stays minimal. F-8.9.1.
func TestProjectNodeAttributesOmitsCleanFailureHistory_spec_8_9_1010(t *testing.T) {
	t.Parallel()
	attrs := sessionstore.ProjectNodeAttributes(sessionstore.Session{ID: "ok", State: session.StateCompleted})
	if attrs.FailureHistory != nil {
		t.Fatalf("FailureHistory = %v, want nil", attrs.FailureHistory)
	}
}

// spec: §8.9 line 1010 — a node that retried or failed carries its
// failure history (retry count plus the §7.1 terminal cause). F-8.9.1.
func TestProjectNodeAttributesCarriesFailureHistory_spec_8_9_1010(t *testing.T) {
	t.Parallel()
	attrs := sessionstore.ProjectNodeAttributes(sessionstore.Session{
		ID:            "failed1",
		State:         session.StateFailed,
		RetryCount:    3,
		FailureClass:  session.FailureClass("infrastructure"),
		FailureReason: "READY_TIMEOUT",
	})
	if attrs.FailureHistory == nil {
		t.Fatal("FailureHistory = nil, want populated")
	}
	if attrs.FailureHistory.RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", attrs.FailureHistory.RetryCount)
	}
	if attrs.FailureHistory.FailureClass != "infrastructure" {
		t.Errorf("FailureClass = %q, want infrastructure", attrs.FailureHistory.FailureClass)
	}
	if attrs.FailureHistory.FailureReason != "READY_TIMEOUT" {
		t.Errorf("FailureReason = %q, want READY_TIMEOUT", attrs.FailureHistory.FailureReason)
	}
}

// spec: §8.9 line 1010 — a node that retried but has not failed still
// carries a failure history with the retry counter, so a recovering
// node's reattach history is visible before any terminal failure.
// F-8.9.1.
func TestProjectNodeAttributesRetryOnlyCarriesHistory_spec_8_9_1010(t *testing.T) {
	t.Parallel()
	attrs := sessionstore.ProjectNodeAttributes(sessionstore.Session{ID: "r", RetryCount: 1})
	if attrs.FailureHistory == nil || attrs.FailureHistory.RetryCount != 1 {
		t.Fatalf("FailureHistory = %+v, want RetryCount 1", attrs.FailureHistory)
	}
	if attrs.FailureHistory.FailureClass != "" || attrs.FailureHistory.FailureReason != "" {
		t.Errorf("retry-only node should carry no failure cause, got %+v", attrs.FailureHistory)
	}
}
