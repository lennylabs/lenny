// SPDX-License-Identifier: MIT

package bindattempt

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// The in-process transport hands the fixture the Server's registry view and
// the wire transport hands it none, so the unnumbered-property cases name
// registry state in the in-process run and keep wire-only assertions over
// gRPC.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
func TestOnlyTheInProcessTransportInspectsTheRegistry_spec_4_7_1(t *testing.T) {
	inProc := New(t, InProcess)
	if inProc.conn.Inspect == nil {
		t.Fatal("the in-process transport set no registry inspector")
	}
	inProc.bindAndStart(t, alice, TokenA)
	if got, want := inProc.conn.Inspect(alice), stampedEntry(TokenA, true); got != want {
		t.Errorf("in-process inspector = %+v, want %+v", got, want)
	}
	if New(t, OverGRPC).conn.Inspect != nil {
		t.Error("the gRPC transport set a registry inspector; the wire run must assert only what a caller observes")
	}
}

// A registry failure message names the state the adapter holds: whether an
// entry stands, its stamp, whether its session started, and the hold.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold)
func TestRegistryFailureNamesTheAdaptersState_spec_4_7_1(t *testing.T) {
	for _, tc := range []struct {
		view adapter.SlotRegistryView
		want string
	}{
		{clearedIdentifier, "no entry, no reclaim hold"},
		{heldIdentifier, "no entry, the reclaim hold open"},
		{stampedEntry("", true), "an untokened entry, started, no reclaim hold"},
		{stampedEntry(TokenB, false), `an entry stamped "` + TokenB + `", unstarted, no reclaim hold`},
	} {
		if got := describeView(tc.view); got != tc.want {
			t.Errorf("describeView(%+v) = %q, want %q", tc.view, got, tc.want)
		}
	}
	if !strings.Contains(describeView(stampedEntry(TokenA, true)), TokenA) {
		t.Error("a stamped entry's description omits the stamp")
	}
}
