// SPDX-License-Identifier: MIT

// Package pgnotify is the Postgres LISTEN/NOTIFY substrate the gateway
// uses as the §4.9 credential-deny-list fallback when Redis pub/sub is
// unavailable. §4.9 line 1647 specifies that a credential revocation
// propagates across replicas "via Redis pub/sub with Postgres
// LISTEN/NOTIFY as fallback": Redis is the low-latency primary, and the
// authoritative Postgres connection carries the revocation when Redis
// is down or disabled.
//
// Bus mirrors the pkg/gateway/pubsub.Bus surface (Publish + Subscribe)
// so the deny-list propagator drives either substrate through the same
// interface. Publish runs pg_notify; Subscribe holds a dedicated
// connection LISTENing on the channel and reconnects on a dropped
// connection so a Postgres failover does not permanently silence the
// fallback.
//
// Postgres NOTIFY is durable for the duration of a transaction and
// delivered to every connected LISTENer, but a notification raised
// while a replica's LISTEN connection is down is missed. The §4.9
// startup deny-list rebuild reconciles any missed revocation on the
// next restart, so a dropped notification degrades convergence latency
// rather than correctness — the same bound the Redis path carries.
// spec: §4.9 line 1647 / §13.3 line 648.
package pgnotify

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reconnectBackoff is the pause before a Subscribe loop re-establishes a
// dropped LISTEN connection. It bounds the reconnect spin during a
// sustained Postgres outage without adding meaningful convergence
// latency once Postgres returns.
const reconnectBackoff = time.Second

// Bus is a Postgres LISTEN/NOTIFY fan-out over a pgxpool.Pool. A nil
// *Bus is a valid no-op: Publish returns nil and Subscribe blocks until
// the context is cancelled, so a caller wired without Postgres runs with
// no fallback and without branching at every call site.
type Bus struct {
	pool *pgxpool.Pool
}

// New returns a Bus backed by pool. A nil pool yields a Bus whose
// Publish and Subscribe are no-ops, which is the no-fallback mode.
func New(pool *pgxpool.Pool) *Bus {
	if pool == nil {
		return nil
	}
	return &Bus{pool: pool}
}

// Publish raises a Postgres notification on channel carrying payload. A
// nil Bus, or a Bus built from a nil pool, publishes nothing and returns
// nil. A publish failure (Postgres unreachable) is returned so the
// caller can log it; it is not fatal because the §4.9 startup rebuild
// reconciles a missed revocation.
func (b *Bus) Publish(ctx context.Context, channel string, payload []byte) error {
	if b == nil || b.pool == nil {
		return nil
	}
	// pg_notify takes the channel as a string argument, so no identifier
	// quoting is needed here; the LISTEN side quotes it. The payload is
	// the JSON-encoded credential key, well under the 8000-byte
	// NOTIFY payload limit.
	_, err := b.pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, string(payload))
	return err
}

// Subscribe holds a dedicated connection LISTENing on channel and
// delivers every notification payload to handler until ctx is cancelled.
// It blocks; callers run it in a goroutine. On a dropped connection it
// backs off and reconnects so a Postgres failover does not permanently
// stop the fallback. A nil Bus blocks until ctx is cancelled and
// delivers nothing.
func (b *Bus) Subscribe(ctx context.Context, channel string, handler func(payload []byte)) {
	if b == nil || b.pool == nil {
		<-ctx.Done()
		return
	}
	for ctx.Err() == nil {
		if err := b.listenOnce(ctx, channel, handler); err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectBackoff):
			}
		}
	}
}

// listenOnce opens a dedicated connection (not a pooled one, so the
// long-lived LISTEN does not tie up a pool slot or trip the pool's
// on-release reset), issues LISTEN, and delivers notifications until the
// connection drops or ctx is cancelled. The channel is rendered through
// pgx.Identifier.Sanitize so a channel name with special characters is
// safely double-quoted.
func (b *Bus) listenOnce(ctx context.Context, channel string, handler func(payload []byte)) error {
	conn, err := pgx.ConnectConfig(ctx, b.pool.Config().ConnConfig)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		handler([]byte(n.Payload))
	}
}
