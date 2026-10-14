// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
)

// TestSessionMessageQueuesEraserRegistration_spec_12_8 pins that the user
// erasure deletes a session's DLQ and durable inbox exactly when messaging is
// wired, and that the eraser runs before the session tree archive in the
// canonical erasure order.
//
// spec: 12.8 (Compliance Interfaces)
func TestSessionMessageQueuesEraserRegistration_spec_12_8(t *testing.T) {
	names := func(w *gatewayWiring) []string {
		var out []string
		for _, se := range w.buildSessionScopedErasers() {
			out = append(out, se.Name)
		}
		return out
	}
	base := func() *gatewayWiring {
		w := &gatewayWiring{}
		w.evals = evalstore.NewMemory(0, time.Now)
		w.treeArchive = treearchive.NewMemory()
		return w
	}
	without := names(base())
	for _, n := range without {
		if n == "session_message_queues" {
			t.Fatalf("erasers %v register the message queues with no messaging", without)
		}
	}
	w := base()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	w.messagingCoord = sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: sessioninbox.NewMemoryInbox(1),
		DLQ:   sessioninbox.NewDLQ(rc, 1),
	})
	with := names(w)
	queues, archive := -1, -1
	for i, n := range with {
		switch n {
		case "session_message_queues":
			queues = i
		case "session_tree_archive":
			archive = i
		}
	}
	if queues < 0 || archive < 0 || queues > archive {
		t.Fatalf("erasers %v, want session_message_queues before session_tree_archive", with)
	}
	if err := erasure.ValidateOrder(erasure.Config{SessionScoped: w.buildSessionScopedErasers()}); err != nil {
		t.Fatalf("ValidateOrder: %v", err)
	}
}
