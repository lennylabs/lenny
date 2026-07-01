// SPDX-License-Identifier: MIT

package auditscope

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// chainSetChain adapts an in-memory audit.ChainSet to the Chain
// interface so the minimal (no-Postgres) gateway can sit a Validator
// in front of its in-memory hash chain. The chain is lost on restart,
// which §11.7 permits only for the non-durable in-memory mode.
type chainSetChain struct {
	chains *audit.ChainSet
	clock  func() time.Time
}

// NewChainSetChain returns a Chain backed by chains. clock overrides
// time.Now; pass nil in production.
func NewChainSetChain(chains *audit.ChainSet, clock func() time.Time) Chain {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &chainSetChain{chains: chains, clock: clock}
}

func (a *chainSetChain) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if at.IsZero() {
		at = a.clock()
	}
	return a.chains.Append(tenantID, eventType, payload, at), nil
}

func (a *chainSetChain) Rows(_ context.Context, tenantID string) ([]audit.Row, error) {
	c := a.chains.Chain(tenantID)
	if c == nil {
		return nil, nil
	}
	return c.Rows(), nil
}

func (a *chainSetChain) Verify(_ context.Context, tenantID string) (audit.VerifyResult, error) {
	c := a.chains.Chain(tenantID)
	if c == nil {
		return audit.VerifyResult{Integrity: audit.ChainVerified}, nil
	}
	return c.Verify(), nil
}
