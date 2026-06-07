// SPDX-License-Identifier: MIT

package tenantaffinity

import (
	"sync"
	"testing"
)

// fakeMetrics records the §5.2 line 573 demand-signal calls so tests can
// assert the router emits lenny_stateless_requests_total and
// lenny_stateless_concurrent_active.
type fakeMetrics struct {
	mu       sync.Mutex
	requests map[string]int
	active   map[string]float64
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{requests: map[string]int{}, active: map[string]float64{}}
}

func (f *fakeMetrics) IncStatelessRequest(pool string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests[pool]++
}

func (f *fakeMetrics) SetStatelessConcurrentActive(pool string, value float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active[pool] = value
}

func ready(ips ...string) []Endpoint {
	out := make([]Endpoint, len(ips))
	for i, ip := range ips {
		out[i] = Endpoint{PodIP: ip, Ready: true}
	}
	return out
}

// spec: §5.2 line 500 — first request for a tenant pins an unpinned pod
// and records the tenantId → pod IP mapping.
func TestRouteFirstRequestPinsUnpinnedPod(t *testing.T) {
	r := New("claude-stateless", newFakeMetrics())
	r.UpdateEndpoints(ready("10.0.0.1", "10.0.0.2"))

	d, err := r.Route("acme")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !d.NewlyPinned {
		t.Errorf("first request should pin a fresh pod; NewlyPinned=false")
	}
	if d.PodIP != "10.0.0.1" {
		t.Errorf("PodIP = %q, want deterministic lowest 10.0.0.1", d.PodIP)
	}
	if got := r.PinnedPods("acme"); len(got) != 1 || got[0] != "10.0.0.1" {
		t.Errorf("PinnedPods(acme) = %v, want [10.0.0.1]", got)
	}
}

// spec: §5.2 line 500 — subsequent requests for the same tenant route to
// the already-pinned pod without pinning a new one.
func TestRouteSubsequentRequestReusesPin(t *testing.T) {
	r := New("p", nil)
	r.UpdateEndpoints(ready("10.0.0.1", "10.0.0.2"))

	first, _ := r.Route("acme")
	second, err := r.Route("acme")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if second.NewlyPinned {
		t.Errorf("second request must reuse the pin; NewlyPinned=true")
	}
	if second.PodIP != first.PodIP {
		t.Errorf("second PodIP = %q, want pinned %q", second.PodIP, first.PodIP)
	}
	if got := r.PinnedPods("acme"); len(got) != 1 {
		t.Errorf("tenant should still have exactly one pinned pod, got %v", got)
	}
}

// spec: §5.2 line 500 — when every pinned pod for a tenant is at slot
// capacity (readiness false), the router pins a new unpinned pod.
func TestRoutePinsNewPodWhenPinnedPodSaturated(t *testing.T) {
	r := New("p", nil)
	r.UpdateEndpoints(ready("10.0.0.1", "10.0.0.2"))

	first, _ := r.Route("acme") // pins 10.0.0.1
	// 10.0.0.1 goes to capacity (readiness false); 10.0.0.2 still ready.
	r.UpdateEndpoints([]Endpoint{
		{PodIP: "10.0.0.1", Ready: false},
		{PodIP: "10.0.0.2", Ready: true},
	})

	d, err := r.Route("acme")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !d.NewlyPinned || d.PodIP != "10.0.0.2" {
		t.Errorf("expected a fresh pin of 10.0.0.2, got %+v", d)
	}
	if got := r.PinnedPods("acme"); len(got) != 2 {
		t.Errorf("tenant should now have two pinned pods, got %v", got)
	}
	_ = first
}

// spec: §5.2 line 500 — no ready pod for the tenant and no unpinned ready
// pod yields ErrNoAvailablePod.
func TestRouteExhaustedReturnsError(t *testing.T) {
	r := New("p", nil)
	r.UpdateEndpoints([]Endpoint{{PodIP: "10.0.0.1", Ready: false}})
	if _, err := r.Route("acme"); err != ErrNoAvailablePod {
		t.Fatalf("err = %v, want ErrNoAvailablePod", err)
	}

	empty := New("p", nil)
	if _, err := empty.Route("acme"); err != ErrNoAvailablePod {
		t.Fatalf("empty endpoints: err = %v, want ErrNoAvailablePod", err)
	}
}

// spec: §5.2 line 502 — a request can never route to a pod pinned to a
// different tenant; with only foreign-pinned pods left the route is
// exhausted rather than crossing tenants.
func TestRouteNeverCrossesTenant(t *testing.T) {
	r := New("p", nil)
	r.UpdateEndpoints(ready("10.0.0.1"))

	if _, err := r.Route("acme"); err != nil { // acme pins the only pod
		t.Fatalf("Route(acme): %v", err)
	}
	if _, err := r.Route("globex"); err != ErrNoAvailablePod {
		t.Fatalf("globex err = %v, want ErrNoAvailablePod (no cross-tenant routing)", err)
	}
	if got := r.PinnedPods("globex"); len(got) != 0 {
		t.Errorf("globex must have no pinned pods, got %v", got)
	}
}

// spec: §5.2 line 502 — CheckTenant rejects a mismatched tenant on an
// already-pinned pod and admits the owning tenant or an unpinned pod.
func TestCheckTenantEnforcesIsolation(t *testing.T) {
	r := New("p", nil)
	r.UpdateEndpoints(ready("10.0.0.1"))
	d, _ := r.Route("acme")

	if err := r.CheckTenant(d.PodIP, "globex"); err != ErrTenantMismatch {
		t.Errorf("CheckTenant(foreign) = %v, want ErrTenantMismatch", err)
	}
	if err := r.CheckTenant(d.PodIP, "acme"); err != nil {
		t.Errorf("CheckTenant(owner) = %v, want nil", err)
	}
	if err := r.CheckTenant("10.0.0.9", "acme"); err != nil {
		t.Errorf("CheckTenant(unpinned) = %v, want nil", err)
	}
}

// spec: §5.2 line 500 — a pod removed from the EndpointSlice snapshot is
// evicted and unpinned, freeing the tenant's set.
func TestUpdateEndpointsEvictsDeletedPod(t *testing.T) {
	r := New("p", nil)
	r.UpdateEndpoints(ready("10.0.0.1", "10.0.0.2"))
	r.Route("acme") // pins 10.0.0.1

	r.UpdateEndpoints(ready("10.0.0.2")) // 10.0.0.1 deleted
	if got := r.PinnedPods("acme"); len(got) != 0 {
		t.Errorf("deleted pod must be unpinned, got %v", got)
	}
	// The freed tenant can now pin the surviving pod.
	d, err := r.Route("acme")
	if err != nil || d.PodIP != "10.0.0.2" {
		t.Fatalf("re-route after eviction: %+v err=%v", d, err)
	}
}

// spec: §5.2 line 573 — Route increments the request counter (including
// for unservable arrivals) and Route/Release maintain the concurrent
// active gauge.
func TestMetricsEmission(t *testing.T) {
	m := newFakeMetrics()
	r := New("poolX", m)
	r.UpdateEndpoints(ready("10.0.0.1", "10.0.0.2"))

	r.Route("acme")
	r.Route("acme")
	if m.requests["poolX"] != 2 {
		t.Errorf("requests = %d, want 2", m.requests["poolX"])
	}
	if m.active["poolX"] != 2 {
		t.Errorf("active gauge = %v, want 2", m.active["poolX"])
	}

	r.Release()
	if m.active["poolX"] != 1 {
		t.Errorf("active gauge after release = %v, want 1", m.active["poolX"])
	}

	// An unservable arrival still counts as demand.
	r.UpdateEndpoints(nil)
	if _, err := r.Route("globex"); err != ErrNoAvailablePod {
		t.Fatalf("err = %v, want ErrNoAvailablePod", err)
	}
	if m.requests["poolX"] != 3 {
		t.Errorf("requests = %d, want 3 (demand counts unservable arrivals)", m.requests["poolX"])
	}
}

// Release must not drive the active count below zero on an over-release.
func TestReleaseFloorsAtZero(t *testing.T) {
	r := New("p", nil)
	r.Release()
	if got := r.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount after over-release = %d, want 0", got)
	}
}

// Two tenants pin distinct pods and never collide.
func TestRouteIsolatesTenants(t *testing.T) {
	r := New("p", nil)
	r.UpdateEndpoints(ready("10.0.0.1", "10.0.0.2"))

	a, _ := r.Route("acme")
	b, _ := r.Route("globex")
	if a.PodIP == b.PodIP {
		t.Errorf("tenants share pod %q; pins must be disjoint", a.PodIP)
	}
}

// The router is safe under concurrent Route/Release/UpdateEndpoints
// (run with -race).
func TestConcurrentAccess(t *testing.T) {
	r := New("p", newFakeMetrics())
	r.UpdateEndpoints(ready("10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Route("acme"); err == nil {
				r.Release()
			}
		}()
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.UpdateEndpoints(ready("10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"))
		}()
	}
	wg.Wait()
	if got := r.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount after balanced Route/Release = %d, want 0", got)
	}
}
