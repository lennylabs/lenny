// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fakeSticky is an in-memory StickyCache that records calls so the wrapper's
// read-through / write-back behavior can be asserted without Redis.
type fakeSticky struct {
	store   map[string]string
	getErr  error
	putErr  error
	puts    int
	getKeys []string
}

func newFakeSticky() *fakeSticky { return &fakeSticky{store: map[string]string{}} }

func skey(tenant, exp, user string) string { return tenant + "|" + exp + "|" + user }

func (f *fakeSticky) Get(_ context.Context, tenant, exp, user string) (string, bool, error) {
	f.getKeys = append(f.getKeys, skey(tenant, exp, user))
	if f.getErr != nil {
		return "", false, f.getErr
	}
	v, ok := f.store[skey(tenant, exp, user)]
	return v, ok, nil
}

func (f *fakeSticky) Put(_ context.Context, tenant, exp, user, variant string) error {
	f.puts++
	if f.putErr != nil {
		return f.putErr
	}
	f.store[skey(tenant, exp, user)] = variant
	return nil
}

func externalStickyCandidate(id string) experimentstore.Experiment {
	return experimentstore.Experiment{
		ID:            id,
		TargetingMode: experiment.TargetingExternal,
		Sticky:        experiment.StickyUser,
	}
}

func stickyTestSession() *sessionstore.Session {
	return &sessionstore.Session{ID: "sess-1", TenantID: "acme", UserID: "alice"}
}

// spec: §10.7 line 831 — on a cache hit the OpenFeature provider (inner
// evaluator) is not called.
func TestStickyWrapper_CacheHitSkipsProvider_spec_10_7(t *testing.T) {
	cache := newFakeSticky()
	cache.store[skey("acme", "exp-1", "alice")] = "variant-b"
	s := &Server{stickyCache: cache}
	row := stickyTestSession()
	innerCalls := 0
	inner := func(string) (string, bool) { innerCalls++; return "variant-x", true }

	wrapped := s.stickyWrappedEvaluator(context.Background(), row,
		[]experimentstore.Experiment{externalStickyCandidate("exp-1")}, inner)
	v, ok := wrapped("exp-1")
	if !ok || v != "variant-b" {
		t.Fatalf("wrapped = (%q,%v), want (variant-b,true)", v, ok)
	}
	if innerCalls != 0 {
		t.Fatalf("provider called %d times on a cache hit, want 0", innerCalls)
	}
}

// spec: §10.7 line 831 — on a cache miss the provider is consulted and the
// result is written back so the next session is a hit.
func TestStickyWrapper_MissEvaluatesAndWritesBack_spec_10_7(t *testing.T) {
	cache := newFakeSticky()
	s := &Server{stickyCache: cache}
	row := stickyTestSession()
	inner := func(string) (string, bool) { return "variant-b", true }

	wrapped := s.stickyWrappedEvaluator(context.Background(), row,
		[]experimentstore.Experiment{externalStickyCandidate("exp-1")}, inner)
	v, ok := wrapped("exp-1")
	if !ok || v != "variant-b" {
		t.Fatalf("wrapped = (%q,%v), want (variant-b,true)", v, ok)
	}
	if cache.puts != 1 {
		t.Fatalf("write-backs = %d, want 1", cache.puts)
	}
	if got := cache.store[skey("acme", "exp-1", "alice")]; got != "variant-b" {
		t.Fatalf("cached = %q, want variant-b", got)
	}
}

// A provider result of no-enrollment (ok=false) is not written back: caching
// a failure would suppress a later successful evaluation.
func TestStickyWrapper_NoEnrollmentNotCached(t *testing.T) {
	cache := newFakeSticky()
	s := &Server{stickyCache: cache}
	inner := func(string) (string, bool) { return "", false }
	wrapped := s.stickyWrappedEvaluator(context.Background(), stickyTestSession(),
		[]experimentstore.Experiment{externalStickyCandidate("exp-1")}, inner)
	if _, ok := wrapped("exp-1"); ok {
		t.Fatal("expected no enrollment")
	}
	if cache.puts != 0 {
		t.Fatalf("write-backs = %d, want 0 (failure must not be cached)", cache.puts)
	}
}

// spec: §12.4 failure behavior — a Redis Get error falls open to fresh
// evaluation rather than failing the session.
func TestStickyWrapper_GetErrorFallsOpen_spec_12_4(t *testing.T) {
	cache := newFakeSticky()
	cache.getErr = errors.New("redis down")
	s := &Server{stickyCache: cache}
	inner := func(string) (string, bool) { return "variant-b", true }
	wrapped := s.stickyWrappedEvaluator(context.Background(), stickyTestSession(),
		[]experimentstore.Experiment{externalStickyCandidate("exp-1")}, inner)
	v, ok := wrapped("exp-1")
	if !ok || v != "variant-b" {
		t.Fatalf("fail-open wrapped = (%q,%v), want (variant-b,true)", v, ok)
	}
}

// Only `mode: external` + `sticky: user` experiments are cached. A
// percentage-mode or non-user-sticky experiment passes straight to inner and
// is never read from or written to the cache.
func TestStickyWrapper_OnlyExternalStickyUser(t *testing.T) {
	cache := newFakeSticky()
	s := &Server{stickyCache: cache}
	innerCalls := 0
	inner := func(string) (string, bool) { innerCalls++; return "variant-b", true }

	candidates := []experimentstore.Experiment{
		{ID: "pct", TargetingMode: experiment.TargetingPercentage, Sticky: experiment.StickyUser},
		{ID: "ext-session", TargetingMode: experiment.TargetingExternal, Sticky: experiment.StickySession},
	}
	wrapped := s.stickyWrappedEvaluator(context.Background(), stickyTestSession(), candidates, inner)
	_, _ = wrapped("ext-session")
	if len(cache.getKeys) != 0 {
		t.Fatalf("cache read for a non-eligible experiment: %v", cache.getKeys)
	}
	if cache.puts != 0 {
		t.Fatalf("cache write for a non-eligible experiment: %d", cache.puts)
	}
	if innerCalls != 1 {
		t.Fatalf("inner calls = %d, want 1", innerCalls)
	}
}

// When no sticky cache is wired the wrapper returns inner unchanged so the
// router re-evaluates every experiment fresh (the §12.4 fail-open posture).
func TestStickyWrapper_NilCacheReturnsInner(t *testing.T) {
	s := &Server{}
	inner := func(string) (string, bool) { return "variant-b", true }
	wrapped := s.stickyWrappedEvaluator(context.Background(), stickyTestSession(),
		[]experimentstore.Experiment{externalStickyCandidate("exp-1")}, inner)
	if v, ok := wrapped("exp-1"); !ok || v != "variant-b" {
		t.Fatalf("nil-cache wrapped = (%q,%v), want (variant-b,true)", v, ok)
	}
}
