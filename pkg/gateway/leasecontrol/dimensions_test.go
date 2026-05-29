// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// dimsTreeConfig registers a tree with non-zero current values and
// generous ceilings on every §8.6 line 643 extendable dimension, so a
// test can exercise the new children / parallel-children / tree-size /
// fileExportLimits dimensions end to end. F-8.6.1.
func dimsTreeConfig() leasecontrol.TreeConfig {
	return leasecontrol.TreeConfig{
		TenantID:                     "acme",
		CurrentTokenBudget:           100_000,
		DeploymentBase:               500_000,
		DeploymentMax:                2_000_000,
		CurrentMaxAgeSeconds:         600,
		EffectiveMaxAgeSeconds:       3_600,
		CurrentChildren:              2,
		EffectiveMaxChildren:         10,
		CurrentParallelChildren:      1,
		EffectiveMaxParallelChildren: 5,
		CurrentTreeSize:              3,
		EffectiveMaxTreeSize:         20,
		CurrentFileExportFiles:       4,
		EffectiveMaxFileExportFiles:  16,
		CurrentFileExportBytes:       1_000,
		EffectiveMaxFileExportBytes:  10_000,
	}
}

// spec: §8.6 line 643 — every extendable dimension (children,
// parallel-children, tree-size, and both fileExportLimits components)
// is requestable on the wire and grantable by the gateway, alongside
// tokens and seconds. F-8.6.1.
func TestExtendLeaseAllDimensionsGranted_Spec8_6_643(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", dimsTreeConfig())
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:                 &adapterv1.SessionId{Value: "root-1"},
		RequestedTokens:           50_000,
		RequestedSeconds:          1_200,
		RequestedChildren:         3,
		RequestedParallelChildren: 2,
		RequestedTreeSize:         5,
		RequestedFileExportLimits: &adapterv1.FileExportLimitsDelta{
			AdditionalMaxFiles: 4,
			AdditionalMaxBytes: 2_000,
		},
	})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_GRANTED {
		t.Fatalf("status = %v, want GRANTED", resp.Status)
	}
	if resp.GrantedTokens != 50_000 || resp.GrantedSeconds != 1_200 {
		t.Errorf("tokens/seconds granted = (%d, %d), want (50000, 1200)", resp.GrantedTokens, resp.GrantedSeconds)
	}
	if resp.GrantedChildren != 3 || resp.GrantedParallelChildren != 2 || resp.GrantedTreeSize != 5 {
		t.Errorf("children/parallel/tree granted = (%d, %d, %d), want (3, 2, 5)",
			resp.GrantedChildren, resp.GrantedParallelChildren, resp.GrantedTreeSize)
	}
	fe := resp.GetGrantedFileExportLimits()
	if fe.GetAdditionalMaxFiles() != 4 || fe.GetAdditionalMaxBytes() != 2_000 {
		t.Errorf("file-export granted = (%d, %d), want (4, 2000)", fe.GetAdditionalMaxFiles(), fe.GetAdditionalMaxBytes())
	}

	// Each dimension's current value rose by its grant.
	tb, err := budgets.TreeBudget(context.Background(), "acme", "root-1")
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	want := leasecontrol.Dimensions{
		Tokens: 150_000, Seconds: 1_800, Children: 5,
		ParallelChildren: 3, TreeSize: 8, FileExportFiles: 8, FileExportBytes: 3_000,
	}
	if tb.Current != want {
		t.Errorf("post-grant current = %+v, want %+v", tb.Current, want)
	}
}

// spec: §8.6 line 643 — dimensions are independent: a dimension already
// at its ceiling reports CEILING_REACHED for itself while the others
// still grant, so the response is PARTIALLY_GRANTED. F-8.6.1; F-8.6.11.
func TestExtendLeaseDimensionsIndependent_Spec8_6_643(t *testing.T) {
	cfg := dimsTreeConfig()
	cfg.CurrentChildren = 10 // already at EffectiveMaxChildren=10: no headroom
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", cfg)
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:         &adapterv1.SessionId{Value: "root-1"},
		RequestedChildren: 2, // hits the children ceiling → zero grant
		RequestedTreeSize: 5, // has headroom → granted
	})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED {
		t.Fatalf("status = %v, want PARTIALLY_GRANTED", resp.Status)
	}
	if resp.GrantedChildren != 0 {
		t.Errorf("children granted = %d, want 0 (ceiling reached)", resp.GrantedChildren)
	}
	if resp.GrantedTreeSize != 5 {
		t.Errorf("tree-size granted = %d, want 5 (independent of children)", resp.GrantedTreeSize)
	}
}

// spec: §8.6 line 643 — a request whose only requested dimension is at
// its ceiling reports CEILING_REACHED for the whole response (the
// adapter MUST treat it as terminal). F-8.6.1.
func TestExtendLeaseSingleDimensionCeiling_Spec8_6_643(t *testing.T) {
	cfg := dimsTreeConfig()
	cfg.CurrentTreeSize = 20 // == EffectiveMaxTreeSize: no headroom
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", cfg)
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:         &adapterv1.SessionId{Value: "root-1"},
		RequestedTreeSize: 4,
	})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_CEILING_REACHED {
		t.Fatalf("status = %v, want CEILING_REACHED", resp.Status)
	}
	if resp.GetGrantedFileExportLimits() != nil {
		t.Errorf("granted file-export limits = %v, want nil (none requested)", resp.GetGrantedFileExportLimits())
	}
}

// spec: §8.6 line 648 — the parent's own lease caps a child's extension
// on every dimension, not just tokens. A child whose parent granted 4
// max children cannot extend past 4 even when the tree ceiling is 10.
// F-8.6.1; F-8.6.15.
func TestExtendLeaseParentCeilingOnNewDimension_Spec8_6_648(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", dimsTreeConfig())
	budgets.AddSession("child-2", "root-1", "acme")
	// Parent lease granted at most 4 children; current is 2 → headroom 2.
	budgets.SetParentLease("child-2", leasecontrol.SessionLease{ChildrenCeiling: 4})
	svc := newService(t, budgets, nil)

	resp, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:         &adapterv1.SessionId{Value: "child-2"},
		RequestedChildren: 5, // capped by parent ceiling (4) to a grant of 2
	})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if resp.Status != adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED {
		t.Fatalf("status = %v, want PARTIALLY_GRANTED", resp.Status)
	}
	if resp.GrantedChildren != 2 {
		t.Errorf("children granted = %d, want 2 (parent ceiling 4 − current 2)", resp.GrantedChildren)
	}
}

// spec: §8.6 line 743 — the audit record carries every requested and
// granted dimension, not just tokens. F-8.6.1.
func TestExtendLeaseAuditCarriesAllDimensions_Spec8_6_743(t *testing.T) {
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree("root-1", dimsTreeConfig())
	rec := &recordingAuditor{}
	svc, err := leasecontrol.NewService(leasecontrol.Options{
		Budgets: budgets, Tenants: budgets, Auditing: rec,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.ExtendLease(context.Background(), &adapterv1.ExtendLeaseRequest{
		SessionId:         &adapterv1.SessionId{Value: "root-1"},
		RequestedChildren: 3,
		RequestedTreeSize: 5,
	}); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if len(rec.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(rec.entries))
	}
	e := rec.entries[0]
	if e.Requested.Children != 3 || e.Granted.Children != 3 {
		t.Errorf("audit children = (req %d, grant %d), want (3, 3)", e.Requested.Children, e.Granted.Children)
	}
	if e.Requested.TreeSize != 5 || e.Granted.TreeSize != 5 {
		t.Errorf("audit tree-size = (req %d, grant %d), want (5, 5)", e.Requested.TreeSize, e.Granted.TreeSize)
	}
	if e.NewLimits.Children != 5 || e.NewLimits.TreeSize != 8 {
		t.Errorf("audit new-limits children/tree = (%d, %d), want (5, 8)", e.NewLimits.Children, e.NewLimits.TreeSize)
	}
}
