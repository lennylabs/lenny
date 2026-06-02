// SPDX-License-Identifier: MIT

package failopen

import (
	"context"
	"errors"
	"testing"
)

// spec: §12.4 line 224 — cold start defaults to 1; Observe updates the
// last-known good count.
func TestReplicaCountColdStartAndObserve_spec_12_4(t *testing.T) {
	rc := NewReplicaCount()
	if got := rc.Get(); got != 1 {
		t.Fatalf("cold-start replica count = %d, want 1", got)
	}
	rc.Observe(4)
	if got := rc.Get(); got != 4 {
		t.Fatalf("after Observe(4) replica count = %d, want 4", got)
	}
}

// spec: §12.4 line 224 — a non-positive (zero ready endpoints) read does
// not overwrite the last-known good count.
func TestReplicaCountIgnoresNonPositive_spec_12_4(t *testing.T) {
	rc := NewReplicaCount()
	rc.Observe(3)
	rc.Observe(0)
	rc.Observe(-1)
	if got := rc.Get(); got != 3 {
		t.Fatalf("replica count = %d, want 3 (non-positive reads ignored)", got)
	}
}

type stubLister struct {
	n   int
	err error
}

func (s stubLister) CountReady(context.Context) (int, error) { return s.n, s.err }

// A successful poll updates the count.
func TestReplicaPollerObservesOnSuccess(t *testing.T) {
	rc := NewReplicaCount()
	p := &ReplicaPoller{Lister: stubLister{n: 5}, Count: rc}
	p.pollOnce(context.Background())
	if got := rc.Get(); got != 5 {
		t.Fatalf("after poll replica count = %d, want 5", got)
	}
}

// spec: §12.4 line 224 — a poll failure retains the last-known good count
// (the dual-outage protection).
func TestReplicaPollerRetainsOnFailure_spec_12_4(t *testing.T) {
	rc := NewReplicaCount()
	rc.Observe(6)
	p := &ReplicaPoller{Lister: stubLister{err: errors.New("api server unreachable")}, Count: rc}
	p.pollOnce(context.Background())
	if got := rc.Get(); got != 6 {
		t.Fatalf("after a failed poll replica count = %d, want 6 (retained)", got)
	}
}

// A poller missing a required seam is a no-op (does not panic).
func TestReplicaPollerNoOpWithoutSeam(t *testing.T) {
	(&ReplicaPoller{}).Run(context.Background())
	(&ReplicaPoller{Count: NewReplicaCount()}).Run(context.Background())
}
