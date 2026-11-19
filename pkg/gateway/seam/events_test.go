// SPDX-License-Identifier: MIT

package seam_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// fakeEvents is an Events implementation with a pointer receiver, so a nil
// *fakeEvents is a typed nil once it is assigned to an Events value.
type fakeEvents struct{ seam.Events }

func (f *fakeEvents) Publish(_, _, _, _ string, _ time.Time) seam.Event { return seam.Event{} }

// valueEvents implements Events with a value receiver, which has no nil
// form.
type valueEvents struct{ seam.Events }

// TestEventsOrNilTurnsATypedNilIntoNil checks that EventsOrNil returns nil
// for a nil interface and for an interface holding a nil pointer, and the
// value itself otherwise, so a part's nil check on its stored Events also
// catches a typed nil.
//
// The helper guards the seam interface's event operations against a wiring
// mistake, so the test cites the operation it protects.
//
// spec: GWSEAM-Publish
func TestEventsOrNilTurnsATypedNilIntoNil(t *testing.T) {
	if got := seam.EventsOrNil(nil); got != nil {
		t.Errorf("nil interface = %v, want nil", got)
	}
	var typedNil *fakeEvents
	var e seam.Events = typedNil
	if e == nil {
		t.Fatal("the fixture does not produce a typed nil")
	}
	if got := seam.EventsOrNil(e); got != nil {
		t.Errorf("typed nil = %#v, want nil", got)
	}
	live := &fakeEvents{}
	if got := seam.EventsOrNil(live); got != live {
		t.Errorf("live pointer = %#v, want it unchanged", got)
	}
	var value seam.Events = valueEvents{}
	if got := seam.EventsOrNil(value); got == nil {
		t.Error("a value receiver came back nil")
	}
}
