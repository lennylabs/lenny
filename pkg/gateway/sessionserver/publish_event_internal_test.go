// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
)

// TestANilBusPublishesNothingWithoutPanicking checks that a session server,
// a tool approval gate, and an inbox emitter given a nil *sessionevents.Bus
// treat it as no bus: they store no event bus and publish nothing, rather
// than calling the bus through a typed nil.
//
// spec: GWSEAM-Publish
func TestANilBusPublishesNothingWithoutPanicking(t *testing.T) {
	var bus *sessionevents.Bus
	s := New(memstore.New(), Options{Events: bus})
	if s.events != nil {
		t.Fatalf("the session server stored %#v, want no event bus", s.events)
	}
	s.publishEvent("acme", "sess-1", "status_change", map[string]string{"state": "running"})
	s.publishEvent("", "sess-1", "response", map[string]string{"text": "hi"})

	gate := NewToolApprovalGate(memstore.New(), interactionstore.NewMemory(), bus, platformtools.NewToolApprovals(toolapproval.NewRegistry()), time.Now, 0)
	if gate.events != nil {
		t.Errorf("the tool approval gate stored %#v, want no event bus", gate.events)
	}
	if emitter := NewBusEmitter(bus, time.Now); emitter != nil {
		t.Errorf("the inbox emitter is %#v, want nil for a nil bus", emitter)
	}
}
