// SPDX-License-Identifier: MIT

package audit

import (
	"context"
	"encoding/json"
	"time"
)

// Appender is the per-tenant audit hash-chain surface of §11.7 that the
// gateway's audit writers append to. The Postgres-backed audit store and
// ChainSetAppender, over an in-memory ChainSet, both satisfy it, so the
// gateway swaps the audit backend without changing a writer. It sits with
// the chain rather than with any one writer, so every gateway part that
// writes an audit event can hold the gateway's appender.
type Appender interface {
	// Append commits an audit event to the tenant's chain and returns
	// the sealed row.
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (Row, error)
}

// ChainSetAppender adapts an in-memory ChainSet to Appender. It backs the
// minimal gateway (no Postgres); the chain is lost on a process restart,
// which §11.7 permits only for the non-durable in-memory mode.
type ChainSetAppender struct {
	chains *ChainSet
	clock  func() time.Time
}

// NewChainSetAppender returns an Appender backed by chains. clock
// overrides time.Now; pass nil in production.
func NewChainSetAppender(chains *ChainSet, clock func() time.Time) *ChainSetAppender {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &ChainSetAppender{chains: chains, clock: clock}
}

// Append implements Appender. A zero at takes the appender's clock.
func (a *ChainSetAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (Row, error) {
	if at.IsZero() {
		at = a.clock()
	}
	return a.chains.Append(tenantID, eventType, payload, at), nil
}

// Ensure ChainSetAppender satisfies Appender at compile time.
var _ Appender = (*ChainSetAppender)(nil)
