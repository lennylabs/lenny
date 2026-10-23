// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// heldSeamExecutor records each Send and each Release.
type heldSeamExecutor struct {
	mu       sync.Mutex
	sent     []string
	released []string
}

func (e *heldSeamExecutor) Send(_ context.Context, id string, _ []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sent = append(e.sent, id)
	return executor.Response{}, nil
}

func (e *heldSeamExecutor) Close(ctx context.Context, id string) error { return e.Release(ctx, id, "") }

func (e *heldSeamExecutor) Release(_ context.Context, id string, d executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released = append(e.released, id+":"+string(d))
	return nil
}

func (e *heldSeamExecutor) sends() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.sent)
}

// TestCoordinationOptionsCarryTheSessionServerSeams_spec_10_1_2 asserts that
// the coordination options the gateway builds carry a non-nil HeldDeliverer
// and TerminalReleaser, that a call before the session server is set does
// nothing and returns at once, and that once it is set the held deliverer
// reaches DeliverHeldAfterTakeover with the passed arguments, delivering a
// held message to a running session at the passed generation.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §10.1.2
// (Coordinator Handoff Protocol), §10.1.1 (Stateless Replicas and Per-Session
// Coordination)
func TestCoordinationOptionsCarryTheSessionServerSeams_spec_10_1_2(t *testing.T) {
	w := &gatewayWiring{}
	opts := coordinationOptions(w, "rep-1", time.Second, nil)
	if opts.HeldDeliverer == nil || opts.TerminalReleaser == nil {
		t.Fatalf("options = %+v, want both seams wired", opts)
	}

	done := make(chan struct{})
	go func() {
		opts.HeldDeliverer.DeliverHeldAfterTakeover("acme", "s1", 1)
		opts.TerminalReleaser.ReleaseTerminalBinding(sessionstore.Session{TenantID: "acme", ID: "s1", State: session.StateCancelled})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a seam call before the session server is set blocked")
	}

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: sessioninbox.NewMemoryInbox(10), DLQ: sessioninbox.NewDLQ(rc, 10),
	})
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning, CoordinationGeneration: 4,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	payload, _ := json.Marshal(sessionserver.MessagePayload{ID: "m1", Role: "user", Content: sessionrecord.MessageContentFromText("hello")})
	if _, err := coord.EnqueueDLQ(context.Background(), "acme", "s1", sessioninbox.Message{
		MessageID: "m1", Payload: payload, EnqueuedAt: time.Now(),
	}, time.Hour); err != nil {
		t.Fatalf("hold: %v", err)
	}
	exec := &heldSeamExecutor{}
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true, Executor: exec, Messaging: coord})
	t.Cleanup(srv.Close)
	w.sessionSrv = srv

	opts.HeldDeliverer.DeliverHeldAfterTakeover("acme", "s1", 3)
	time.Sleep(50 * time.Millisecond)
	if n := exec.sends(); n != 0 {
		t.Fatalf("sends = %d after a takeover at another generation, want 0", n)
	}
	opts.HeldDeliverer.DeliverHeldAfterTakeover("acme", "s1", 4)
	deadline := time.Now().Add(5 * time.Second)
	for exec.sends() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("sends = %d, want the held message delivered once", exec.sends())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
