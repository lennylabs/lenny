// SPDX-License-Identifier: MIT

package adapterregistry_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/adapterregistry"
)

// fakeAdapter is a test ExternalProtocolAdapter that records every
// lifecycle invocation so the §15.0 contract can be asserted.
type fakeAdapter struct {
	adapterregistry.BaseAdapter
	name    string
	prefix  string
	body    string
	elicits bool
	caps    adapterregistry.OutboundCapabilitySet
	created []adapterregistry.SessionMetadata
	events  []adapterregistry.SessionEvent
	ended   []adapterregistry.TerminationReason
	mu      sync.Mutex
	failOn  string
}

func (f *fakeAdapter) Name() string { return f.name }

func (f *fakeAdapter) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(f.body))
	})
}

func (f *fakeAdapter) Capabilities() adapterregistry.Capabilities {
	return adapterregistry.Capabilities{
		PathPrefix:          f.prefix,
		Protocol:            f.name,
		SupportsElicitation: f.elicits,
	}
}

func (f *fakeAdapter) OutboundCapabilities() adapterregistry.OutboundCapabilitySet {
	return f.caps
}

func (f *fakeAdapter) OnSessionCreated(_ context.Context, m adapterregistry.SessionMetadata) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, m)
	if f.failOn == "created" {
		return errors.New("fakeAdapter created failure")
	}
	return nil
}

func (f *fakeAdapter) OnSessionEvent(_ context.Context, e adapterregistry.SessionEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	if f.failOn == "event" {
		return errors.New("fakeAdapter event failure")
	}
	return nil
}

func (f *fakeAdapter) OnSessionTerminated(_ context.Context, _ string, r adapterregistry.TerminationReason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, r)
	return nil
}

// spec: §15.0 — Register/Lookup/Names round-trip and a re-register
// of the same name is rejected so a third-party adapter cannot
// silently shadow a built-in.
func TestRegisterRoundTripsAndRejectsDuplicates(t *testing.T) {
	r := adapterregistry.New()
	a := &fakeAdapter{name: "mcp", prefix: "/mcp"}
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Lookup("mcp")
	if !ok || got != a {
		t.Fatalf("Lookup did not round-trip: %v %v", got, ok)
	}
	if names := r.Names(); len(names) != 1 || names[0] != "mcp" {
		t.Fatalf("Names = %v, want [mcp]", names)
	}
	// Duplicate name.
	if err := r.Register(&fakeAdapter{name: "mcp", prefix: "/x"}); err == nil {
		t.Error("duplicate name should be rejected (§15.0)")
	}
	// Duplicate prefix.
	if err := r.Register(&fakeAdapter{name: "mcp2", prefix: "/mcp"}); err == nil {
		t.Error("duplicate PathPrefix should be rejected (§15.0)")
	}
}

// spec: §15.0 — adapters with empty Name or PathPrefix are rejected
// at registration; nil adapter likewise.
func TestRegisterRejectsInvalidInputs(t *testing.T) {
	r := adapterregistry.New()
	if err := r.Register(nil); err == nil {
		t.Error("nil adapter should be rejected (§15.0)")
	}
	if err := r.Register(&fakeAdapter{name: "", prefix: "/x"}); err == nil {
		t.Error("empty name should be rejected (§15.0)")
	}
	if err := r.Register(&fakeAdapter{name: "x", prefix: ""}); err == nil {
		t.Error("empty PathPrefix should be rejected (§15.0)")
	}
}

// spec: §15.0 — Unregister removes the adapter and frees its prefix
// so a replacement can register at the same path immediately.
func TestUnregisterFreesNameAndPrefix(t *testing.T) {
	r := adapterregistry.New()
	a := &fakeAdapter{name: "mcp", prefix: "/mcp"}
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !r.Unregister("mcp") {
		t.Fatal("Unregister should report true on success")
	}
	if r.Unregister("mcp") {
		t.Error("Unregister on missing should report false")
	}
	// Prefix is reusable now.
	if err := r.Register(&fakeAdapter{name: "mcp-v2", prefix: "/mcp"}); err != nil {
		t.Fatalf("re-register after Unregister: %v", err)
	}
}

// spec: §15.0 — Mount installs every registered adapter on a shared
// mux at its PathPrefix; both the exact prefix and the slash-suffix
// form are mounted so a sub-path (e.g., /v1/responses/{id}) reaches
// the same adapter.
func TestMountInstallsHandlersAtPathPrefix(t *testing.T) {
	r := adapterregistry.New()
	if err := r.Register(&fakeAdapter{name: "mcp", prefix: "/mcp", body: "mcp-body"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&fakeAdapter{name: "openai", prefix: "/v1/chat/completions", body: "openai-body"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&fakeAdapter{name: "responses", prefix: "/v1/responses", body: "responses-body"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	r.Mount(mux)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/mcp", "mcp-body"},
		{"/v1/chat/completions", "openai-body"},
		{"/v1/responses", "responses-body"},
		{"/v1/responses/abc123", "responses-body"},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if got := strings.TrimSpace(rr.Body.String()); got != tc.want {
			t.Errorf("path %q: got %q, want %q", tc.path, got, tc.want)
		}
	}
}

// spec: §15.0 — MountAdapter installs a single adapter after the
// initial Mount so the admin-API runtime-registration path takes
// effect without a restart.
func TestMountAdapterServesAfterInitialMount(t *testing.T) {
	r := adapterregistry.New()
	if err := r.Register(&fakeAdapter{name: "mcp", prefix: "/mcp", body: "mcp-body"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	r.Mount(mux)

	// Late-registered adapter, then attached to the live mux.
	a := &fakeAdapter{name: "responses", prefix: "/v1/responses", body: "responses-body"}
	if err := r.Register(a); err != nil {
		t.Fatalf("late Register: %v", err)
	}
	r.MountAdapter(mux, a)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if got := strings.TrimSpace(rr.Body.String()); got != "responses-body" {
		t.Fatalf("MountAdapter did not attach late adapter: got %q", got)
	}
}

// spec: §15.0 — DispatchSessionCreated and DispatchSessionTerminated
// fan out to every registered adapter; the order does not matter for
// v1 but every adapter MUST receive the call.
func TestDispatchSessionCreatedAndTerminatedFansOutToEveryAdapter(t *testing.T) {
	r := adapterregistry.New()
	a1 := &fakeAdapter{name: "mcp", prefix: "/mcp"}
	a2 := &fakeAdapter{name: "openai", prefix: "/v1/chat/completions"}
	for _, a := range []*fakeAdapter{a1, a2} {
		if err := r.Register(a); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	m := adapterregistry.SessionMetadata{TenantID: "acme", SessionID: "sess_1"}
	if errs := r.DispatchSessionCreated(context.Background(), m); len(errs) != 0 {
		t.Fatalf("DispatchSessionCreated errs: %v", errs)
	}
	for _, a := range []*fakeAdapter{a1, a2} {
		if len(a.created) != 1 || a.created[0].SessionID != "sess_1" {
			t.Errorf("adapter %s did not see OnSessionCreated", a.name)
		}
	}
	reason := adapterregistry.TerminationReason{Code: adapterregistry.TerminationCompleted}
	if errs := r.DispatchSessionTerminated(context.Background(), "sess_1", reason); len(errs) != 0 {
		t.Fatalf("DispatchSessionTerminated errs: %v", errs)
	}
	for _, a := range []*fakeAdapter{a1, a2} {
		if len(a.ended) != 1 || a.ended[0].Code != adapterregistry.TerminationCompleted {
			t.Errorf("adapter %s did not see OnSessionTerminated", a.name)
		}
	}
}

// spec: §15.0 — the dispatch-filter rule: an adapter that does not
// declare a SupportedEventKind MUST NOT receive that kind. The
// BaseAdapter default has an empty SupportedEventKinds set, so a
// stock adapter receives nothing.
func TestDispatchSessionEventHonorsTheDispatchFilterRule(t *testing.T) {
	r := adapterregistry.New()
	// a1 declares output + state_change; a2 declares only error.
	a1 := &fakeAdapter{
		name: "mcp", prefix: "/mcp",
		caps: adapterregistry.OutboundCapabilitySet{
			PushNotifications:   true,
			SupportedEventKinds: []adapterregistry.SessionEventKind{adapterregistry.SessionEventOutput, adapterregistry.SessionEventStateChange},
		},
	}
	a2 := &fakeAdapter{
		name: "responses", prefix: "/v1/responses",
		caps: adapterregistry.OutboundCapabilitySet{
			PushNotifications:   true,
			SupportedEventKinds: []adapterregistry.SessionEventKind{adapterregistry.SessionEventError},
		},
	}
	a3 := &fakeAdapter{name: "openai", prefix: "/v1/chat/completions"} // BaseAdapter default: no kinds.
	for _, a := range []*fakeAdapter{a1, a2, a3} {
		if err := r.Register(a); err != nil {
			t.Fatal(err)
		}
	}
	r.DispatchSessionEvent(context.Background(), adapterregistry.SessionEvent{
		Kind:    adapterregistry.SessionEventOutput,
		SeqNum:  7,
		Payload: []byte(`{"o":1}`),
	})
	if len(a1.events) != 1 || a1.events[0].SeqNum != 7 {
		t.Errorf("a1 should receive output event; got %v", a1.events)
	}
	if len(a2.events) != 0 {
		t.Errorf("a2 should NOT receive output event (declares only error); got %v", a2.events)
	}
	if len(a3.events) != 0 {
		t.Errorf("a3 (BaseAdapter default) MUST NOT receive any events; got %v", a3.events)
	}

	r.DispatchSessionEvent(context.Background(), adapterregistry.SessionEvent{Kind: adapterregistry.SessionEventError})
	if len(a2.events) != 1 {
		t.Errorf("a2 should receive error event; got %v", a2.events)
	}
}

// spec: §15.0 — a per-adapter hook error is accumulated and returned;
// it MUST NOT prevent other adapters from receiving the call.
func TestDispatchSessionCreatedAccumulatesErrorsWithoutShadowing(t *testing.T) {
	r := adapterregistry.New()
	a1 := &fakeAdapter{name: "mcp", prefix: "/mcp", failOn: "created"}
	a2 := &fakeAdapter{name: "openai", prefix: "/v1/chat/completions"}
	for _, a := range []*fakeAdapter{a1, a2} {
		if err := r.Register(a); err != nil {
			t.Fatal(err)
		}
	}
	errs := r.DispatchSessionCreated(context.Background(), adapterregistry.SessionMetadata{SessionID: "sess_1"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "mcp") {
		t.Errorf("expected one error from mcp; got %v", errs)
	}
	if len(a2.created) != 1 {
		t.Error("a2 should still see the call even after a1 errored")
	}
}

// spec: §15.0 — the BaseAdapter default returns a no-op
// OutboundChannel that discards Send and returns nil from Close. An
// adapter that embeds BaseAdapter MUST surface this without panicking.
func TestBaseAdapterOpenOutboundChannelReturnsNoOpChannel(t *testing.T) {
	a := &fakeAdapter{name: "x", prefix: "/x"}
	ch, err := a.OpenOutboundChannel(context.Background(), "sess_1", adapterregistry.OutboundSubscription{})
	if err != nil {
		t.Fatalf("OpenOutboundChannel: %v", err)
	}
	if err := ch.Send(context.Background(), adapterregistry.SessionEvent{Kind: adapterregistry.SessionEventOutput}); err != nil {
		t.Errorf("discardChannel.Send should be a no-op; got %v", err)
	}
	if err := ch.Close(); err != nil {
		t.Errorf("discardChannel.Close should be nil; got %v", err)
	}
}

// spec: §15.0 — concurrent Register/Mount/Dispatch must not race.
// The §15.0 admin-API runtime-registration path can register a new
// adapter while in-flight requests fan out to the existing set.
func TestRegistryIsSafeUnderConcurrentRegisterAndDispatch(t *testing.T) {
	r := adapterregistry.New()
	for i := 0; i < 4; i++ {
		err := r.Register(&fakeAdapter{
			name:   strNum("seed", i),
			prefix: strNum("/seed", i),
			caps: adapterregistry.OutboundCapabilitySet{
				SupportedEventKinds: []adapterregistry.SessionEventKind{adapterregistry.SessionEventOutput},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 32; i++ {
			_ = r.Register(&fakeAdapter{name: strNum("late", i), prefix: strNum("/late", i)})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 256; i++ {
			r.DispatchSessionCreated(context.Background(), adapterregistry.SessionMetadata{SessionID: "sess_1"})
			r.DispatchSessionEvent(context.Background(), adapterregistry.SessionEvent{Kind: adapterregistry.SessionEventOutput})
		}
	}()
	wg.Wait()
	if got := len(r.Names()); got < 4 {
		t.Fatalf("expected at least 4 adapters, got %d", got)
	}
}

func strNum(prefix string, i int) string {
	// Avoid pulling fmt into the parallel section — use a small inline
	// formatter.
	const digits = "0123456789"
	var buf [16]byte
	n := 0
	if i == 0 {
		return prefix + "0"
	}
	for i > 0 {
		buf[n] = digits[i%10]
		i /= 10
		n++
	}
	// Reverse buf[:n].
	for l, r := 0, n-1; l < r; l, r = l+1, r-1 {
		buf[l], buf[r] = buf[r], buf[l]
	}
	return prefix + string(buf[:n])
}

// spec: §15.0 — the SimpleAdapter wraps an existing http.Handler and
// capability declaration so built-in adapters register through the
// registry without changing their handler logic.
func TestSimpleAdapterWrapsExistingHandler(t *testing.T) {
	body := []byte("hello from underlying handler")
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	a := adapterregistry.NewSimpleAdapter("mcp", h, adapterregistry.Capabilities{PathPrefix: "/mcp", Protocol: "mcp"})
	if a.Name() != "mcp" {
		t.Fatalf("Name = %q, want mcp", a.Name())
	}
	caps := a.Capabilities()
	if caps.PathPrefix != "/mcp" {
		t.Fatalf("PathPrefix = %q, want /mcp", caps.PathPrefix)
	}
	r := adapterregistry.New()
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	mux := http.NewServeMux()
	r.Mount(mux)
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Body.String() != string(body) {
		t.Errorf("SimpleAdapter did not serve through the underlying handler: %q", rr.Body.String())
	}
}
