// SPDX-License-Identifier: MIT

package statelessslot

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// spec: §5.2 line 500 — the gate admits up to maxConcurrent slots and
// reports Ready while a slot is free.
func TestGateAdmitsUpToMaxThenNotReady(t *testing.T) {
	g := NewGate(2)
	if !g.Ready() {
		t.Fatal("fresh gate should be Ready")
	}
	if !g.Acquire() || !g.Acquire() {
		t.Fatal("first two Acquire should succeed")
	}
	if g.Ready() {
		t.Fatal("gate at capacity should report NotReady")
	}
	if g.Acquire() {
		t.Fatal("Acquire past capacity should fail")
	}
	g.Release()
	if !g.Ready() {
		t.Fatal("gate should be Ready again after a Release")
	}
	if g.Active() != 1 {
		t.Fatalf("Active = %d, want 1", g.Active())
	}
}

// spec: §5.2 line 500 — the readiness probe returns 503 at slot capacity
// so the pod drops out of the EndpointSlice.
func TestReadyHandlerReflectsCapacity(t *testing.T) {
	g := NewGate(1)
	rec := httptest.NewRecorder()
	g.ReadyHandler()(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready probe with free slot = %d, want 200", rec.Code)
	}
	g.Acquire()
	rec = httptest.NewRecorder()
	g.ReadyHandler()(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready probe at capacity = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("NotReady probe should set Retry-After")
	}
}

// Serve occupies a slot for the request duration and rejects an
// over-capacity request with 503 rather than admitting it past the cap.
func TestServeOccupiesSlotAndRejectsOverflow(t *testing.T) {
	g := NewGate(1)
	release := make(chan struct{})
	entered := make(chan struct{})
	h := g.Serve(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	// First request enters and holds the only slot.
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-entered
	if g.Active() != 1 {
		t.Fatalf("Active during in-flight request = %d, want 1", g.Active())
	}

	// Second concurrent request finds the slot full → 503.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("overflow request = %d, want 503", rec.Code)
	}

	close(release)
}

// NewGate clamps a non-positive maxConcurrent to 1 so a misconfigured
// pod serializes rather than admitting unbounded concurrency.
func TestNewGateClampsNonPositiveMax(t *testing.T) {
	g := NewGate(0)
	if g.Max() != 1 {
		t.Fatalf("Max for NewGate(0) = %d, want 1", g.Max())
	}
}

// Release floors at zero so a double Release cannot drive Active negative
// (which would falsely report capacity).
func TestReleaseFloorsAtZero(t *testing.T) {
	g := NewGate(2)
	g.Acquire()
	g.Release()
	g.Release() // extra
	if g.Active() != 0 {
		t.Fatalf("Active after double Release = %d, want 0", g.Active())
	}
}

// The gate is safe under concurrent Acquire/Release and never exceeds the
// cap.
func TestGateConcurrencySafety(t *testing.T) {
	g := NewGate(4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	maxSeen := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.Acquire() {
				mu.Lock()
				if a := g.Active(); a > maxSeen {
					maxSeen = a
				}
				mu.Unlock()
				g.Release()
			}
		}()
	}
	wg.Wait()
	if maxSeen > 4 {
		t.Fatalf("observed Active = %d, want <= 4 (cap never exceeded)", maxSeen)
	}
	if g.Active() != 0 {
		t.Fatalf("Active after all done = %d, want 0", g.Active())
	}
}
