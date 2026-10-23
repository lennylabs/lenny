// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// TestSessionMessageQueuesEraserRegistration_spec_12_8 pins that the user
// erasure deletes a session's DLQ and durable inbox exactly when messaging is
// wired, and that the eraser runs before the session tree archive in the
// canonical erasure order.
//
// spec: 12.8 (Compliance Interfaces)
func TestSessionMessageQueuesEraserRegistration_spec_12_8(t *testing.T) {
	queuesEntry := func(w *gatewayWiring) (erasure.SessionEraser, int, int) {
		queues, archive := -1, -1
		var entry erasure.SessionEraser
		for i, se := range w.buildSessionScopedErasers() {
			switch se.Name {
			case "session_message_queues":
				queues, entry = i, se
			case "session_tree_archive":
				archive = i
			}
		}
		return entry, queues, archive
	}
	base := func() *gatewayWiring {
		w := &gatewayWiring{}
		w.evals = evalstore.NewMemory(0, time.Now)
		w.treeArchive = treearchive.NewMemory()
		return w
	}
	if entry, _, _ := queuesEntry(base()); entry.DeleteBySession != nil {
		t.Fatal("the message queue eraser has a function with no messaging wired")
	}
	w := base()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	w.messagingCoord = sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: sessioninbox.NewMemoryInbox(1),
		DLQ:   sessioninbox.NewDLQ(rc, 1),
	})
	entry, queues, archive := queuesEntry(w)
	if entry.DeleteBySession == nil {
		t.Fatal("the message queue eraser has no function with messaging wired")
	}
	if queues < 0 || archive < 0 || queues > archive {
		t.Fatalf("session_message_queues at %d, session_tree_archive at %d, want the queues first", queues, archive)
	}
	w.sessions = memstore.New()
	w.interactions = interactionstore.NewMemory()
	cfg := erasure.Config{SessionScoped: w.buildSessionScopedErasers(), UserScoped: w.buildUserErasers(nil, nil, nil, nil)}
	if err := erasure.ValidateOrder(cfg); err != nil {
		t.Fatalf("ValidateOrder: %v", err)
	}
}

// TestTenantDeletionErasesSessionMessageQueues_spec_12_8 runs the Phase 4
// list of a gateway with messaging wired for acme and asserts each of
// acme's sessions has its DLQ deleted while globex's DLQ survives. Before
// the tenant list carried a session_message_queues entry, a deleted
// tenant's held messages stayed in Redis.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle)
func TestTenantDeletionErasesSessionMessageQueues_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := inMemoryEraserWiring()
	seedTenants(t, w.tenants, "acme", "globex")
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	dlq := sessioninbox.NewDLQ(rc, 10)
	w.messagingCoord = sessioninbox.NewCoordinator(sessioninbox.Config{Inbox: sessioninbox.NewMemoryInbox(10), DLQ: dlq})
	now := time.Now()
	for _, s := range []sessionstore.Session{
		{ID: "s-acme-1", TenantID: "acme", State: session.StateSuspended, CreatedAt: now, UpdatedAt: now},
		{ID: "s-acme-2", TenantID: "acme", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "s-globex", TenantID: "globex", State: session.StateSuspended, CreatedAt: now, UpdatedAt: now},
	} {
		if err := w.sessions.Create(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", s.ID, err)
		}
		if _, err := dlq.Enqueue(ctx, s.TenantID, s.ID, sessioninbox.Message{MessageID: "m-" + s.ID, Payload: []byte("held"), EnqueuedAt: now}, time.Hour); err != nil {
			t.Fatalf("enqueue %s: %v", s.ID, err)
		}
	}
	phase4 := &tenantEraser{erasers: w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil)}
	counts, err := phase4.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("Phase 4: %v", err)
	}
	for _, sid := range []string{"s-acme-1", "s-acme-2"} {
		if n, err := dlq.Len(ctx, "acme", sid); err != nil || n != 0 {
			t.Errorf("acme %s DLQ length = %d (%v), want 0", sid, n, err)
		}
	}
	if n, err := dlq.Len(ctx, "globex", "s-globex"); err != nil || n != 1 {
		t.Errorf("globex DLQ length = %d (%v), want 1", n, err)
	}
	if counts["session_message_queues"] == 0 {
		t.Errorf("receipt counts %v record no message queue deletions", counts)
	}
}
