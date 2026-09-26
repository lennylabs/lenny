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
	if got, want := inProc.conn.Inspect(alice, TokenA), stampedEntry(TokenA, true).view(); got != want {
		t.Errorf("in-process inspector = %+v, want %+v", got, want)
	}
	if New(t, OverGRPC).conn.Inspect != nil {
		t.Error("the gRPC transport set a registry inspector; the wire run must assert only what a caller observes")
	}
}

// A registry failure message names the state the adapter holds: whether an
// entry stands, whether its stamp is the expected attempt's, whether its
// session started, and the hold.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold)
func TestRegistryFailureNamesTheAdaptersState_spec_4_7_1(t *testing.T) {
	anotherAttempt := adapter.SlotRegistryView{Entry: true, Tokened: true}
	for _, tc := range []struct {
		view  adapter.SlotRegistryView
		token string
		want  string
	}{
		{clearedIdentifier.view(), "", "no entry, no reclaim hold"},
		{heldIdentifier.view(), "", "no entry, the reclaim hold open"},
		{stampedEntry("", true).view(), "", "an untokened entry, started, no reclaim hold"},
		{stampedEntry(TokenB, false).view(), TokenB, `an entry stamped "` + TokenB + `", unstarted, no reclaim hold`},
		{anotherAttempt, TokenA, "an entry stamped with another attempt's token, unstarted, no reclaim hold"},
	} {
		if got := describeView(tc.view, tc.token); got != tc.want {
			t.Errorf("describeView(%+v, %q) = %q, want %q", tc.view, tc.token, got, tc.want)
		}
	}
}

// The in-process inspector tells a case whose expected stamp differs from
// the entry's that the entry carries another attempt's token, without
// handing that token back: the case sees Tokened without
// CarriesBindAttempt, and the failure message names neither token it did
// not already hold.
//
// spec: §4.7.1 (role and gateway RPC contract)
func TestInspectorAnswersAnotherAttemptsStampWithoutTheToken_spec_4_7_1(t *testing.T) {
	inProc := New(t, InProcess)
	inProc.bindAndStart(t, alice, TokenA)
	got := inProc.conn.Inspect(alice, TokenB)
	if got == stampedEntry(TokenB, true).view() {
		t.Fatalf("inspector asked about attempt B = %+v, reported attempt B's stamp on attempt A's entry", got)
	}
	if !got.Entry || !got.Tokened || got.CarriesBindAttempt {
		t.Errorf("inspector asked about attempt B = %+v, want a tokened entry that does not carry B", got)
	}
	if msg := describeView(got, TokenB); strings.Contains(msg, TokenA) {
		t.Errorf("failure message %q names attempt A's token, which the case did not supply", msg)
	}
}
