// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// slowAncestorStore satisfies sessionstore.Store but blocks Get on the
// designated parent id past the caller's deadline so the §11.3 line 211
// per-hop forwarding timeout can be exercised deterministically. Other
// store methods are not used by the elicitation chain walk and panic if
// reached so a future refactor that depends on them surfaces here.
type slowAncestorStore struct {
	leaf, mid sessionstore.Session
	slowID    string
}

func (s *slowAncestorStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	switch id {
	case s.leaf.ID:
		return s.leaf, nil
	case s.mid.ID:
		if id == s.slowID {
			<-ctx.Done()
			return sessionstore.Session{}, ctx.Err()
		}
		return s.mid, nil
	default:
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
}

func (s *slowAncestorStore) GetByID(context.Context, string) (sessionstore.Session, error) {
	panic("unused")
}
func (s *slowAncestorStore) Create(context.Context, sessionstore.Session) error {
	panic("unused")
}
func (s *slowAncestorStore) Update(context.Context, string, string, func(*sessionstore.Session) error) (sessionstore.Session, error) {
	panic("unused")
}
func (s *slowAncestorStore) List(context.Context, string, sessionstore.ListFilter) ([]sessionstore.Session, error) {
	panic("unused")
}
func (s *slowAncestorStore) ListByRoot(context.Context, string, string) ([]sessionstore.Session, error) {
	panic("unused")
}
func (s *slowAncestorStore) Delete(context.Context, string, string) error { panic("unused") }
func (s *slowAncestorStore) DeleteByUser(context.Context, string, string) (int, error) {
	panic("unused")
}
func (s *slowAncestorStore) DeleteByTenant(context.Context, string) (int, error) {
	panic("unused")
}
func (s *slowAncestorStore) GetActiveSlotsByPod(context.Context, string) (int, error) {
	panic("unused")
}
func (s *slowAncestorStore) PoolDrainStats(context.Context, string) (int, time.Time, error) {
	panic("unused")
}
func (s *slowAncestorStore) CountActiveSessions(context.Context, string) (int, error) {
	panic("unused")
}
func (s *slowAncestorStore) CountActiveSessionsByUser(context.Context, string, string) (int, error) {
	panic("unused")
}
func (s *slowAncestorStore) CountActiveSessionsByRuntime(context.Context, string, string) (int, error) {
	panic("unused")
}
func (s *slowAncestorStore) CountActiveSessionsGlobal(context.Context) (int, error) {
	panic("unused")
}
func (s *slowAncestorStore) CountActiveDelegatedChildrenByUser(context.Context, string, string) (int, error) {
	panic("unused")
}

// TestElicitationPerHopForwardingTimeoutDefaultConstant_spec_11_3_211
// pins the §11.3 line 211 / §9.1 line 104 hard-coded 30s default.
// F-11.3.16: changing the constant requires a spec edit, so guard it
// with a literal-value assertion at the test layer.
func TestElicitationPerHopForwardingTimeoutDefaultConstant_spec_11_3_211(t *testing.T) {
	if ElicitationPerHopForwardingTimeout != 30*time.Second {
		t.Fatalf("ElicitationPerHopForwardingTimeout = %s, want 30s per §11.3 line 211",
			ElicitationPerHopForwardingTimeout)
	}
}

// TestBuildHopsAncestorLookupTimeoutReturnsPerHopError_spec_11_3_211
// proves that when a forwarding hop's ancestor lookup stalls past the
// per-hop deadline, buildHops surfaces ELICITATION_PER_HOP_TIMEOUT
// with the failing hop's session id in the error details. F-11.3.16.
func TestBuildHopsAncestorLookupTimeoutReturnsPerHopError_spec_11_3_211(t *testing.T) {
	now := time.Now()
	leaf := sessionstore.Session{
		ID: "sess_leaf", TenantID: "acme", UserID: "alice",
		ParentSessionID: "sess_mid",
		CreatedAt:       now, UpdatedAt: now,
	}
	mid := sessionstore.Session{
		ID: "sess_mid", TenantID: "acme", UserID: "alice",
		ParentSessionID: "sess_root",
		CreatedAt:       now, UpdatedAt: now,
	}
	store := &slowAncestorStore{leaf: leaf, mid: mid, slowID: "sess_mid"}
	d := &elicitationDispatcher{
		store:         store,
		perHopTimeout: 10 * time.Millisecond,
	}
	_, err := d.buildHops(context.Background(), "acme", leaf)
	if err == nil {
		t.Fatalf("buildHops returned nil error, want ELICITATION_PER_HOP_TIMEOUT")
	}
	var toolErr *mcp.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("buildHops error = %T (%v), want *mcp.ToolError", err, err)
	}
	if toolErr.Code != "ELICITATION_PER_HOP_TIMEOUT" {
		t.Fatalf("toolErr.Code = %q, want ELICITATION_PER_HOP_TIMEOUT", toolErr.Code)
	}
	if got := toolErr.Details["hopSessionId"]; got != "sess_leaf" {
		t.Errorf("details.hopSessionId = %v, want sess_leaf (the pod whose forward stalled)", got)
	}
	if got := toolErr.Details["timeout"]; got != (10 * time.Millisecond).String() {
		t.Errorf("details.timeout = %v, want 10ms", got)
	}
	if got, ok := toolErr.Details["timeoutSeconds"].(float64); !ok || got != 0.01 {
		t.Errorf("details.timeoutSeconds = %v (%T), want 0.01 (float64)", got, toolErr.Details["timeoutSeconds"])
	}
}

// TestBuildHopsFastAncestorLookupSucceeds_spec_11_3_211 proves the
// per-hop timeout does not regress the happy path: a chain whose
// lookups all complete well within the deadline assembles cleanly.
// F-11.3.16.
func TestBuildHopsFastAncestorLookupSucceeds_spec_11_3_211(t *testing.T) {
	now := time.Now()
	leaf := sessionstore.Session{ID: "sess_leaf", TenantID: "acme", UserID: "alice", ParentSessionID: "sess_mid", CreatedAt: now, UpdatedAt: now}
	mid := sessionstore.Session{ID: "sess_mid", TenantID: "acme", UserID: "alice", ParentSessionID: "", CreatedAt: now, UpdatedAt: now}
	store := &slowAncestorStore{leaf: leaf, mid: mid, slowID: ""}
	d := &elicitationDispatcher{
		store:         store,
		perHopTimeout: 5 * time.Second,
	}
	hops, err := d.buildHops(context.Background(), "acme", leaf)
	if err != nil {
		t.Fatalf("buildHops error: %v", err)
	}
	if len(hops) != 2 {
		t.Fatalf("len(hops) = %d, want 2 (leaf + root)", len(hops))
	}
	if hops[0].SessionID != "sess_leaf" || !hops[1].IsHuman {
		t.Errorf("hops layout unexpected: %+v", hops)
	}
}

// TestEffectivePerHopTimeoutDefaultsToConstant_spec_11_3_211 proves
// the dispatcher applies the §11.3 line 211 30s default when the test
// override is unset.
func TestEffectivePerHopTimeoutDefaultsToConstant_spec_11_3_211(t *testing.T) {
	d := &elicitationDispatcher{}
	if got := d.effectivePerHopTimeout(); got != ElicitationPerHopForwardingTimeout {
		t.Errorf("effectivePerHopTimeout() = %s, want %s", got, ElicitationPerHopForwardingTimeout)
	}
}

// TestEffectivePerHopTimeoutHonorsOverride_spec_11_3_211 proves the
// production binary can swap the deadline (for example to enable a
// tighter cap in tests).
func TestEffectivePerHopTimeoutHonorsOverride_spec_11_3_211(t *testing.T) {
	d := &elicitationDispatcher{perHopTimeout: 7 * time.Second}
	if got := d.effectivePerHopTimeout(); got != 7*time.Second {
		t.Errorf("effectivePerHopTimeout() = %s, want 7s", got)
	}
}

// silence the unused-import linter: the elicitation package is used by
// the production code under test but not by these helpers directly.
var _ = elicitation.DepthAllowAll
