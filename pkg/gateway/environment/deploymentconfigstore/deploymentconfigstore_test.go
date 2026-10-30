// SPDX-License-Identifier: MIT

package deploymentconfigstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/deploymentconfigstore"
)

// spec: §16.7 — the baseline reports not-found until the first Put, then
// round-trips the recorded config so the audit emitter has a prior value
// to diff against.
func TestMemoryRoundTrip(t *testing.T) {
	m := deploymentconfigstore.NewMemory()
	if _, found, err := m.Get(context.Background()); err != nil || found {
		t.Fatalf("empty store: found=%v err=%v, want found=false", found, err)
	}
	want := deploymentconfigstore.Config{
		CycleDetectionMode: "warn", AllowSelfRecursion: "yes",
		DefaultMaxDepth: 12, ElicitationFloor: "enforce", LastRevision: 7,
	}
	if err := m.Put(context.Background(), want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, found, err := m.Get(context.Background())
	if err != nil || !found {
		t.Fatalf("after put: found=%v err=%v", found, err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
	// A second Put overwrites.
	next := want
	next.ElicitationFloor = "detect-only"
	next.LastRevision = 8
	_ = m.Put(context.Background(), next)
	if got, _, _ := m.Get(context.Background()); got != next {
		t.Errorf("overwrite = %+v, want %+v", got, next)
	}
}
