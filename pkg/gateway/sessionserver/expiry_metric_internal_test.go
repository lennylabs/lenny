// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §16.1 (lenny_session_expiry_total{pool, reason}); §16.1.1 (reason
// vocabulary). OnSessionExpired is the watchdog's platform-expiry-clock hook;
// it forwards the session's PoolRef and the watchdog-resolved reason to the
// IncSessionExpiry counter callback. F-11.3.7.

type recordedExpiry struct {
	mu    sync.Mutex
	calls [][2]string // {pool, reason}
}

func (r *recordedExpiry) record(pool, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]string{pool, reason})
}

func TestOnSessionExpiredEmitsCounter_F_11_3_7(t *testing.T) {
	counter := &recordedExpiry{}
	srv := New(memstore.New(), Options{IncSessionExpiry: counter.record})

	srv.OnSessionExpired(context.Background(), sessionstore.Session{
		ID: "sess_idle", TenantID: "acme", PoolRef: "pool-a",
		State: session.StateExpired,
	}, "max_idle_time")
	srv.OnSessionExpired(context.Background(), sessionstore.Session{
		ID: "sess_age", TenantID: "acme", PoolRef: "pool-b",
		State: session.StateExpired,
	}, "max_session_age")

	want := [][2]string{
		{"pool-a", "max_idle_time"},
		{"pool-b", "max_session_age"},
	}
	if len(counter.calls) != len(want) {
		t.Fatalf("counter calls = %v, want %v", counter.calls, want)
	}
	for i, w := range want {
		if counter.calls[i] != w {
			t.Errorf("call %d = %v, want %v", i, counter.calls[i], w)
		}
	}
}

// A nil IncSessionExpiry callback degrades to a no-op so the watchdog can wire
// the terminal hook unconditionally.
func TestOnSessionExpiredWithoutCounterIsNoOp_F_11_3_7(t *testing.T) {
	srv := New(memstore.New(), Options{})
	srv.OnSessionExpired(context.Background(), sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateExpired,
	}, "max_idle_time")
}
