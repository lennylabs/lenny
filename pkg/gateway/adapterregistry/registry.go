// SPDX-License-Identifier: MIT

// Package adapterregistry implements the §15.0 ExternalAdapterRegistry:
// the gateway-side registry that holds every registered external
// protocol adapter, mounts each adapter's HTTP surface on a shared
// http.ServeMux, and fans out the session-lifecycle hooks
// (OnSessionCreated, OnSessionEvent, OnSessionTerminated) per the
// §15.0 ExternalProtocolAdapter interface.
//
// v1 scope: the built-in MCP, OpenAI Completions, and Open Responses
// adapters register through this surface so cmd/lenny-gateway/main.go
// composes the gateway HTTP mux through Registry.Mount instead of
// stamping each handler onto the mux by hand. The §15.0 admin-API
// runtime-registration path (POST /v1/admin/external-adapters) is
// served by the same Registry so a third-party adapter registered at
// runtime takes effect without a restart. OutboundChannel dispatch is
// included so adapters that already implement push (the MCP Streamable
// HTTP transport's SSE channel) integrate cleanly when their channel
// wiring lands.
package adapterregistry

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/adapter"
)

// Capabilities is the §15.0 AdapterCapabilities — re-exported here as
// the registry's public Capabilities type so callers do not have to
// import pkg/gateway/adapter for the registry contract alone. The
// underlying struct is the same value type so a third-party adapter
// can populate either symbol interchangeably.
type Capabilities = adapter.Capabilities

// SessionMetadata is the §15.0 SessionMetadata projection passed to
// OnSessionCreated. v1 carries only the fields wired up by the
// existing gateway surfaces; the spec defines the full closed set in
// §15.0 "Shared Adapter Types".
type SessionMetadata struct {
	TenantID                  string
	SessionID                 string
	RuntimeName               string
	DelegationDepth           int
	NegotiatedProtocolVersion string
}

// SessionEventKind is the §15.0 closed enum of outbound event
// categories. It mirrors the SessionEventKind constants in spec
// §15.0; adapters declare the subset they handle via
// OutboundCapabilitySet.SupportedEventKinds.
type SessionEventKind string

const (
	// SessionEventStateChange — session state transition (§15.0).
	SessionEventStateChange SessionEventKind = "state_change"
	// SessionEventOutput — agent output frame (§15.0).
	SessionEventOutput SessionEventKind = "output"
	// SessionEventElicitation — elicitation request surfaced (§15.0).
	SessionEventElicitation SessionEventKind = "elicitation"
	// SessionEventToolUse — tool-call lifecycle transition (§15.0).
	SessionEventToolUse SessionEventKind = "tool_use"
	// SessionEventError — non-terminal session error (§15.0).
	SessionEventError SessionEventKind = "error"
	// SessionEventTerminated — terminal-state event (§15.0).
	SessionEventTerminated SessionEventKind = "terminated"
)

// AllSessionEventKinds returns the §15.0 closed SessionEventKind enum in
// spec order. The gateway will never dispatch a kind outside this set and
// third-party adapters MUST NOT rely on receiving unknown kinds
// (§15.0 "SessionEvent Kind Registry"). spec: §15 line 318.
func AllSessionEventKinds() []SessionEventKind {
	return []SessionEventKind{
		SessionEventStateChange,
		SessionEventOutput,
		SessionEventElicitation,
		SessionEventToolUse,
		SessionEventError,
		SessionEventTerminated,
	}
}

// IsValid reports whether k is one of the §15.0 closed-enum kinds.
func (k SessionEventKind) IsValid() bool {
	for _, v := range AllSessionEventKinds() {
		if k == v {
			return true
		}
	}
	return false
}

// ValidateCapabilityConsistency enforces the §15.0 "Capability-consistency
// invariant with elicitation policy": an adapter that declares
// SessionEventElicitation in its OutboundCapabilitySet.SupportedEventKinds
// MUST also return AdapterCapabilities.SupportsElicitation: true. Declaring
// the elicitation kind without the matching capability would mislead clients
// that gate elicitation-dependent workflows on SupportsElicitation. The check
// also rejects any kind outside the closed enum, which the spec calls a
// gateway-internal bug rather than an adapter-extension point.
// spec: §15 line 559.
func ValidateCapabilityConsistency(caps Capabilities, out OutboundCapabilitySet) error {
	declaresElicitation := false
	for _, k := range out.SupportedEventKinds {
		if !k.IsValid() {
			return fmt.Errorf("adapterregistry: SupportedEventKinds carries %q which is outside the §15.0 closed SessionEventKind enum", k)
		}
		if k == SessionEventElicitation {
			declaresElicitation = true
		}
	}
	if declaresElicitation && !caps.SupportsElicitation {
		return fmt.Errorf("adapterregistry: adapter declares the %q outbound kind but Capabilities.SupportsElicitation is false (§15.0 capability-consistency invariant)", SessionEventElicitation)
	}
	return nil
}

// SessionEvent is the outbound event envelope dispatched to adapters
// per §15.0. v1 carries the minimum fields the built-in surfaces
// emit; the spec defines the closed schema in §15.0 "Shared Adapter
// Types".
type SessionEvent struct {
	Kind      SessionEventKind
	SeqNum    uint64
	Payload   []byte
	SessionID string
}

// TerminationCode is the §15.0 closed enum of terminal-state causes.
type TerminationCode string

const (
	// TerminationCompleted — runtime finished normally (§15.0).
	TerminationCompleted TerminationCode = "completed"
	// TerminationFailed — runtime exited abnormally (§15.0).
	TerminationFailed TerminationCode = "failed"
	// TerminationCancelled — caller cancelled the session (§15.0).
	TerminationCancelled TerminationCode = "cancelled"
	// TerminationExpired — session exceeded its max age (§15.0).
	TerminationExpired TerminationCode = "expired"
	// TerminationDrained — gateway/pod drained before completion (§15.0).
	TerminationDrained TerminationCode = "drained"
)

// TerminationReason is the §15.0 closed-enum termination cause.
type TerminationReason struct {
	Code   TerminationCode
	Detail string
}

// OutboundCapabilitySet is the §15.0 declaration of an adapter's
// asynchronous push capabilities. All fields are false in the zero
// value (the BaseAdapter default).
type OutboundCapabilitySet struct {
	PushNotifications          bool
	SupportedEventKinds        []SessionEventKind
	MaxConcurrentSubscriptions int
}

// OutboundSubscription is the §15.0 caller-supplied delivery target
// (webhook URL, persistent SSE writer, etc.).
type OutboundSubscription struct {
	CallbackURL    string
	ResponseWriter http.ResponseWriter
	Metadata       map[string]string
}

// OutboundChannel is the §15.0 push-channel handle. Send is
// non-blocking (the channel implements either buffered-drop or
// bounded-error policy per §15.0); Close releases resources.
type OutboundChannel interface {
	Send(ctx context.Context, event SessionEvent) error
	Close() error
}

// ExternalProtocolAdapter is the §15.0 canonical interface every
// external adapter implements. The required methods (HandleInbound,
// HandleDiscovery, Capabilities) are mandatory; the optional lifecycle
// and outbound hooks have no-op defaults via BaseAdapter so existing
// adapters (MCP, OpenAI Completions, Open Responses) need not change.
type ExternalProtocolAdapter interface {
	// Name is the adapter identifier used in admin APIs, audit, and
	// metric labels. Distinct from the path prefix so adapters with
	// the same prefix may be swapped out by name.
	Name() string

	// HTTPHandler returns the http.Handler the registry mounts at
	// Capabilities().PathPrefix. The handler implements §15.0
	// HandleInbound and HandleDiscovery on the appropriate routes; the
	// registry treats the returned value as opaque.
	HTTPHandler() http.Handler

	// Capabilities returns the §15.0 AdapterCapabilities declaration.
	// PathPrefix MUST be non-empty and unique across the registry.
	Capabilities() Capabilities

	// OnSessionCreated is invoked once per session immediately after
	// the gateway has materialized the session record. Per §15.0
	// adapters MAY use this hook to allocate per-session state (push
	// channels, correlation maps); returning a non-nil error fails the
	// session-create call.
	OnSessionCreated(ctx context.Context, metadata SessionMetadata) error

	// OnSessionEvent is invoked for each SessionEvent the gateway
	// emits while the session is active. The registry filters by the
	// adapter's declared OutboundCapabilitySet.SupportedEventKinds per
	// the §15.0 dispatch-filter rule before calling this hook.
	OnSessionEvent(ctx context.Context, event SessionEvent) error

	// OnSessionTerminated is invoked once per session when it reaches
	// a terminal state. Per §15.0 the hook MUST be safe to call after
	// the session record has been cleaned up.
	OnSessionTerminated(ctx context.Context, sessionID string, reason TerminationReason) error

	// OutboundCapabilities declares the adapter's push capabilities
	// per §15.0. The zero value (PushNotifications false, no kinds)
	// is the BaseAdapter default.
	OutboundCapabilities() OutboundCapabilitySet

	// OpenOutboundChannel returns an OutboundChannel for the session
	// per §15.0. Adapters with no outbound push return a no-op
	// channel; the BaseAdapter implementation does this by default.
	OpenOutboundChannel(ctx context.Context, sessionID string, sub OutboundSubscription) (OutboundChannel, error)
}

// BaseAdapter provides no-op implementations of the §15.0 optional
// hooks so existing adapters embed it and override only the methods
// they actually need. The required Name / HTTPHandler / Capabilities
// methods have no sensible default; an adapter that embeds BaseAdapter
// MUST supply them.
type BaseAdapter struct{}

// OnSessionCreated is a no-op (§15.0 BaseAdapter default).
func (BaseAdapter) OnSessionCreated(context.Context, SessionMetadata) error { return nil }

// OnSessionEvent is a no-op (§15.0 BaseAdapter default).
func (BaseAdapter) OnSessionEvent(context.Context, SessionEvent) error { return nil }

// OnSessionTerminated is a no-op (§15.0 BaseAdapter default).
func (BaseAdapter) OnSessionTerminated(context.Context, string, TerminationReason) error {
	return nil
}

// OutboundCapabilities returns the zero-value (§15.0 BaseAdapter
// default — PushNotifications false, no SupportedEventKinds).
func (BaseAdapter) OutboundCapabilities() OutboundCapabilitySet { return OutboundCapabilitySet{} }

// OpenOutboundChannel returns a discarding channel (§15.0 BaseAdapter
// default — adapters with no outbound push reject the open call).
func (BaseAdapter) OpenOutboundChannel(context.Context, string, OutboundSubscription) (OutboundChannel, error) {
	return discardChannel{}, nil
}

// discardChannel is the §15.0 BaseAdapter no-op OutboundChannel: Send
// silently drops the event and Close is a no-op so adapters with no
// outbound push surface cleanly through the registry's lifecycle
// dispatch path.
type discardChannel struct{}

func (discardChannel) Send(context.Context, SessionEvent) error { return nil }
func (discardChannel) Close() error                             { return nil }

// Registry is the §15.0 ExternalAdapterRegistry. Adapters register
// through Register and are mounted on a shared http.ServeMux via
// Mount; lifecycle events fan out across every registered adapter via
// DispatchSessionCreated, DispatchSessionEvent, DispatchTerminated.
// Concurrent reads and writes are guarded by an internal RWMutex so
// the §15.0 admin-API runtime-registration path can register a new
// adapter while in-flight requests dispatch against the existing set.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]ExternalProtocolAdapter
	// prefixes records the path prefixes the registry has already
	// mounted so a second adapter registering the same prefix is
	// rejected (§15.0 "PathPrefix MUST be unique across all
	// registered adapters").
	prefixes map[string]string
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{
		adapters: map[string]ExternalProtocolAdapter{},
		prefixes: map[string]string{},
	}
}

// Register adds a to the registry. Returns an error if the adapter's
// Name or PathPrefix is empty, or if either collides with an already-
// registered adapter (§15.0 uniqueness rule).
func (r *Registry) Register(a ExternalProtocolAdapter) error {
	if a == nil {
		return fmt.Errorf("adapterregistry: nil adapter (§15.0)")
	}
	name := strings.TrimSpace(a.Name())
	if name == "" {
		return fmt.Errorf("adapterregistry: adapter name is empty (§15.0)")
	}
	caps := a.Capabilities()
	prefix := strings.TrimSpace(caps.PathPrefix)
	if prefix == "" {
		return fmt.Errorf("adapterregistry: adapter %q PathPrefix is empty (§15.0)", name)
	}
	// §15.0 capability-consistency invariant: an adapter cannot declare the
	// elicitation outbound kind without SupportsElicitation, and cannot
	// declare a kind outside the closed enum. Checked at registration so a
	// misdeclared adapter never reaches the dispatch path.
	if err := ValidateCapabilityConsistency(caps, a.OutboundCapabilities()); err != nil {
		return fmt.Errorf("adapterregistry: adapter %q: %w", name, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("adapterregistry: adapter %q already registered (§15.0)", name)
	}
	if owner, exists := r.prefixes[prefix]; exists {
		return fmt.Errorf("adapterregistry: PathPrefix %q already owned by %q (§15.0)", prefix, owner)
	}
	r.adapters[name] = a
	r.prefixes[prefix] = name
	return nil
}

// Unregister removes the adapter by name and returns whether anything
// was removed. The §15.0 admin-API DELETE
// /v1/admin/external-adapters/{name} path drives this.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.adapters[name]
	if !ok {
		return false
	}
	delete(r.adapters, name)
	delete(r.prefixes, a.Capabilities().PathPrefix)
	return true
}

// Lookup returns the adapter registered under name and whether it
// exists. The §15.0 admin-API GET /v1/admin/external-adapters/{name}
// path drives this.
func (r *Registry) Lookup(name string) (ExternalProtocolAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[name]
	return a, ok
}

// Names returns the sorted list of registered adapter names. The
// §15.0 admin-API GET /v1/admin/external-adapters path drives this.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for name := range r.adapters {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Mount installs every registered adapter's HTTPHandler on mux at the
// adapter's Capabilities().PathPrefix. The §15.0 routing contract
// says simultaneously active adapters route by path prefix; Mount
// uses both the exact-prefix and slash-suffix forms so a handler
// registered at "/v1/responses" also catches "/v1/responses/{id}".
//
// Mount is safe to call once after all built-in adapters have been
// registered; the §15.0 admin-API runtime-registration path uses
// MountAdapter for individual adapters added after Mount has run.
func (r *Registry) Mount(mux *http.ServeMux) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.adapters {
		r.mountUnlocked(mux, a)
	}
}

// MountAdapter installs one adapter's HTTPHandler on mux. The §15.0
// admin-API runtime-registration path calls this after a successful
// Register so the new adapter starts serving without a restart.
func (r *Registry) MountAdapter(mux *http.ServeMux, a ExternalProtocolAdapter) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	r.mountUnlocked(mux, a)
}

func (r *Registry) mountUnlocked(mux *http.ServeMux, a ExternalProtocolAdapter) {
	prefix := a.Capabilities().PathPrefix
	h := a.HTTPHandler()
	// Mount the exact path so a single-shot handler (the v1 MCP POST
	// /mcp endpoint) catches its own prefix.
	mux.Handle(prefix, h)
	// Mount the slash-suffix form so a sub-path (e.g.,
	// /v1/responses/{id}) reaches the same adapter.
	if !strings.HasSuffix(prefix, "/") {
		mux.Handle(prefix+"/", h)
	}
}

// DispatchSessionCreated fans out the OnSessionCreated hook to every
// registered adapter per §15.0. Hook errors are accumulated and
// returned together so a slow adapter cannot blank the others.
func (r *Registry) DispatchSessionCreated(ctx context.Context, metadata SessionMetadata) []error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var errs []error
	for _, a := range r.adapters {
		if err := a.OnSessionCreated(ctx, metadata); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
		}
	}
	return errs
}

// DispatchSessionEvent fans out a SessionEvent to every adapter whose
// declared OutboundCapabilitySet.SupportedEventKinds includes the
// event's Kind (the §15.0 dispatch-filter rule). Adapters that do
// not declare the kind never receive the event.
func (r *Registry) DispatchSessionEvent(ctx context.Context, event SessionEvent) []error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var errs []error
	for _, a := range r.adapters {
		caps := a.OutboundCapabilities()
		if !kindHandled(caps.SupportedEventKinds, event.Kind) {
			continue
		}
		if err := a.OnSessionEvent(ctx, event); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
		}
	}
	return errs
}

// DispatchSessionTerminated fans out the OnSessionTerminated hook to
// every registered adapter per §15.0. Errors are accumulated as in
// DispatchSessionCreated.
func (r *Registry) DispatchSessionTerminated(ctx context.Context, sessionID string, reason TerminationReason) []error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var errs []error
	for _, a := range r.adapters {
		if err := a.OnSessionTerminated(ctx, sessionID, reason); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Name(), err))
		}
	}
	return errs
}

// kindHandled reports whether kind appears in the declared subset.
// Per §15.0 an empty SupportedEventKinds means the adapter handles
// no kinds — the gateway MUST NOT dispatch to it.
func kindHandled(declared []SessionEventKind, kind SessionEventKind) bool {
	for _, k := range declared {
		if k == kind {
			return true
		}
	}
	return false
}

// SimpleAdapter is a §15.0 ExternalProtocolAdapter wrapper that
// captures an existing http.Handler plus its capability declaration.
// The built-in adapters (MCP, OpenAI Completions, Open Responses)
// keep their handler logic in their existing packages and register
// into the registry through a SimpleAdapter so the migration adds no
// behavioral changes — only the indirection through the registry.
type SimpleAdapter struct {
	BaseAdapter
	name    string
	handler http.Handler
	caps    Capabilities
}

// NewSimpleAdapter returns a SimpleAdapter wrapping handler. The name
// and capabilities are required; capabilities.PathPrefix MUST be
// non-empty (Register enforces this).
func NewSimpleAdapter(name string, handler http.Handler, caps Capabilities) *SimpleAdapter {
	return &SimpleAdapter{name: name, handler: handler, caps: caps}
}

// Name returns the adapter name registered with the registry.
func (s *SimpleAdapter) Name() string { return s.name }

// HTTPHandler returns the wrapped handler.
func (s *SimpleAdapter) HTTPHandler() http.Handler { return s.handler }

// Capabilities returns the §15.0 declaration.
func (s *SimpleAdapter) Capabilities() Capabilities { return s.caps }
