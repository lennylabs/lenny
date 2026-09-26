// SPDX-License-Identifier: MIT

package adapter

import (
	"reflect"
	"testing"
)

// InspectSlotRegistry reports each registry state the §4.7.1 rules read for
// one identifier: no entry, an entry stamped at creation, a started entry,
// an untokened entry, and an identifier under the §5.2 reclaim hold. The
// in-process conformance battery names the state behind a failure through
// it, so a view that misreported any field would misdirect that diagnosis.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold)
func TestInspectSlotRegistryReportsEachRegistryState_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)

	if got := s.InspectSlotRegistry("alice", attempt1); got != (SlotRegistryView{}) {
		t.Fatalf("view of an unknown identifier = %+v, want the zero view", got)
	}

	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("assign: %v", err)
	}
	want := SlotRegistryView{Entry: true, Tokened: true, CarriesBindAttempt: true}
	if got := s.InspectSlotRegistry("alice", attempt1); got != want {
		t.Fatalf("view of a bound, unstarted entry = %+v, want %+v", got, want)
	}

	if err := start(s, "alice"); err != nil {
		t.Fatalf("start: %v", err)
	}
	want.Started = true
	if got := s.InspectSlotRegistry("alice", attempt1); got != want {
		t.Fatalf("view of a started entry = %+v, want %+v", got, want)
	}

	if err := start(s, "bob"); err != nil {
		t.Fatalf("start bob: %v", err)
	}
	if got, want := s.InspectSlotRegistry("bob", ""), (SlotRegistryView{Entry: true, Started: true}); got != want {
		t.Fatalf("view of an untokened started entry = %+v, want %+v", got, want)
	}

	s.mu.Lock()
	_, removed, _, release := s.reclaimSlotLocked("alice")
	s.mu.Unlock()
	if !removed {
		t.Fatal("reclaimSlotLocked removed nothing")
	}
	if got, want := s.InspectSlotRegistry("alice", attempt1), (SlotRegistryView{ReclaimHeld: true}); got != want {
		t.Fatalf("view during the reclaim hold = %+v, want %+v", got, want)
	}
	release()
	if got := s.InspectSlotRegistry("alice", attempt1); got != (SlotRegistryView{}) {
		t.Fatalf("view after the hold ended = %+v, want the zero view", got)
	}
}

// The view answers whether the entry's stamp equals a token the caller
// already holds and nothing more: another attempt's token, an empty token,
// and any token against an untokened entry all answer false. §4.7.1 has the
// adapter only compare the bind attempt token, so a view that matched an
// empty name against an untokened entry, or matched a different attempt,
// would report a stamp the entry does not carry.
//
// spec: §4.7.1 (role and gateway RPC contract)
func TestInspectSlotRegistryAnswersStampEqualityOnly_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)
	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := start(s, "bob"); err != nil {
		t.Fatalf("start bob: %v", err)
	}
	for _, tc := range []struct {
		what, id, token string
	}{
		{"another attempt's token", "alice", attempt2},
		{"an empty token against a tokened entry", "alice", ""},
		{"an empty token against an untokened entry", "bob", ""},
		{"a token against an untokened entry", "bob", attempt1},
	} {
		if s.InspectSlotRegistry(tc.id, tc.token).CarriesBindAttempt {
			t.Errorf("%s: CarriesBindAttempt = true, want false", tc.what)
		}
	}
	if v := s.InspectSlotRegistry("alice", attempt2); !v.Entry || !v.Tokened {
		t.Errorf("view naming another attempt = %+v, want the tokened entry still reported", v)
	}
}

// The view hands no bind attempt token back out of the adapter. §4.7.1 has
// the adapter compare the token for equality and do nothing else with it, and
// no RPC response carries it, so the in-process diagnosis surface carries
// only boolean answers: a string field would give any importer of the
// package a read-back path for a capability over the entry's teardown.
//
// spec: §4.7.1 (role and gateway RPC contract)
func TestSlotRegistryViewCarriesNoToken_spec_4_7_1(t *testing.T) {
	typ := reflect.TypeOf(SlotRegistryView{})
	for i := range typ.NumField() {
		if f := typ.Field(i); f.Type.Kind() != reflect.Bool {
			t.Errorf("SlotRegistryView.%s is %s, want every field a bool answer", f.Name, f.Type)
		}
	}
}
