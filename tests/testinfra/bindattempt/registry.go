// SPDX-License-Identifier: MIT

package bindattempt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// registryState is the registry state a case expects for one identifier.
// token is the stamp the case expects the entry to carry, empty for an
// untokened entry; the adapter is asked only whether its stamp equals it, so
// the battery never reads a token back out of the adapter.
type registryState struct {
	entry   bool
	token   string
	started bool
	held    bool
}

// view is the answer the adapter gives for s when asked about s.token.
func (s registryState) view() adapter.SlotRegistryView {
	tokened := s.entry && s.token != ""
	return adapter.SlotRegistryView{
		Entry:              s.entry,
		Tokened:            tokened,
		CarriesBindAttempt: tokened,
		Started:            s.entry && s.started,
		ReclaimHeld:        s.held,
	}
}

// wantRegistry fails the case unless the adapter's registry state for id is
// want, naming the state the adapter actually holds. It runs only when the
// transport can inspect the registry, which is the in-process run: there a
// failure of one of the unnumbered properties names the entry, stamp, start
// or hold that produced it, while over gRPC the case's wire assertions stand
// alone. It is read-only, so a case may call it at any point.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold)
func (f *Fixture) wantRegistry(t *testing.T, what, id string, want registryState) {
	t.Helper()
	if f.conn.Inspect == nil {
		return
	}
	if got := f.conn.Inspect(id, want.token); got != want.view() {
		t.Errorf("%s: the registry holds %s for %s, want %s",
			what, describeView(got, want.token), id, describeView(want.view(), want.token))
	}
}

// describeView renders a registry view as the state it names, so a failure
// message reads as the adapter's condition rather than a struct dump. token
// is the one the view was asked about, which is the case's own expectation.
func describeView(v adapter.SlotRegistryView, token string) string {
	parts := []string{"no entry"}
	if v.Entry {
		stamp := "an untokened entry"
		switch {
		case v.CarriesBindAttempt:
			stamp = fmt.Sprintf("an entry stamped %q", token)
		case v.Tokened:
			stamp = "an entry stamped with another attempt's token"
		}
		started := "unstarted"
		if v.Started {
			started = "started"
		}
		parts = []string{stamp + ", " + started}
	}
	hold := "no reclaim hold"
	if v.ReclaimHeld {
		hold = "the reclaim hold open"
	}
	return strings.Join(append(parts, hold), ", ")
}

// stampedEntry is the state of a live entry carrying token, with no hold.
func stampedEntry(token string, started bool) registryState {
	return registryState{entry: true, token: token, started: started}
}

// heldIdentifier is the state of an identifier whose entry a release has
// deregistered and whose cleanup is still running.
var heldIdentifier = registryState{held: true}

// clearedIdentifier is the state of an identifier with neither an entry nor
// a hold, which a completed cleanup leaves.
var clearedIdentifier = registryState{}
