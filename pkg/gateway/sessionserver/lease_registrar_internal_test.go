// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fakeTreeRegistrar captures the §8.6 RegisterTree calls registerLeaseTree
// makes so the wiring tests can assert a root session is registered with
// the resolved deployment-level config. F-15.3.5.
type fakeTreeRegistrar struct {
	calls []treeCall
}

type treeCall struct {
	rootSessionID string
	cfg           leasecontrol.TreeConfig
}

func (f *fakeTreeRegistrar) RegisterTree(rootSessionID string, cfg leasecontrol.TreeConfig) {
	f.calls = append(f.calls, treeCall{rootSessionID, cfg})
}

func defaultsForTest() LeaseExtensionDefaults {
	return LeaseExtensionDefaults{
		DeploymentBudget:    500_000,
		DeploymentMaxBudget: 2_000_000,
		ApprovalMode:        leasecontrol.ApprovalModeAuto,
		SuccessCoolOff:      5 * time.Second,
		RejectionCoolOff:    300 * time.Second,
		AutoMaxPerMinute:    12,
	}
}

// spec: §8.6 lines 660-678 — a newly created root session registers a
// lease-extension budget tree seeded with the deployment-level defaults,
// so the first adapter ExtendLease resolves the tree instead of failing
// ErrSessionNotFound. F-15.3.5.
func TestRegisterLeaseTree_RootSession_spec_8_6_line_660(t *testing.T) {
	reg := &fakeTreeRegistrar{}
	s := &Server{leaseRegistrar: reg, leaseExtDefaults: defaultsForTest()}

	s.registerLeaseTree(sessionstore.Session{ID: "sess_root", TenantID: "acme"})

	if len(reg.calls) != 1 {
		t.Fatalf("RegisterTree calls = %d, want 1", len(reg.calls))
	}
	call := reg.calls[0]
	if call.rootSessionID != "sess_root" {
		t.Fatalf("rootSessionID = %q, want sess_root", call.rootSessionID)
	}
	want := leasecontrol.TreeConfig{
		TenantID:         "acme",
		DeploymentBase:   500_000,
		DeploymentMax:    2_000_000,
		ApprovalMode:     leasecontrol.ApprovalModeAuto,
		SuccessCoolOff:   5 * time.Second,
		RejectionCoolOff: 300 * time.Second,
		AutoMaxPerMinute: 12,
	}
	if call.cfg != want {
		t.Fatalf("TreeConfig = %+v, want %+v", call.cfg, want)
	}
}

// A root row carrying a granted DelegationLease seeds the tree's current
// per-dimension values from it, so an extension raises the present limit
// toward the ceiling rather than from zero. F-15.3.5.
func TestRegisterLeaseTree_SeedsCurrentFromGrantedLease(t *testing.T) {
	reg := &fakeTreeRegistrar{}
	s := &Server{leaseRegistrar: reg, leaseExtDefaults: defaultsForTest()}

	s.registerLeaseTree(sessionstore.Session{
		ID: "sess_root", TenantID: "acme",
		DelegationLease: &sessionstore.DelegationLease{
			MaxTokenBudget: 100_000, MaxChildrenTotal: 8, MaxTreeSize: 16,
			MaxParallelChildren: 3, PerChildMaxAge: 900,
		},
	})

	if len(reg.calls) != 1 {
		t.Fatalf("RegisterTree calls = %d, want 1", len(reg.calls))
	}
	cfg := reg.calls[0].cfg
	if cfg.CurrentTokenBudget != 100_000 || cfg.CurrentChildren != 8 ||
		cfg.CurrentTreeSize != 16 || cfg.CurrentParallelChildren != 3 ||
		cfg.CurrentMaxAgeSeconds != 900 {
		t.Fatalf("current dimensions not seeded from lease: %+v", cfg)
	}
}

// spec: §8.6 line 648 — a delegated child (ParentSessionID set) is
// registered by the delegation Service, not the session server; the
// session-server helper skips it to avoid registering a child as a fresh
// root tree. F-15.3.5.
func TestRegisterLeaseTree_SkipsDelegatedChild_spec_8_6_line_648(t *testing.T) {
	reg := &fakeTreeRegistrar{}
	s := &Server{leaseRegistrar: reg, leaseExtDefaults: defaultsForTest()}

	s.registerLeaseTree(sessionstore.Session{
		ID: "sess_child", TenantID: "acme", ParentSessionID: "sess_root",
	})

	if len(reg.calls) != 0 {
		t.Fatalf("RegisterTree calls = %d, want 0 (child skipped)", len(reg.calls))
	}
}

// A nil registrar (no GatewayControl listener) makes registerLeaseTree a
// no-op rather than panicking. F-15.3.5.
func TestRegisterLeaseTree_NilRegistrar_NoPanic(t *testing.T) {
	s := &Server{leaseExtDefaults: defaultsForTest()}
	s.registerLeaseTree(sessionstore.Session{ID: "sess_root", TenantID: "acme"})
}
