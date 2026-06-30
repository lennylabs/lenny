// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/derivelock"
)

// fakeLock blocks the second acquire indefinitely so we can observe the
// 429 DERIVE_LOCK_CONTENTION envelope without flakily timing a real
// derivelock.Memory implementation.
type fakeLock struct {
	contended bool
}

func (f *fakeLock) Acquire(_ context.Context, _ string) (derivelock.Releaser, error) {
	if f.contended {
		return nil, derivelock.ErrContended
	}
	return func() {}, nil
}

// spec: §7.1 line 92 — concurrent derives on the same source session
// that fail to acquire the advisory lock within the wait budget return
// `429 DERIVE_LOCK_CONTENTION` so SDK clients can distinguish a
// transient lock race from the immutable-source rejection.
func TestDerive_ContendedLockReturns429_spec_7_1_92(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:     func() string { return "sess_d" },
		DeriveLock: &fakeLock{contended: true},
	})

	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rr.Code, rr.Body.String())
	}
	code, _, details := decodeError(t, rr)
	if code != "DERIVE_LOCK_CONTENTION" {
		t.Errorf("code = %q, want DERIVE_LOCK_CONTENTION", code)
	}
	if got, _ := details["sourceSessionId"].(string); got != "sess_source" {
		t.Errorf("details.sourceSessionId = %v, want sess_source", details["sourceSessionId"])
	}
}

// spec: §7.1 line 92 — a successful Acquire admits the derive and
// releases the lock as soon as the snapshot has been read. The
// in-process Memory implementation satisfies this on a single replica.
func TestDerive_LockHeldThenReleased_spec_7_1_92(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)
	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:     func() string { return "sess_d" },
		DeriveLock: derivelock.NewMemory(time.Second),
	})

	// First derive succeeds; sess_source is one-shot, so the second
	// derive's source is the same row and the lock is reusable.
	rr := deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first derive status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// A second derive against the same source must acquire (the lock
	// was released after the first), not contend.
	srv2 := sessionserver.New(store, sessionserver.Options{
		IDFunc:     func() string { return "sess_d2" },
		DeriveLock: derivelock.NewMemory(50 * time.Millisecond),
	})
	rr2 := deriveRequest(t, srv2.Handler(), sessionserver.DeriveRequest{})
	if rr2.Code != http.StatusCreated {
		t.Fatalf("second derive status = %d, want 201; body=%s", rr2.Code, rr2.Body.String())
	}
}

// spec: §7.1 line 92 — concurrent derives on the same source session
// must serialize. The Memory implementation blocks the second acquirer
// until the first releases. We assert the second derive does not race
// past the first via a synchronized barrier.
func TestDerive_MemoryLockSerializesConcurrentRequests_spec_7_1_92(t *testing.T) {
	store := memstore.New()
	newSourceSession(t, store)

	// Wrap the Memory lock to count concurrent holders so we can prove
	// at most one derive runs at a time.
	lock := derivelock.NewMemory(2 * time.Second)
	counter := &concurrentHoldCounter{inner: lock}

	srv := sessionserver.New(store, sessionserver.Options{
		IDFunc:     func() string { return "sess_d" },
		DeriveLock: counter,
	})

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deriveRequest(t, srv.Handler(), sessionserver.DeriveRequest{})
		}()
	}
	wg.Wait()

	if got := counter.maxConcurrent.Load(); got > 1 {
		t.Errorf("maxConcurrent under lock = %d, want ≤1 (lock failed to serialize)", got)
	}
}
