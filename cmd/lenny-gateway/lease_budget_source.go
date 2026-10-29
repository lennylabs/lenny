// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/denialpg"
)

// leaseExtensionConfig resolves the deployment-level §8.6 lease-extension
// configuration from the gateway flags.
// spec: §8.6
func leaseExtensionConfig(f *gatewayFlags) leasecontrol.ExtensionConfig {
	return leasecontrol.ExtensionConfig{
		DeploymentBudget:    *f.leaseDefaultBudget,
		DeploymentMaxBudget: *f.leaseMaxBudget,
		ApprovalMode:        leasecontrol.ApprovalMode(*f.leaseDefaultApproval),
		SuccessCoolOff:      time.Duration(*f.leaseCoolOffSec) * time.Second,
		RejectionCoolOff:    time.Duration(*f.leaseRejectionCoolOffSec) * time.Second,
		AutoMaxPerMinute:    *f.leaseAutoMaxPerMin,
	}
}

// newLeaseDenialStore builds the §8.6 DenialStore that records a tree's
// denial, cool-off, and cumulative grants: the delegation_tree_budget row
// through denialpg when Postgres is configured, or a replica-local
// MemoryDenialStore on the development path without Postgres. One instance
// serves the lease budget source and the delegate_task token cap, so a grant
// the source records raises the cap the reservation reads.
// spec: §8.6 (Extension state), §8.6 (Durability), §8.3
func (w *gatewayWiring) newLeaseDenialStore() leasecontrol.DenialStore {
	if w.pgPool != nil {
		return denialpg.New(w.pgPool)
	}
	return leasecontrol.NewMemoryDenialStore(nil)
}

// newLeaseBudgetSource builds the §8.6 store-backed budget source over
// denials when the GatewayControl listener is enabled, and returns nil
// otherwise. It reads session rows from the gateway SessionStore, and
// denial, cool-off, and cumulative grants from denials.
// spec: §8.6 (Extension state), §8.6 (Durability)
func (w *gatewayWiring) newLeaseBudgetSource(denials leasecontrol.DenialStore) (*leasecontrol.StoreBudgetSource, error) {
	if *w.f.grpcAddr == "" {
		return nil, nil
	}
	return leasecontrol.NewStoreBudgetSource(w.sessions, denials, leaseExtensionConfig(w.f))
}

// treeGrantReader adapts a §8.6 DenialStore to the delegation service's
// TreeGrantReader: the token cap a reservation reads is the ext_tokens value
// DenialStore.Grants returns.
// spec: §8.3, §8.6 (Extension state)
type treeGrantReader struct{ denials leasecontrol.DenialStore }

// TreeTokenGrant returns the tree's cumulative token extension grant.
func (r treeGrantReader) TreeTokenGrant(ctx context.Context, tenantID, rootSessionID string) (int64, error) {
	g, err := r.denials.Grants(ctx, tenantID, rootSessionID)
	if err != nil {
		return 0, fmt.Errorf("read tree %s grants: %w", rootSessionID, err)
	}
	return g.Tokens, nil
}
