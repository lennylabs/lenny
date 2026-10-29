// SPDX-License-Identifier: MIT

package main

import (
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

// newLeaseBudgetSource builds the §8.6 store-backed budget source when the
// GatewayControl listener is enabled, and returns nil otherwise. It reads
// session rows from the gateway SessionStore, and denial, cool-off, and
// cumulative grants from the delegation_tree_budget row through denialpg
// when Postgres is configured, or from a replica-local MemoryDenialStore on
// the development path without Postgres.
// spec: §8.6 (Extension state), §8.6 (Durability)
func (w *gatewayWiring) newLeaseBudgetSource() (*leasecontrol.StoreBudgetSource, error) {
	if *w.f.grpcAddr == "" {
		return nil, nil
	}
	var denials leasecontrol.DenialStore = leasecontrol.NewMemoryDenialStore(nil)
	if w.pgPool != nil {
		denials = denialpg.New(w.pgPool)
	}
	return leasecontrol.NewStoreBudgetSource(w.sessions, denials, leaseExtensionConfig(w.f))
}
