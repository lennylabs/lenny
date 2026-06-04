// SPDX-License-Identifier: MIT

package billingsink

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// Publishing decorates a billingstore.Store so that every event a
// successful Append seals is delivered to the configured delivery sinks.
// It enforces the §11.2.1 line 137 ordering: the sink publish happens
// only after the wrapped synchronous Postgres write confirms, and runs
// asynchronously so webhook/queue latency never blocks the billing write
// path.
//
// The decorator wraps the durable primary store (the failover pipeline's
// Primary). Events that are buffered through the §11.2.1 Redis-stream /
// in-memory write-ahead path during a Postgres outage are flushed later
// via the inserter's own commit path; sink delivery for that degraded
// window is a tracked residual on the finding.
//
// spec: §11.2.1 — Delivery sinks (line 137). F-11.2.14.
type Publishing struct {
	billingstore.Store
	pub *Publisher
	ctx context.Context
	// sync makes Append publish inline rather than in a goroutine; tests
	// set it so assertions on delivery are deterministic.
	sync bool
}

// PublishingOptions configures a Publishing decorator.
type PublishingOptions struct {
	// Context is the long-lived context the async publish goroutines
	// run under (the gateway's background context), detached from the
	// request context that may end before delivery completes. Defaults
	// to context.Background().
	Context context.Context
	// Sync publishes inline instead of spawning a goroutine. For tests.
	Sync bool
}

// NewPublishing wraps store so successful appends publish to pub. When
// pub is empty it returns store unwrapped so the no-sink configuration
// keeps the original write path untouched.
func NewPublishing(store billingstore.Store, pub *Publisher, opts PublishingOptions) billingstore.Store {
	if pub.Empty() {
		return store
	}
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return &Publishing{Store: store, pub: pub, ctx: ctx, sync: opts.Sync}
}

// Append commits e through the wrapped store and, on success, publishes
// the sealed event to the delivery sinks.
func (p *Publishing) Append(ctx context.Context, e billingstore.Event) (billingstore.Event, error) {
	sealed, err := p.Store.Append(ctx, e)
	if err != nil {
		return sealed, err
	}
	p.publish(sealed)
	return sealed, nil
}

// publish marshals the sealed event and hands it to the fan-out
// Publisher. A marshal failure (which cannot happen for a well-formed
// event) is dropped rather than failing the already-committed write.
func (p *Publishing) publish(e billingstore.Event) {
	body, err := Marshal(e)
	if err != nil {
		return
	}
	meta := metaOf(e)
	if p.sync {
		p.pub.Publish(p.ctx, body, meta)
		return
	}
	go p.pub.Publish(p.ctx, body, meta)
}
