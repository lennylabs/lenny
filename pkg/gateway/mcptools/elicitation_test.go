// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// mkUserSession seeds a session that carries a user id so the §9.2
// respond-authorization triple tests have a non-empty user component.
func mkUserSession(t *testing.T, store sessionstore.Store, id, user string, parent string) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: user, State: session.StateRunning,
		ParentSessionID: parent, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

// chainDeps builds the elicitation-chain MCP server with the given
// dispatcher configuration and returns the registered server, the
// session store, and the interaction store.
type chainOpts struct {
	timeout    time.Duration
	urlMode    elicitation.URLModeAllowlist
	intercepts func(sessionstore.Session) bool
	depth      elicitation.DepthPolicy
	suppressAt int
	// drops, when set, receives a §9.1 drop-counter notification for
	// every dispatcher rejection. F-9.2.11 / F-9.2.14.
	drops mcptools.ElicitationDropRecorder
	// lifecycle, when set, receives the §16.1 admit/terminal lifecycle
	// notifications. F-9.2.14.
	lifecycle mcptools.ElicitationLifecycleRecorder
}

func newMCPForChain(t *testing.T, opts chainOpts) (*mcp.Server, sessionstore.Store, interactionstore.Store) {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	timeout := opts.timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	mcptools.Register(srv, mcptools.Deps{
		Store:                       store,
		Interactions:                interactions,
		ElicitationTimeout:          timeout,
		ElicitationURLModeAllowlist: opts.urlMode,
		ElicitationIntercepts:       opts.intercepts,
		ElicitationDepthPolicy:      opts.depth,
		ElicitationSuppressAtDepth:  opts.suppressAt,
		ElicitationMetrics:          opts.drops,
		ElicitationLifecycleMetrics: opts.lifecycle,
		IDFunc:                      func() string { return "elic_gen" },
		TenantID:                    "acme",
	})
	return srv, store, interactions
}

// recordingDropMetric records each §9.1 drop the dispatcher reports so
// tests can assert which `reason` label fired (spec: §9.1 line ~/§16.1
// drops counter). F-9.2.11 / F-9.2.14.
type recordingDropMetric struct {
	reasons []string
}

func (r *recordingDropMetric) RecordElicitationDrop(reason string) {
	r.reasons = append(r.reasons, reason)
}

// recordingLifecycleMetric records the §16.1 lines 60–63 admit/terminal
// lifecycle events the dispatcher reports. F-9.2.14. The dispatcher fires
// these hooks from the elicitation handler goroutine while a test reads
// the counters, so the fields are guarded by a mutex to stay -race clean.
type recordingLifecycleMetric struct {
	mu           sync.Mutex
	pendingDelta int
	pendingMax   int
	timeouts     int
	suppressed   int
	roundtrips   []time.Duration
}

func (r *recordingLifecycleMetric) IncElicitationPending() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingDelta++
	if r.pendingDelta > r.pendingMax {
		r.pendingMax = r.pendingDelta
	}
}

func (r *recordingLifecycleMetric) DecElicitationPending() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingDelta--
}

func (r *recordingLifecycleMetric) IncElicitationTimeout() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timeouts++
}

func (r *recordingLifecycleMetric) IncElicitationSuppressed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.suppressed++
}

func (r *recordingLifecycleMetric) ObserveElicitationRoundtrip(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roundtrips = append(r.roundtrips, d)
}

// The accessors below read the recorded counters under the lock so a test
// polling them does not race the handler-goroutine writes above.

func (r *recordingLifecycleMetric) getPendingDelta() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pendingDelta
}

func (r *recordingLifecycleMetric) getPendingMax() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pendingMax
}

func (r *recordingLifecycleMetric) getTimeouts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.timeouts
}

func (r *recordingLifecycleMetric) getSuppressed() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.suppressed
}

func (r *recordingLifecycleMetric) roundtripCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.roundtrips)
}

// waitElicitationFor blocks until an elicitation is recorded against
// (sessionID, userID), or fails the test. The §9.2 dispatcher records
// an elicitation under the resolving user, so the wait is user-aware.
func waitElicitationFor(t *testing.T, store interactionstore.Store, sessionID, userID, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.Get(context.Background(), "acme", sessionID, userID, id); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("elicitation %s was never recorded against %s/%s", id, sessionID, userID)
}

// resolveAt responds to a pending elicitation recorded against the
// given session, the path a human at the chain terminus drives.
func resolveAt(t *testing.T, store interactionstore.Store, sessionID, userID, id string, response any) {
	t.Helper()
	if _, err := store.Resolve(context.Background(), "acme", sessionID, userID, id,
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseResponded
			i.Response = response
			return nil
		}); err != nil {
		t.Fatalf("resolve %s at %s: %v", id, sessionID, err)
	}
}

// TestElicitationForwardsUpMultipleHops proves a §9.2 elicitation
// raised by a delegated child forwards up the task tree to the
// human-facing root, where it is recorded for resolution.
func TestElicitationForwardsUpMultipleHops(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	// root → mid → leaf delegation tree; the leaf raises the elicitation.
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_mid", "alice", "sess_root")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_mid")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve?","schema":{},"elicitationId":"elic_x"}`)
	}()

	// §9.2: the elicitation forwarded up to the root — it is NOT
	// recorded against the raising leaf or the intermediate hop.
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")
	if _, err := interactions.Get(context.Background(), "acme", "sess_leaf", "alice", "elic_x"); err == nil {
		t.Error("the elicitation was recorded against the raising leaf, not the chain terminus")
	}
	if _, err := interactions.Get(context.Background(), "acme", "sess_mid", "alice", "elic_x"); err == nil {
		t.Error("the elicitation was recorded against an intermediate hop")
	}

	// The human at the root resolves it; the raising leaf unblocks.
	resolveAt(t, interactions, "sess_root", "alice", "elic_x", "yes")
	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "yes") {
			t.Errorf("request_elicitation result = %q, want the human response", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the root resolved it")
	}
}

// TestElicitationParentIntercepts proves the §9.2 chain terminates at
// an intercepting parent — the elicitation is recorded against that
// parent, not forwarded to the root.
func TestElicitationParentIntercepts(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{
		// The middle session intercepts the chain.
		intercepts: func(s sessionstore.Session) bool { return s.ID == "sess_mid" },
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_mid", "alice", "sess_root")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_mid")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve?","schema":{},"elicitationId":"elic_x"}`)
	}()

	// §9.2: the chain stopped at the intercepting parent — recorded
	// against sess_mid, never reaching the root.
	waitElicitationFor(t, interactions, "sess_mid", "alice", "elic_x")
	if _, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x"); err == nil {
		t.Error("the elicitation was forwarded past the intercepting parent to the root")
	}

	// The intercepting parent answers it.
	resolveAt(t, interactions, "sess_mid", "alice", "elic_x", "intercepted-answer")
	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "intercepted-answer") {
			t.Errorf("result = %q, want the intercepting parent's answer", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the parent intercepted it")
	}
}

// TestElicitationContentDigestRecorded proves the §9.2 gateway-origin
// content-integrity digest is computed and recorded so a forward-hop
// re-emission can be verified against the original {message, schema}.
func TestElicitationContentDigestRecorded(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve the deploy?","schema":{"type":"boolean"},"elicitationId":"elic_x"}`)
	}()
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")

	in, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x")
	if err != nil {
		t.Fatalf("get elicitation: %v", err)
	}
	gotDigest, _ := in.Detail["contentDigest"].(string)
	if gotDigest == "" {
		t.Fatal("the recorded elicitation carries no content-integrity digest")
	}
	// The recorded digest must equal the §9.2 canonical digest of the
	// original {message, schema} pair.
	want, err := elicitation.Content{
		Message: "approve the deploy?",
		Schema:  map[string]any{"type": "boolean"},
	}.Digest()
	if err != nil {
		t.Fatalf("reference digest: %v", err)
	}
	if gotDigest != want {
		t.Errorf("recorded digest = %q, want the canonical digest %q", gotDigest, want)
	}
	// A forward-hop re-emission of the unchanged content verifies; a
	// rewritten message is detected as a §9.2 tamper.
	if err := elicitation.VerifyContentAtHop("pod-mid", gotDigest,
		elicitation.Content{Message: "approve the deploy?", Schema: map[string]any{"type": "boolean"}}); err != nil {
		t.Errorf("an unchanged re-emission must verify against the recorded digest: %v", err)
	}
	if err := elicitation.VerifyContentAtHop("pod-mid", gotDigest,
		elicitation.Content{Message: "approve deploy to PROD?", Schema: map[string]any{"type": "boolean"}}); err == nil {
		t.Error("a rewritten message must fail the recorded-digest check")
	}
}

// newMCPForProvenance builds an elicitation-chain MCP server with a
// §15.1 event bus wired so the §9.2 provenance-stamping tests can
// inspect both the recorded interaction Detail and the
// elicitation_request SSE payload. The timeout is short so an
// unresolved request_elicitation goroutine does not linger. F-9.2.6.
func newMCPForProvenance(t *testing.T) (*mcp.Server, sessionstore.Store, interactionstore.Store, *sessionevents.Bus) {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	bus := sessionevents.NewBus(16)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:              store,
		Interactions:       interactions,
		Events:             bus,
		ElicitationTimeout: 2 * time.Second,
		IDFunc:             func() string { return "elic_gen" },
		TenantID:           "acme",
	})
	return srv, store, interactions, bus
}

// mkRuntimeSession seeds a session carrying a RuntimeRef so the §9.2
// origin_runtime provenance field has a value to stamp.
func mkRuntimeSession(t *testing.T, store sessionstore.Store, id, user, parent, runtime string) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: user, State: session.StateRunning,
		ParentSessionID: parent, RuntimeRef: runtime, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

// detailInt reads an integer Detail field tolerant of the int / float64
// representations a store may use after a JSON round-trip.
func detailInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// TestElicitationStampsProvenanceOnInteraction proves the §9.2
// provenance metadata (origin_pod, delegation_depth, origin_runtime,
// initiator_type) is stamped on the recorded elicitation so the §16.7
// audit row can source delegation_depth / initiator_type from the
// stored record and the §15.1 resolver UI can render provenance.
// spec: §9.2 lines 70–82; §16.7 line 674. F-9.2.6.
func TestElicitationStampsProvenanceOnInteraction_spec_9_2_F_9_2_6(t *testing.T) {
	srv, store, interactions, _ := newMCPForProvenance(t)
	mkUserSession(t, store, "sess_root", "alice", "")
	// sess_leaf is one delegation hop below the root and runs python-3.12.
	mkRuntimeSession(t, store, "sess_leaf", "alice", "sess_root", "python-3.12")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve the deploy?","schema":{"type":"boolean"},"elicitationId":"elic_x"}`)
	}()
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")

	in, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x")
	if err != nil {
		t.Fatalf("get elicitation: %v", err)
	}
	if got, _ := in.Detail["originPod"].(string); got != "sess_leaf" {
		t.Errorf("Detail originPod = %q, want sess_leaf", got)
	}
	if got, _ := in.Detail["initiatorType"].(string); got != "agent" {
		t.Errorf("Detail initiatorType = %q, want agent", got)
	}
	if got, ok := detailInt(in.Detail["delegationDepth"]); !ok || got != 1 {
		t.Errorf("Detail delegationDepth = %v, want 1 (leaf one hop below the root)", in.Detail["delegationDepth"])
	}
	if got, _ := in.Detail["originRuntime"].(string); got != "python-3.12" {
		t.Errorf("Detail originRuntime = %q, want python-3.12", got)
	}
	// Unblock the request_elicitation goroutine.
	resolveAt(t, interactions, "sess_root", "alice", "elic_x", true)
}

// TestElicitationStampsProvenanceOnEvent proves the §9.2 provenance
// reaches the client over the live event channel — the
// elicitation_request SSE payload carries origin_pod, initiator_type,
// delegation_depth, and origin_runtime so a resolver UI can display
// provenance prominently and distinguish a platform OAuth flow from an
// agent-initiated prompt. spec: §9.2 lines 70–82. F-9.2.6.
func TestElicitationStampsProvenanceOnEvent_spec_9_2_F_9_2_6(t *testing.T) {
	srv, store, interactions, bus := newMCPForProvenance(t)
	mkUserSession(t, store, "sess_root", "alice", "")
	mkRuntimeSession(t, store, "sess_leaf", "alice", "sess_root", "python-3.12")

	// Subscribe to the resolver session before raising so the live
	// elicitation_request event is delivered to this subscriber.
	sub := bus.Subscribe("sess_root", 0, 16)
	defer sub.Close()

	h := srv.Handler()
	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"approve the deploy?","schema":{"type":"boolean"},"elicitationId":"elic_x"}`)
	}()

	var ev sessionevents.Event
	select {
	case ev = <-sub.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("no elicitation_request event published to the resolver session")
	}
	if ev.Type != "elicitation_request" {
		t.Fatalf("event type = %q, want elicitation_request", ev.Type)
	}
	var payload struct {
		OriginPod       string `json:"originPod"`
		InitiatorType   string `json:"initiatorType"`
		DelegationDepth int    `json:"delegationDepth"`
		OriginRuntime   string `json:"originRuntime"`
	}
	if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
		t.Fatalf("decode event data: %v; data=%s", err, ev.Data)
	}
	if payload.OriginPod != "sess_leaf" {
		t.Errorf("event originPod = %q, want sess_leaf", payload.OriginPod)
	}
	if payload.InitiatorType != "agent" {
		t.Errorf("event initiatorType = %q, want agent", payload.InitiatorType)
	}
	if payload.DelegationDepth != 1 {
		t.Errorf("event delegationDepth = %d, want 1", payload.DelegationDepth)
	}
	if payload.OriginRuntime != "python-3.12" {
		t.Errorf("event originRuntime = %q, want python-3.12", payload.OriginRuntime)
	}
	// Unblock the request_elicitation goroutine.
	resolveAt(t, interactions, "sess_root", "alice", "elic_x", true)
}

// TestElicitationDepthSuppressed proves a §9.2 depth-suppressed
// elicitation returns a SUPPRESSED response and is never recorded
// anywhere in the chain.
func TestElicitationDepthSuppressed(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{
		depth:      elicitation.DepthSuppressAtDepth,
		suppressAt: 2,
		timeout:    time.Second,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_mid", "alice", "sess_root")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_mid") // depth 2

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"ask","schema":{},"elicitationId":"elic_x"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "suppressed") {
		t.Errorf("result = %q, want a SUPPRESSED response", text)
	}
	for _, sid := range []string{"sess_leaf", "sess_mid", "sess_root"} {
		if _, err := interactions.Get(context.Background(), "acme", sid, "alice", "elic_x"); err == nil {
			t.Errorf("a suppressed elicitation was recorded against %s", sid)
		}
	}
}

// TestElicitationLifecycleAdmitAndResolveBumpsMetrics_spec_16_1_F_9_2_14
// proves the §16.1 lifecycle hooks fire on the happy path: pending
// gauge +1 on admit, -1 on terminal, and one roundtrip observation
// covering the wall-clock from admit to resolve. F-9.2.14.
func TestElicitationLifecycleAdmitAndResolveBumpsMetrics_spec_16_1_F_9_2_14(t *testing.T) {
	lc := &recordingLifecycleMetric{}
	srv, store, interactions := newMCPForChain(t, chainOpts{
		lifecycle: lc,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"ok?","schema":{},"elicitationId":"elic_x"}`)
	}()
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")
	// Admit → pending gauge incremented to at least 1.
	if lc.getPendingMax() < 1 {
		t.Errorf("pendingMax = %d, want >= 1 (admit must Inc the pending gauge)", lc.getPendingMax())
	}
	resolveAt(t, interactions, "sess_root", "alice", "elic_x", "yes")
	// Wait until the dispatcher records terminal — Allow time for the
	// poll cycle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lc.roundtripCount() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if lc.roundtripCount() != 1 {
		t.Errorf("roundtrips = %d, want exactly 1", lc.roundtripCount())
	}
	if lc.getPendingDelta() != 0 {
		t.Errorf("pendingDelta = %d, want net 0 (Inc + Dec)", lc.getPendingDelta())
	}
	if lc.getTimeouts() != 0 {
		t.Errorf("timeouts = %d, want 0 on the resolved-happy path", lc.getTimeouts())
	}
}

// TestElicitationLifecycleTimeoutBumpsTimeoutCounter_spec_16_1_F_9_2_14
// proves the §16.1 line 63 timeout counter increments and pending
// decrements when the §9.1 maxElicitationWait fires. F-9.2.14.
func TestElicitationLifecycleTimeoutBumpsTimeoutCounter_spec_16_1_F_9_2_14(t *testing.T) {
	lc := &recordingLifecycleMetric{}
	srv, store, _ := newMCPForChain(t, chainOpts{
		lifecycle: lc,
		timeout:   80 * time.Millisecond,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"ok?","schema":{},"elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected ELICITATION_TIMEOUT error: %+v", resp)
	}
	if lc.getTimeouts() != 1 {
		t.Errorf("timeouts = %d, want 1 after maxElicitationWait fires", lc.getTimeouts())
	}
	if lc.getPendingDelta() != 0 {
		t.Errorf("pendingDelta = %d, want net 0 (Inc + Dec on timeout)", lc.getPendingDelta())
	}
	if lc.roundtripCount() != 1 {
		t.Errorf("roundtrips = %d, want 1 observation on timeout path", lc.roundtripCount())
	}
}

// TestElicitationLifecycleSuppressionBumpsSuppressedCounter_spec_16_1_F_9_2_14
// proves the §16.1 line 62 suppressed counter increments when a §9.2
// depth-policy suppression fires; the pending gauge is NOT touched
// because the suppression rejects before admit. F-9.2.14.
func TestElicitationLifecycleSuppressionBumpsSuppressedCounter_spec_16_1_F_9_2_14(t *testing.T) {
	lc := &recordingLifecycleMetric{}
	srv, store, _ := newMCPForChain(t, chainOpts{
		depth:      elicitation.DepthSuppressAtDepth,
		suppressAt: 1,
		lifecycle:  lc,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root") // depth 1

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"ok?","schema":{},"elicitationId":"elic_x"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "suppressed") {
		t.Fatalf("expected suppressed response, got %q", text)
	}
	if lc.getSuppressed() != 1 {
		t.Errorf("suppressed = %d, want 1 after depth suppression", lc.getSuppressed())
	}
	if lc.getPendingMax() != 0 {
		t.Errorf("pendingMax = %d, want 0 (suppression must reject before admit)", lc.getPendingMax())
	}
}

// TestURLModeElicitationAllowedDomain proves a §9.2 url-mode
// elicitation whose domain is on the connector allowlist proceeds.
// F-9.2.11: no §9.1 drop metric is incremented on the happy path.
func TestURLModeElicitationAllowedDomain(t *testing.T) {
	drops := &recordingDropMetric{}
	srv, store, interactions := newMCPForChain(t, chainOpts{
		urlMode: elicitation.URLModeAllowlist{
			Enabled:         true,
			DomainAllowlist: []string{"accounts.example.com"},
		},
		drops: drops,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_leaf","message":"sign in","schema":{},"url":"https://accounts.example.com/oauth/authorize","elicitationId":"elic_x"}`)
	}()
	// An allowlisted url-mode elicitation is recorded — it forwarded up
	// the chain normally.
	waitElicitationFor(t, interactions, "sess_root", "alice", "elic_x")
	if len(drops.reasons) != 0 {
		t.Errorf("an allowed url-mode elicitation incremented drop counter: %+v", drops.reasons)
	}
}

// TestURLModeElicitationDisallowedDomain proves a §9.2 url-mode
// elicitation whose domain is not on the connector allowlist is
// dropped with DOMAIN_NOT_ALLOWLISTED and increments the
// `domain_not_allowlisted` drop counter. The drop also writes the
// §16.7 elicitation.url_mode_domain_rejected audit row; that audit
// payload is asserted in the internal dispatcher test
// TestDispatchURLModeDropWritesAuditRow_spec_16_7 (F-EL3).
//
// spec: §9.2 line 86; §16.7 (elicitation.url_mode_domain_rejected).
// F-9.2.11, F-EL3.
func TestURLModeElicitationDisallowedDomain_spec_9_2_F_9_2_11(t *testing.T) {
	drops := &recordingDropMetric{}
	srv, store, interactions := newMCPForChain(t, chainOpts{
		urlMode: elicitation.URLModeAllowlist{
			Enabled:         true,
			DomainAllowlist: []string{"accounts.example.com"},
		},
		drops: drops,
	})
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_leaf", "alice", "sess_root")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"sign in","schema":{},"url":"https://phish.evil.test/login","elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a disallowed-domain url-mode elicitation should be a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if msg, _ := c0["text"].(string); !strings.Contains(msg, "DOMAIN_NOT_ALLOWLISTED") {
		t.Errorf("error = %q, want DOMAIN_NOT_ALLOWLISTED", msg)
	}
	// F-9.2.11: exactly one §9.1 drop, reason="domain_not_allowlisted".
	if len(drops.reasons) != 1 || drops.reasons[0] != "domain_not_allowlisted" {
		t.Errorf("drop reasons = %v, want [domain_not_allowlisted]", drops.reasons)
	}
	// The dropped elicitation was not recorded.
	if _, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x"); err == nil {
		t.Error("a dropped url-mode elicitation was recorded in the interaction store")
	}
}

// TestURLModeElicitationAgentBlockedByDefault proves §9.2 control 1:
// an agent-initiated url-mode elicitation is blocked when the pool
// does not allowlist url-mode at all. F-9.2.11: the rejection
// increments the §9.1 drop counter with reason="domain_not_allowlisted"
// (the disabled-allowlist case shares the metric reason with the
// unmatched-domain case).
func TestURLModeElicitationAgentBlockedByDefault(t *testing.T) {
	drops := &recordingDropMetric{}
	// No urlMode allowlist configured — the §9.2 default blocks
	// agent-initiated url-mode.
	srv, store, _ := newMCPForChain(t, chainOpts{drops: drops})
	mkUserSession(t, store, "sess_root", "alice", "")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_root","message":"sign in","schema":{},"url":"https://accounts.example.com/oauth","elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an agent url-mode elicitation should be blocked by default: %+v", resp)
	}
	if len(drops.reasons) != 1 || drops.reasons[0] != "domain_not_allowlisted" {
		t.Errorf("drop reasons = %v, want one [domain_not_allowlisted]", drops.reasons)
	}
}

// TestRequestElicitationDropsSelfAssertedConnector_spec_9_2 proves the
// F-9.2.19 hardening: an agent pod cannot self-declare
// `initiatorType: "connector"` through `lenny/request_elicitation` to
// bypass the agent-initiated url-mode allowlist. The field was removed
// from the tool's input schema; even when a legacy client supplies it,
// the gateway treats the elicitation as agent-initiated and rejects it
// against the empty allowlist with the standard §11.7 url_mode_disabled
// audit event.
//
// spec: §9.2 lines 87–88 (agent binaries cannot self-declare as a
// connector); F-9.2.19.
func TestRequestElicitationDropsSelfAssertedConnector_spec_9_2(t *testing.T) {
	drops := &recordingDropMetric{}
	srv, store, interactions := newMCPForChain(t, chainOpts{drops: drops})
	mkUserSession(t, store, "sess_root", "alice", "")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_root","message":"sign in","schema":{},"url":"https://github.com/login/oauth/authorize","initiatorType":"connector","elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a self-asserted connector url-mode elicitation must not be admitted: %+v", resp)
	}
	if len(drops.reasons) != 1 || drops.reasons[0] != "domain_not_allowlisted" {
		t.Errorf("drop reasons = %v, want one [domain_not_allowlisted] (agent-initiated path)", drops.reasons)
	}
	// The dropped elicitation was never recorded against the resolver.
	if _, err := interactions.Get(context.Background(), "acme", "sess_root", "alice", "elic_x"); err == nil {
		t.Error("a dropped self-asserted-connector elicitation must not be recorded")
	}
}

// TestResolveElicitationAuthorizedResolver proves the §9.2/§15.1
// respond-authorization triple admits the authorized resolver.
func TestResolveElicitationAuthorizedResolver(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	out, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_root", "alice", "elic_x",
		interactionstore.PhaseResponded, "yes", "")
	if err != nil {
		t.Fatalf("the authorized resolver must succeed: %v", err)
	}
	if out.Phase != interactionstore.PhaseResponded || out.Response != "yes" {
		t.Errorf("resolved interaction = %+v", out)
	}
}

// TestResolveElicitationWrongUserRejected proves the §9.2 triple
// rejects a resolver whose user id does not match — returning the
// not-found condition rather than leaking the elicitation.
func TestResolveElicitationWrongUserRejected(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	// bob is not the user the elicitation was directed at.
	_, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_root", "bob", "elic_x",
		interactionstore.PhaseResponded, "yes", "")
	if err != mcptools.ErrElicitationNotFound {
		t.Fatalf("err = %v, want ErrElicitationNotFound for a wrong-user resolve", err)
	}
}

// TestResolveElicitationWrongSessionRejected proves the §9.2 triple
// rejects a resolution that targets a session the elicitation was
// not issued against (a non-resolver session).
func TestResolveElicitationWrongSessionRejected(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	// The elicitation was issued against the chain resolver sess_root.
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	// A resolution that names a different session — the raising leaf —
	// must not match.
	_, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_leaf", "alice", "elic_x",
		interactionstore.PhaseResponded, "yes", "")
	if err != mcptools.ErrElicitationNotFound {
		t.Fatalf("err = %v, want ErrElicitationNotFound for a wrong-session resolve", err)
	}
}

// TestResolveElicitationWrongKindRejected proves a tool_call_id used
// on the §9.2 elicitation resolution path is rejected as not found.
func TestResolveElicitationWrongKindRejected(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "tc_x", Kind: interactionstore.KindToolUse,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	_, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_root", "alice", "tc_x",
		interactionstore.PhaseResponded, "yes", "")
	if err != mcptools.ErrElicitationNotFound {
		t.Fatalf("err = %v, want ErrElicitationNotFound for a tool-use interaction", err)
	}
}

// TestResolveElicitationDismiss proves the §9.2 dismiss path records
// the dismissal and reason through the authorized triple.
func TestResolveElicitationDismiss(t *testing.T) {
	store := interactionstore.NewMemory()
	ctx := context.Background()
	if err := store.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	out, err := mcptools.ResolveElicitation(ctx, store, "acme", "sess_root", "alice", "elic_x",
		interactionstore.PhaseDismissed, nil, "user_cancelled")
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if out.Phase != interactionstore.PhaseDismissed || out.Reason != "user_cancelled" {
		t.Errorf("dismissed interaction = %+v", out)
	}
}

// TestMCPRespondToElicitationResolves proves the §9.2 line 108 MCP
// `lenny/respond_to_elicitation` tool resolves a pending elicitation
// when the (sessionId, sessionUserID, elicitationId) triple matches.
// F-9.2.17.
func TestMCPRespondToElicitationResolves(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	ctx := context.Background()
	if err := interactions.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/respond_to_elicitation",
		`{"sessionId":"sess_root","elicitationId":"elic_x","response":"option-A"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "responded") {
		t.Errorf("respond result = %q, want a responded phase", text)
	}
	cur, err := interactions.Get(ctx, "acme", "sess_root", "alice", "elic_x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur.Phase != interactionstore.PhaseResponded || cur.Response != "option-A" {
		t.Errorf("resolved interaction = %+v", cur)
	}
}

// TestMCPRespondToElicitationTripleMismatchSurfacesNotFound verifies
// §9.2 line 108: a (sessionId, userId, elicitationId) mismatch
// collapses to ELICITATION_NOT_FOUND on the MCP surface, mirroring the
// §15.1 REST handler so the existence of another session's elicitation
// never leaks. F-9.2.17.
func TestMCPRespondToElicitationTripleMismatchSurfacesNotFound(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	// The elicitation is bound to alice's session; bob's session targets
	// the same elicitation id and must collapse to not-found.
	mkUserSession(t, store, "sess_root", "alice", "")
	mkUserSession(t, store, "sess_bob", "bob", "")
	ctx := context.Background()
	if err := interactions.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/respond_to_elicitation",
		`{"sessionId":"sess_bob","elicitationId":"elic_x","response":"option-A"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a wrong-session resolve must be a tool error: %+v", resp)
	}
	envelope := readErrorEnvelope(t, result)
	if envelope["code"] != "ELICITATION_NOT_FOUND" {
		t.Errorf("envelope.code = %v, want ELICITATION_NOT_FOUND", envelope["code"])
	}
}

// TestMCPDismissElicitation proves the §9.2 line 108 MCP
// `lenny/dismiss_elicitation` tool records the dismissal with the
// caller-supplied reason. F-9.2.17.
func TestMCPDismissElicitation(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	ctx := context.Background()
	if err := interactions.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	_ = call(t, srv.Handler(), "lenny/dismiss_elicitation",
		`{"sessionId":"sess_root","elicitationId":"elic_x","reason":"user_cancelled"}`)
	cur, err := interactions.Get(ctx, "acme", "sess_root", "alice", "elic_x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur.Phase != interactionstore.PhaseDismissed || cur.Reason != "user_cancelled" {
		t.Errorf("dismissed interaction = %+v", cur)
	}
}

// TestMCPRespondToElicitationAlreadyResolvedSurfacesConflict verifies a
// second resolution attempt against an interaction the store has
// already settled surfaces the §15.1 INTERACTION_ALREADY_RESOLVED
// envelope rather than crashing or silently overwriting. F-9.2.17.
func TestMCPRespondToElicitationAlreadyResolvedSurfacesConflict(t *testing.T) {
	srv, store, interactions := newMCPForChain(t, chainOpts{})
	mkUserSession(t, store, "sess_root", "alice", "")
	ctx := context.Background()
	if err := interactions.Put(ctx, interactionstore.Interaction{
		ID: "elic_x", Kind: interactionstore.KindElicitation,
		SessionID: "sess_root", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := interactions.Resolve(ctx, "acme", "sess_root", "alice", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseResponded
			i.Response = "first"
			return nil
		}); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/respond_to_elicitation",
		`{"sessionId":"sess_root","elicitationId":"elic_x","response":"second"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a second resolution must be a tool error: %+v", resp)
	}
	envelope := readErrorEnvelope(t, result)
	if envelope["code"] != "INTERACTION_ALREADY_RESOLVED" {
		t.Errorf("envelope.code = %v, want INTERACTION_ALREADY_RESOLVED", envelope["code"])
	}
}

// readErrorEnvelope decodes the §15.2.1 lenny error envelope (code,
// category, retryable, message, details) from the `lenny/error`
// content block of an isError MCP tool result.
func readErrorEnvelope(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] != "lenny/error" {
			continue
		}
		text, _ := block["text"].(string)
		var env map[string]any
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		return env
	}
	t.Fatalf("no lenny/error block in %+v", content)
	return nil
}
