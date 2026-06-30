// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fakeHWMReader records the root id it was asked about and returns a
// fixed result. found=false models a tree that admitted no delegation.
type fakeHWMReader struct {
	value      int64
	found      bool
	err        error
	calledWith []string
}

func (f *fakeHWMReader) ObserveHighWatermark(_ context.Context, rootSessionID string) (int64, bool, error) {
	f.calledWith = append(f.calledWith, rootSessionID)
	return f.value, f.found, f.err
}

// fakeHWMObserver records the values observed onto the histogram.
type fakeHWMObserver struct {
	pools   []string
	tenants []string
	values  []int64
}

func (f *fakeHWMObserver) ObserveDelegationParallelChildrenHighWatermark(pool, tenantID string, value int64) {
	f.pools = append(f.pools, pool)
	f.tenants = append(f.tenants, tenantID)
	f.values = append(f.values, value)
}

func newHWMServer(reader DelegationHighWatermarkReader, obs DelegationHighWatermarkObserver) *Server {
	return &Server{hwmReader: reader, hwmObserver: obs}
}

// spec: §8.3 line 379 — when a tree root settles, the gateway reads the
// per-tree parallel-children high-watermark and observes it onto the
// §16.1 histogram with the root's pool and tenant labels. F-8.9.6.
func TestObserveTreeHighWatermarkAtRootSettle_spec_8_3_379(t *testing.T) {
	reader := &fakeHWMReader{value: 5, found: true}
	obs := &fakeHWMObserver{}
	s := newHWMServer(reader, obs)
	s.observeTreeHighWatermark(context.Background(), sessionstore.Session{
		ID: "root1", TenantID: "acme", PoolRef: "pool-a",
	})
	if len(obs.values) != 1 || obs.values[0] != 5 {
		t.Fatalf("observed values = %v, want [5]", obs.values)
	}
	if obs.pools[0] != "pool-a" || obs.tenants[0] != "acme" {
		t.Errorf("labels = (%q, %q), want (pool-a, acme)", obs.pools[0], obs.tenants[0])
	}
	if len(reader.calledWith) != 1 || reader.calledWith[0] != "root1" {
		t.Errorf("reader called with %v, want [root1]", reader.calledWith)
	}
}

// spec: §8.3 line 379 — a delegated child (one with a parent) settling
// is not the tree apex, so its terminal transition does not sample the
// histogram. The observation fires once per tree at the root, not per
// settling child. F-8.9.6.
func TestObserveTreeHighWatermarkSkipsNonRoot_spec_8_3_379(t *testing.T) {
	reader := &fakeHWMReader{value: 5, found: true}
	obs := &fakeHWMObserver{}
	s := newHWMServer(reader, obs)
	s.observeTreeHighWatermark(context.Background(), sessionstore.Session{
		ID: "child1", TenantID: "acme", ParentSessionID: "root1", RootSessionID: "root1",
	})
	if len(obs.values) != 0 || len(reader.calledWith) != 0 {
		t.Fatalf("non-root settle observed %v / read %v, want none", obs.values, reader.calledWith)
	}
}

// spec: §8.3 line 379 — a tree that admitted no delegation has no
// recorded watermark; ObserveHighWatermark returns found=false and the
// histogram is not sampled. F-8.9.6.
func TestObserveTreeHighWatermarkSkipsWhenAbsent_spec_8_3_379(t *testing.T) {
	reader := &fakeHWMReader{found: false}
	obs := &fakeHWMObserver{}
	s := newHWMServer(reader, obs)
	s.observeTreeHighWatermark(context.Background(), sessionstore.Session{ID: "root1", TenantID: "acme"})
	if len(obs.values) != 0 {
		t.Fatalf("observed %v for an undelegated tree, want none", obs.values)
	}
}

// spec: §8.3 line 379 — a read error is swallowed (best-effort) and
// never samples the histogram, so a Redis outage cannot corrupt the
// metric or fail the terminal transition. F-8.9.6.
func TestObserveTreeHighWatermarkSwallowsReadError_spec_8_3_379(t *testing.T) {
	reader := &fakeHWMReader{err: errors.New("redis down")}
	obs := &fakeHWMObserver{}
	s := newHWMServer(reader, obs)
	s.observeTreeHighWatermark(context.Background(), sessionstore.Session{ID: "root1", TenantID: "acme"})
	if len(obs.values) != 0 {
		t.Fatalf("observed %v after a read error, want none", obs.values)
	}
}

// spec: §8.3 line 379 — with no reader/observer wired (developer mode
// without Redis) the path is a no-op. F-8.9.6.
func TestObserveTreeHighWatermarkNoopWithoutDeps_spec_8_3_379(t *testing.T) {
	newHWMServer(nil, nil).observeTreeHighWatermark(context.Background(), sessionstore.Session{ID: "root1"})
}
