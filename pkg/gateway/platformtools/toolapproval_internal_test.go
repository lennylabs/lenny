// SPDX-License-Identifier: MIT

package platformtools

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
)

// TestANilBusLeavesTheApprovalGateWithoutEvents checks that a tool approval
// gate given a nil *sessionevents.Bus treats it as no bus: it stores no
// event bus and publishes nothing, rather than calling the bus through a
// typed nil.
//
// spec: GWSEAM-AwaitApproval
func TestANilBusLeavesTheApprovalGateWithoutEvents(t *testing.T) {
	var bus *sessionevents.Bus
	gate := NewToolApprovalGate(memstore.New(), interactionstore.NewMemory(), bus, nil, time.Now, 0)
	if gate.events != nil {
		t.Fatalf("the tool approval gate stored %#v, want no event bus", gate.events)
	}
	gate.publishRequested("acme", "sess-1", seam.PendingToolCall{ID: "call-1", Name: "tool"})
	if _, err := gate.AwaitApproval(context.Background(), "acme", "sess-1", seam.PendingToolCall{ID: "call-1"}); err == nil {
		t.Errorf("AwaitApproval on a gate with no waits returned no error")
	}
}
