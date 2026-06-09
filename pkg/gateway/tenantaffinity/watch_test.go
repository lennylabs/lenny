// SPDX-License-Identifier: MIT

package tenantaffinity

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeLister returns a scripted sequence of snapshots, then an error on
// every subsequent call so the test can drive the retain-on-error path.
type fakeLister struct {
	mu        sync.Mutex
	snapshots [][]Endpoint
	calls     int
	err       error
}

func (f *fakeLister) ListEndpoints(context.Context) ([]Endpoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	f.calls++
	if i < len(f.snapshots) {
		return f.snapshots[i], nil
	}
	if f.err != nil {
		return nil, f.err
	}
	if len(f.snapshots) == 0 {
		return nil, nil
	}
	return f.snapshots[len(f.snapshots)-1], nil
}

// spec: §5.2 line 500 — the poller feeds the EndpointSlice snapshot into
// Router.UpdateEndpoints so Route sees the live pod-IP/readiness set.
func TestEndpointPollerFeedsRouterFirstListImmediate(t *testing.T) {
	r := New("acme-stateless", nil)
	lister := &fakeLister{snapshots: [][]Endpoint{
		{{PodIP: "10.0.0.1", Ready: true}, {PodIP: "10.0.0.2", Ready: true}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &EndpointPoller{Lister: lister, Router: r, Interval: time.Hour}
	go p.Run(ctx)

	// The first poll fires immediately; wait for the router to carry the
	// endpoints rather than one full (1h) interval.
	waitFor(t, func() bool {
		d, err := r.Route("acme")
		if err != nil {
			return false
		}
		r.Release()
		return d.PodIP == "10.0.0.1"
	})
}

// spec: §5.2 line 500 — a failed list retains the last-known endpoint
// set rather than draining the router (a transient API blip must not
// collapse routing).
func TestEndpointPollerRetainsSnapshotOnListError(t *testing.T) {
	r := New("acme-stateless", nil)
	lister := &fakeLister{
		snapshots: [][]Endpoint{{{PodIP: "10.0.0.1", Ready: true}}},
		err:       errors.New("apiserver unavailable"),
	}
	// Seed the router directly with the first snapshot, then let the
	// poller's subsequent (error) polls run; the snapshot must survive.
	r.UpdateEndpoints(lister.snapshots[0])

	var logged int
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &EndpointPoller{
		Lister:   &fakeLister{err: errors.New("apiserver unavailable")},
		Router:   r,
		Interval: 5 * time.Millisecond,
		Logf:     func(string, ...any) { mu.Lock(); logged++; mu.Unlock() },
	}
	go p.Run(ctx)

	// Give the poller several error polls.
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return logged >= 2 })

	d, err := r.Route("acme")
	if err != nil {
		t.Fatalf("Route after error polls = %v, want pod retained", err)
	}
	if d.PodIP != "10.0.0.1" {
		t.Fatalf("Route = %q, want retained 10.0.0.1", d.PodIP)
	}
	r.Release()
}

// spec: §5.2 line 500 — a readiness flip on a re-list removes a pod at
// slot capacity from the route-target set.
func TestEndpointPollerReflectsReadinessFlip(t *testing.T) {
	r := New("acme-stateless", nil)
	lister := &fakeLister{snapshots: [][]Endpoint{
		{{PodIP: "10.0.0.1", Ready: true}},
		{{PodIP: "10.0.0.1", Ready: false}}, // pod hit slot capacity
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &EndpointPoller{Lister: lister, Router: r, Interval: 5 * time.Millisecond}
	go p.Run(ctx)

	waitFor(t, func() bool {
		_, err := r.Route("acme")
		if err == nil {
			r.Release()
		}
		return errors.Is(err, ErrNoAvailablePod)
	})
}

// TestEndpointPollerNoOpWithoutSeams asserts a poller missing a required
// seam returns immediately rather than panicking.
func TestEndpointPollerNoOpWithoutSeams(t *testing.T) {
	(&EndpointPoller{}).Run(context.Background())
	(&EndpointPoller{Lister: &fakeLister{}}).Run(context.Background())
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}
