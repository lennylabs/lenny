// SPDX-License-Identifier: MIT

package bindattempt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// wantRegistry fails the case unless the adapter's registry state for id is
// want, naming the state the adapter actually holds. It runs only when the
// transport can inspect the registry, which is the in-process run: there a
// failure of one of the unnumbered properties names the entry, stamp, start
// or hold that produced it, while over gRPC the case's wire assertions stand
// alone. It is read-only, so a case may call it at any point.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold)
func (f *Fixture) wantRegistry(t *testing.T, what, id string, want adapter.SlotRegistryView) {
	t.Helper()
	if f.conn.Inspect == nil {
		return
	}
	if got := f.conn.Inspect(id); got != want {
		t.Errorf("%s: the registry holds %s for %s, want %s", what, describeView(got), id, describeView(want))
	}
}

// describeView renders a registry view as the state it names, so a failure
// message reads as the adapter's condition rather than a struct dump.
func describeView(v adapter.SlotRegistryView) string {
	parts := []string{"no entry"}
	if v.Entry {
		stamp := "an untokened entry"
		if v.BindAttempt != "" {
			stamp = fmt.Sprintf("an entry stamped %q", v.BindAttempt)
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

// stampedEntry is the view of a live entry carrying token, with no hold.
func stampedEntry(token string, started bool) adapter.SlotRegistryView {
	return adapter.SlotRegistryView{Entry: true, BindAttempt: token, Started: started}
}

// heldIdentifier is the view of an identifier whose entry a release has
// deregistered and whose cleanup is still running.
var heldIdentifier = adapter.SlotRegistryView{ReclaimHeld: true}

// clearedIdentifier is the view of an identifier with neither an entry nor a
// hold, which a completed cleanup leaves.
var clearedIdentifier = adapter.SlotRegistryView{}
