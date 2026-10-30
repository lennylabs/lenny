// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// leaseFlags returns gateway flags carrying the lease-extension values.
func leaseFlags(grpcAddr string) *gatewayFlags {
	budget, maxBudget := int64(500_000), int64(2_000_000)
	approval := string(leasecontrol.ApprovalModeAuto)
	coolOff, rejection, perMin := 7, 90, 3
	return &gatewayFlags{
		grpcAddr:                 &grpcAddr,
		leaseDefaultBudget:       &budget,
		leaseMaxBudget:           &maxBudget,
		leaseDefaultApproval:     &approval,
		leaseCoolOffSec:          &coolOff,
		leaseRejectionCoolOffSec: &rejection,
		leaseAutoMaxPerMin:       &perMin,
	}
}

// TestLeaseExtensionConfigFromFlags pins that the deployment-level §8.6
// configuration the store-backed source serves every tree with is read
// from the gateway flags.
//
// spec: §8.6
func TestLeaseExtensionConfigFromFlags(t *testing.T) {
	got := leaseExtensionConfig(leaseFlags(""))
	want := leasecontrol.ExtensionConfig{
		DeploymentBudget: 500_000, DeploymentMaxBudget: 2_000_000,
		ApprovalMode:   leasecontrol.ApprovalModeAuto,
		SuccessCoolOff: 7 * time.Second, RejectionCoolOff: 90 * time.Second,
		AutoMaxPerMinute: 3,
	}
	if got != want {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

// TestNewLeaseBudgetSourceReadsSessionStore pins that the gateway builds no
// source without a GatewayControl listener, and that with one the source
// reads its rows from the gateway session store, so a session the replica
// never registered resolves.
//
// spec: §8.6 (Extension state)
func TestNewLeaseBudgetSourceReadsSessionStore(t *testing.T) {
	w := &gatewayWiring{f: leaseFlags("")}
	if src, err := w.newLeaseBudgetSource(w.newLeaseDenialStore()); err != nil || src != nil {
		t.Fatalf("no listener: source = %v, err = %v, want nil and no error", src, err)
	}

	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: "sess-1", TenantID: "acme", UserID: "alice@acme.com", State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 100_000},
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	w = &gatewayWiring{f: leaseFlags("127.0.0.1:0")}
	w.sessions = sessions
	src, err := w.newLeaseBudgetSource(w.newLeaseDenialStore())
	if err != nil || src == nil {
		t.Fatalf("listener: source = %v, err = %v, want a source", src, err)
	}
	tb, err := src.TreeBudget(context.Background(), "acme", "sess-1")
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	if tb.Current.Tokens != 100_000 || tb.EffectiveMax.Tokens != 500_000 {
		t.Fatalf("TreeBudget = current %d max %d, want 100000 500000", tb.Current.Tokens, tb.EffectiveMax.Tokens)
	}
}

// failingGrants is a DenialStore whose Grants read fails.
type failingGrants struct{ leasecontrol.DenialStore }

func (failingGrants) Grants(context.Context, string, string) (leasecontrol.Dimensions, error) {
	return leasecontrol.Dimensions{}, errors.New("postgres down")
}

// TestTreeGrantReaderReadsTheSourcesGrants pins that the delegate_task token
// cap reads the cumulative token grant from the same DenialStore the lease
// budget source records grants in, and that a read failure is returned for
// the reservation to fail closed.
//
// spec: §8.3, §8.6 (Extension state)
func TestTreeGrantReaderReadsTheSourcesGrants(t *testing.T) {
	ctx := context.Background()
	w := &gatewayWiring{f: leaseFlags("")}
	denials := w.newLeaseDenialStore()
	r := treeGrantReader{denials: denials}
	if got, err := r.TreeTokenGrant(ctx, "acme", "root"); err != nil || got != 0 {
		t.Fatalf("grant before any extension = %d, %v, want 0", got, err)
	}
	if _, err := denials.Grant(ctx, "acme", "root", leasecontrol.Dimensions{Tokens: 40_000}, leasecontrol.Dimensions{Tokens: 1_000_000}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if got, err := r.TreeTokenGrant(ctx, "acme", "root"); err != nil || got != 40_000 {
		t.Fatalf("grant after an extension = %d, %v, want 40000", got, err)
	}
	if _, err := (treeGrantReader{denials: failingGrants{denials}}).TreeTokenGrant(ctx, "acme", "root"); err == nil {
		t.Fatal("a failing Grants read returned no error")
	}
}
