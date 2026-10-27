// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"time"
)

// contextShutdowner is a listener that stops on a context, which
// *http.Server satisfies.
type contextShutdowner interface {
	Shutdown(ctx context.Context) error
}

// terminalWorkServer is the session server surface the shutdown sequence
// uses: the wait on the terminal claim record, its bound, the count it logs
// when the bound ends the wait, and the server-lifetime cancel.
type terminalWorkServer interface {
	WaitTerminalWork(ctx context.Context) error
	TerminalPipelineBudget() time.Duration
	OpenTerminalWork() int
	Close()
}

// shutdownStep is one close of the final shutdown stage, which runs on the
// fresh shutdown-timeout context.
type shutdownStep struct {
	name string
	run  func(ctx context.Context)
}

// shutdownSequence holds the collaborators of the gateway's post-signal
// shutdown. sessions is nil when no session server is built.
type shutdownSequence struct {
	timeout time.Duration
	http    contextShutdowner
	// forward is the CH-HOLDERFORWARD listener, stopped with the REST
	// listener so no forwarded request starts after the stop; nil when the
	// forward is not enabled.
	forward  contextShutdowner
	sessions terminalWorkServer
	closers  []shutdownStep
}

// run executes the shutdown in the order §10.1.7 states: stop accepting
// requests, wait for the terminal pipelines in progress for at most the
// terminal pipeline budget, cancel the session server's lifetime, and only
// then stop background work and close the store connections.
//
// The wait precedes Close because the post-terminal hook's release and the
// terminal binding release take the slot-accounting lock on the
// server-lifetime context and skip the release once Close cancels it. The
// span flush runs among the final closes, after Close, so the spans the
// terminal pipelines emit during the wait are exported. Each stage that
// takes a context gets its own: the listener stop and the final closes each
// run on a fresh shutdown-timeout context, so a long wait does not consume
// the budget of the closes that follow it. The watchdog context, which the
// coordination Sweeper runs on, is cancelled by the caller after run
// returns, so the Sweeper keeps renewing leases and releasing terminal
// bindings through the wait.
//
// spec: §10.1.7 (Terminal pipelines at process exit.), §29.7
func (q shutdownSequence) run() {
	q.stopListener()
	if q.sessions != nil {
		q.waitTerminalWork()
		q.sessions.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	for _, step := range q.closers {
		step.run(ctx)
	}
}

// stopListener stops the HTTP listener, and the forward listener when one
// runs, on a shutdown-timeout context.
func (q shutdownSequence) stopListener() {
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	defer cancel()
	if err := q.http.Shutdown(ctx); err != nil {
		slog.Warn("lenny-gateway: HTTP listener shutdown", "error", err)
	}
	if q.forward != nil {
		if err := q.forward.Shutdown(ctx); err != nil {
			slog.Warn("lenny-gateway: holder-forward listener shutdown", "error", err)
		}
	}
}

// waitTerminalWork waits on the terminal claim record for at most the
// terminal pipeline budget, and logs the entries still open when the bound
// ends the wait. Those pipelines are cut by the process exit, which
// §10.1.7 records as a departure the orphaned-claim detection and the stale
// slot release recover from.
//
// spec: §10.1.7 (Terminal pipelines at process exit.)
func (q shutdownSequence) waitTerminalWork() {
	budget := q.sessions.TerminalPipelineBudget()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	started := time.Now()
	err := q.sessions.WaitTerminalWork(ctx)
	switch {
	case err == nil:
		slog.Info("lenny-gateway: terminal pipelines drained before shutdown", "waited", time.Since(started))
	case errors.Is(err, context.DeadlineExceeded):
		slog.Warn("lenny-gateway: terminal pipeline budget ended the shutdown wait; open terminal work is cut at exit",
			"open_entries", q.sessions.OpenTerminalWork(), "budget", budget)
	default:
		slog.Warn("lenny-gateway: terminal pipeline shutdown wait ended",
			"open_entries", q.sessions.OpenTerminalWork(), "error", err)
	}
}

// shutdownSequence assembles the gateway's shutdown collaborators. The final
// closes keep their prior relative order: the span flush, the LLM proxy, the
// GatewayControl server, the Postgres pools, the Source-Mode SQLite final
// snapshot, the Redis clients, and the OpenFeature providers.
//
// spec: §10.1.7 (Terminal pipelines at process exit.)
func (w *gatewayWiring) shutdownSequence(timeout time.Duration) shutdownSequence {
	q := shutdownSequence{timeout: timeout, http: w.httpSrv}
	if w.holderForwardSrv != nil {
		q.forward = w.holderForwardSrv
	}
	if w.sessionSrv != nil {
		q.sessions = w.sessionSrv
	}
	q.closers = w.finalShutdownSteps()
	return q
}

// finalShutdownSteps lists the closes that run after the session server's
// lifetime ends, each on the fresh shutdown-timeout context.
func (w *gatewayWiring) finalShutdownSteps() []shutdownStep {
	return []shutdownStep{
		// Flush buffered spans before the process exits (§16.3 batch
		// processor). It runs after the terminal pipeline wait so the spans
		// those pipelines emit are exported.
		{"trace", func(ctx context.Context) {
			if w.traceShutdown != nil {
				_ = w.traceShutdown(ctx)
			}
		}},
		{"llm-proxy", func(ctx context.Context) {
			if w.llmProxySrv != nil {
				_ = w.llmProxySrv.Shutdown(ctx)
			}
		}},
		{"gateway-control", func(context.Context) {
			if w.gatewayCtrlSrv != nil {
				w.gatewayCtrlSrv.GracefulStop()
			}
		}},
		{"postgres", func(context.Context) {
			if w.pgPool != nil {
				w.pgPool.Close()
			}
			// spec: §12.3, §15.1 — release the CREATE-privileged DDL pools
			// opened for per-tenant sequence provisioning. primaryDDLPool
			// aliases billingAuditDDLPool in the single-instance topology, so
			// closeDDLPools closes each distinct pool once. F-11.2.10.
			closeDDLPools(w.billingAuditDDLPool, w.primaryDDLPool)
		}},
		// spec: §17.4 — stop the Source-Mode SQLite flush loop and snapshot
		// the session/metadata stores one final time so a clean shutdown
		// (the documented Ctrl-C flow) loses no writes, then close the
		// database. F-17.4.2.
		{"sqlite", func(ctx context.Context) {
			if w.sqliteDB == nil {
				return
			}
			if w.sqliteFlushCancel != nil {
				w.sqliteFlushCancel()
			}
			if err := w.sqliteDB.Close(ctx); err != nil {
				log.Printf("lenny-gateway: §17.4 sqlite close: %v", err)
			}
		}},
		{"redis", func(context.Context) {
			if w.redisClient != nil {
				_ = w.redisClient.Close()
			}
			// §12.4: close the per-concern split clients (no-op when no
			// split is configured; the base client closed above is left
			// untouched).
			_ = w.concernRedis.Close()
		}},
		// §10.7: release the vendor OpenFeature SDK background connections.
		{"openfeature", func(context.Context) { w.experimentProviders.Close() }},
	}
}
