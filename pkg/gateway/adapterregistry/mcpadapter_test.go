// SPDX-License-Identifier: MIT

package adapterregistry_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/adapterregistry"
)

// TestMCPAdapterOutboundCapabilities_spec_15_2_1335 pins the mandatory
// §15.2 MCPAdapter override: push-notifications on, the full closed
// SessionEventKind enum supported, unlimited concurrent subscriptions.
func TestMCPAdapterOutboundCapabilities_spec_15_2_1335(t *testing.T) {
	a := adapterregistry.NewMCPAdapter(http.NotFoundHandler())
	out := a.OutboundCapabilities()
	if !out.PushNotifications {
		t.Fatal("MCPAdapter must declare PushNotifications: true")
	}
	if out.MaxConcurrentSubscriptions != 0 {
		t.Fatalf("MaxConcurrentSubscriptions = %d, want 0 (unlimited)", out.MaxConcurrentSubscriptions)
	}
	got := out.SupportedEventKinds
	want := adapterregistry.AllSessionEventKinds()
	if len(got) != len(want) {
		t.Fatalf("SupportedEventKinds = %v, want the full closed enum %v", got, want)
	}
	seen := map[adapterregistry.SessionEventKind]bool{}
	for _, k := range got {
		if !k.IsValid() {
			t.Fatalf("SupportedEventKinds carries %q outside the closed enum", k)
		}
		seen[k] = true
	}
	for _, k := range want {
		if !seen[k] {
			t.Fatalf("SupportedEventKinds omits closed-enum kind %q", k)
		}
	}
}

// TestSessionEventKindClosedEnum_spec_15_318 asserts the closed enum has
// exactly the six §15.0 kinds and rejects values outside it.
func TestSessionEventKindClosedEnum_spec_15_318(t *testing.T) {
	if n := len(adapterregistry.AllSessionEventKinds()); n != 6 {
		t.Fatalf("AllSessionEventKinds has %d kinds, want 6", n)
	}
	for _, k := range adapterregistry.AllSessionEventKinds() {
		if !k.IsValid() {
			t.Fatalf("closed-enum kind %q reported invalid", k)
		}
	}
	if adapterregistry.SessionEventKind("delegation").IsValid() {
		t.Fatal("a value outside the closed enum must be invalid")
	}
}

// TestMCPAdapterSatisfiesCapabilityConsistency_spec_15_559 confirms the
// MCPAdapter pairs the elicitation outbound kind with SupportsElicitation.
func TestMCPAdapterSatisfiesCapabilityConsistency_spec_15_559(t *testing.T) {
	a := adapterregistry.NewMCPAdapter(http.NotFoundHandler())
	if err := adapterregistry.ValidateCapabilityConsistency(a.Capabilities(), a.OutboundCapabilities()); err != nil {
		t.Fatalf("MCPAdapter violates the capability-consistency invariant: %v", err)
	}
	if !a.Capabilities().SupportsElicitation {
		t.Fatal("MCPAdapter must declare SupportsElicitation: true")
	}
}

// TestCapabilityConsistencyRejectsElicitationWithoutSupport_spec_15_559
// exercises the §15.0 invariant: declaring the elicitation outbound kind
// without SupportsElicitation is a misdeclaration.
func TestCapabilityConsistencyRejectsElicitationWithoutSupport_spec_15_559(t *testing.T) {
	caps := adapterregistry.Capabilities{PathPrefix: "/x", Protocol: "x", SupportsElicitation: false}
	out := adapterregistry.OutboundCapabilitySet{
		PushNotifications:   true,
		SupportedEventKinds: []adapterregistry.SessionEventKind{adapterregistry.SessionEventElicitation},
	}
	if err := adapterregistry.ValidateCapabilityConsistency(caps, out); err == nil {
		t.Fatal("expected rejection: elicitation kind without SupportsElicitation")
	}
}

// TestCapabilityConsistencyRejectsUnknownKind_spec_15 rejects a kind
// outside the closed enum.
func TestCapabilityConsistencyRejectsUnknownKind_spec_15(t *testing.T) {
	caps := adapterregistry.Capabilities{PathPrefix: "/x", Protocol: "x"}
	out := adapterregistry.OutboundCapabilitySet{
		SupportedEventKinds: []adapterregistry.SessionEventKind{adapterregistry.SessionEventKind("gossip")},
	}
	if err := adapterregistry.ValidateCapabilityConsistency(caps, out); err == nil {
		t.Fatal("expected rejection: kind outside the closed enum")
	}
}

// TestCapabilityConsistencyAcceptsA2AStyleSet_spec_15_559 confirms the
// invariant admits the A2A-style four-kind declaration (no elicitation,
// no tool_use, SupportsElicitation false) the spec names as conformant.
func TestCapabilityConsistencyAcceptsA2AStyleSet_spec_15_559(t *testing.T) {
	caps := adapterregistry.Capabilities{PathPrefix: "/a2a", Protocol: "a2a", SupportsElicitation: false}
	out := adapterregistry.OutboundCapabilitySet{
		PushNotifications: true,
		SupportedEventKinds: []adapterregistry.SessionEventKind{
			adapterregistry.SessionEventStateChange,
			adapterregistry.SessionEventOutput,
			adapterregistry.SessionEventError,
			adapterregistry.SessionEventTerminated,
		},
	}
	if err := adapterregistry.ValidateCapabilityConsistency(caps, out); err != nil {
		t.Fatalf("A2A-style four-kind set rejected: %v", err)
	}
}

// TestRegistryRejectsMisdeclaredAdapter_spec_15_559 confirms Register
// fails closed when an adapter declares elicitation without
// SupportsElicitation, so a misdeclared adapter never reaches dispatch.
func TestRegistryRejectsMisdeclaredAdapter_spec_15_559(t *testing.T) {
	reg := adapterregistry.New()
	bad := &fakeAdapter{
		name:   "bad",
		prefix: "/bad",
		caps: adapterregistry.OutboundCapabilitySet{
			PushNotifications:   true,
			SupportedEventKinds: []adapterregistry.SessionEventKind{adapterregistry.SessionEventElicitation},
		},
	}
	if err := reg.Register(bad); err == nil {
		t.Fatal("Register accepted an adapter that declares elicitation without SupportsElicitation")
	}
}

// TestRegisterAndMountMCPAdapter_spec_15_2_1335 confirms the MCPAdapter
// registers and serves its handler on /mcp through the registry.
func TestRegisterAndMountMCPAdapter_spec_15_2_1335(t *testing.T) {
	reg := adapterregistry.New()
	got := ""
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got = "served"
		_, _ = w.Write([]byte("ok"))
	})
	if err := reg.Register(adapterregistry.NewMCPAdapter(h)); err != nil {
		t.Fatalf("Register MCPAdapter: %v", err)
	}
	mux := http.NewServeMux()
	reg.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || got != "served" {
		t.Fatalf("MCPAdapter handler not reached on /mcp: code=%d served=%q", rec.Code, got)
	}
}

// TestMCPAdapterDispatchReceivesEveryKind_spec_15_555 exercises the
// dispatch-filter rule against the MCPAdapter's six-kind declaration: a
// fakeAdapter mirroring the MCP outbound set receives every closed-enum
// kind, while a subset adapter receives only its declared kinds.
func TestMCPAdapterDispatchReceivesEveryKind_spec_15_555(t *testing.T) {
	reg := adapterregistry.New()
	full := &fakeAdapter{
		name:   "full",
		prefix: "/full",
		caps: adapterregistry.OutboundCapabilitySet{
			PushNotifications:   true,
			SupportedEventKinds: adapterregistry.AllSessionEventKinds(),
		},
	}
	// full declares elicitation, so it must also declare SupportsElicitation.
	full.elicits = true
	subset := &fakeAdapter{
		name:   "subset",
		prefix: "/subset",
		caps: adapterregistry.OutboundCapabilitySet{
			PushNotifications:   true,
			SupportedEventKinds: []adapterregistry.SessionEventKind{adapterregistry.SessionEventOutput},
		},
	}
	if err := reg.Register(full); err != nil {
		t.Fatalf("Register full: %v", err)
	}
	if err := reg.Register(subset); err != nil {
		t.Fatalf("Register subset: %v", err)
	}
	for _, k := range adapterregistry.AllSessionEventKinds() {
		reg.DispatchSessionEvent(t.Context(), adapterregistry.SessionEvent{Kind: k, SessionID: "s1"})
	}
	if n := len(full.events); n != 6 {
		t.Fatalf("full adapter received %d events, want 6 (one per closed-enum kind)", n)
	}
	if n := len(subset.events); n != 1 {
		t.Fatalf("subset adapter received %d events, want 1 (only its declared output kind)", n)
	}
	if subset.events[0].Kind != adapterregistry.SessionEventOutput {
		t.Fatalf("subset adapter received %q, want output", subset.events[0].Kind)
	}
}
