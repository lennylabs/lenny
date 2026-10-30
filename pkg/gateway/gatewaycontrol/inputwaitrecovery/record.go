// SPDX-License-Identifier: MIT

// Package inputwaitrecovery keeps a session row's persisted input_required
// state consistent with the live lenny/request_input wait it names.
//
// A lenny/request_input call that arrives over LNK-GWCONTROL persists
// input_required on the session row, naming the wait by an identity the
// gateway assigns when it registers the wait. The wait itself lives only in
// the in-memory registry of the replica holding the pod process's
// LNK-GWCONTROL connection, so a replica restart, an adapter reconnect, or a
// failed exit write can leave a row in input_required naming a wait no
// registry holds. §28.3 makes LNK-GWCONTROL one connection per pod process to
// one replica, so the replica that accepts a pod process's connection is the
// one place that can tell a stale row from a live wait. This package holds
// the per-connection record that says whether a connection has been
// recovered, the replica's re-arm generation, the row writes the
// lenny/request_input handler and the recovery share, and the unary
// interceptor that runs the recovery before a connection's first call.
//
// spec: §7.2 (Session state machine), §28.3 (Link register), §5.2 (Tenant
// pinning).
package inputwaitrecovery

import (
	"context"
	"sync/atomic"

	"google.golang.org/grpc/stats"
)

// Generation is the replica's re-arm generation. Every connection record
// references it, and a record is recovered while its recovered generation
// equals the replica's. A failed exit write increments it, so every
// connection on the replica runs the recovery again before its next call
// carrying a session ID. The zero value is ready to use.
type Generation struct {
	n atomic.Uint64
}

// NewGeneration returns a replica generation. Its value starts above the
// recovered generation of every fresh record, so a new connection is not
// recovered until its first recovery run completes.
func NewGeneration() *Generation {
	g := &Generation{}
	g.n.Store(1)
	return g
}

// Load returns the current generation.
func (g *Generation) Load() uint64 { return g.n.Load() }

// Rearm increments the generation, so every record on the replica stops
// being recovered.
func (g *Generation) Rearm() { g.n.Add(1) }

// Record is one LNK-GWCONTROL connection's recovery record.
type Record struct {
	gen       *Generation
	recovered atomic.Uint64
}

// Recovered reports whether the connection's last completed recovery run
// started at the replica's current generation.
func (r *Record) Recovered() bool { return r.recovered.Load() == r.gen.Load() }

// Generation returns the replica generation the record references.
func (r *Record) Generation() *Generation { return r.gen }

// markRecovered raises the record's recovered generation to g. A run that
// started at an older generation never lowers a newer run's mark.
func (r *Record) markRecovered(g uint64) {
	for {
		cur := r.recovered.Load()
		if cur >= g || r.recovered.CompareAndSwap(cur, g) {
			return
		}
	}
}

type recordKey struct{}

// NewContext returns ctx carrying a fresh, not-recovered record that
// references gen. Its recovered generation starts at zero, below every value
// NewGeneration produces.
func NewContext(ctx context.Context, gen *Generation) context.Context {
	return context.WithValue(ctx, recordKey{}, &Record{gen: gen})
}

// FromContext returns the connection record ctx carries. A call served
// outside LNK-GWCONTROL, such as one on the gateway-edge /mcp endpoint,
// carries none.
func FromContext(ctx context.Context) (*Record, bool) {
	r, ok := ctx.Value(recordKey{}).(*Record)
	return r, ok && r != nil
}

// StatsHandler attaches a fresh connection record to each accepted
// connection's context, which gRPC uses as the parent of every call on the
// connection.
type StatsHandler struct {
	gen *Generation
}

// NewStatsHandler returns a StatsHandler whose records reference gen.
func NewStatsHandler(gen *Generation) *StatsHandler { return &StatsHandler{gen: gen} }

var _ stats.Handler = (*StatsHandler)(nil)

// TagConn attaches a fresh record to the connection's context.
func (h *StatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return NewContext(ctx, h.gen)
}

// HandleConn implements stats.Handler.
func (h *StatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements stats.Handler.
func (h *StatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

// HandleRPC implements stats.Handler.
func (h *StatsHandler) HandleRPC(context.Context, stats.RPCStats) {}
