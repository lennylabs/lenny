// SPDX-License-Identifier: MIT

package leaseelicit

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
)

// TestNewPublishesTheElicitationThroughTheSeamEvents checks that an
// elicitor built over Config.Events publishes the elicitation_request event
// on the requesting session's stream through the seam interface, without a
// tenant scope, and that one built without Events publishes nothing.
//
// spec: GWSEAM-Publish
func TestNewPublishesTheElicitationThroughTheSeamEvents(t *testing.T) {
	bus := sessionevents.NewBus(0)
	var events seam.Events = bus
	e := New(Config{Events: events})
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	e.publishRequest("sess-req", "lease-elicit-1", now)

	hist := bus.History("sess-req", 0)
	if len(hist) != 1 || hist[0].Type != "elicitation_request" || !strings.Contains(hist[0].Data, "lease-elicit-1") {
		t.Fatalf("history = %+v, want one elicitation_request naming lease-elicit-1", hist)
	}
	// The event carried no tenant, so a subscriber of any tenant reads it.
	sub, err := bus.Subscribe("acme", "sess-req", 0, 1)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	sub.Close()

	if quiet := New(Config{}); quiet.publish != nil {
		t.Error("an elicitor without Events has a publisher")
	}
}
