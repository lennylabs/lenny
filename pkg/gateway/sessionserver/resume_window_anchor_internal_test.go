// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §6.2 (Pod State Machine), §7.3 (Resume driver)
//
// The resume driver bounds an attempt by the time left in the session's
// resume_pending window, measured from the row's entry to resume_pending as
// the watchdog's window sweep measures it. A row that entered the state long
// ago has no window left although a later write left its UpdatedAt recent,
// and a row that entered the state recently has nearly the whole window
// although its UpdatedAt is old. The second half discriminates: measured
// from UpdatedAt, the window would already have ended.
func TestResumeWindowLeftRunsFromStateEntry_spec_6_2(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	const window = 15 * time.Minute
	s := &Server{clock: func() time.Time { return now }, maxResumePending: window}

	stale := sessionstore.Session{UserID: testOwner, StateEnteredAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Second)}
	if got := s.windowLeft(stale); got > 0 {
		t.Errorf("windowLeft for a row that entered resume_pending 1h ago = %s, want <= 0", got)
	}
	fresh := sessionstore.Session{UserID: testOwner, StateEnteredAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Hour)}
	if got, want := s.windowLeft(fresh), window-time.Minute; got != want {
		t.Errorf("windowLeft for a row that entered resume_pending 1m ago = %s, want %s", got, want)
	}
}
