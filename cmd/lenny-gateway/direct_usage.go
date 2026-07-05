// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// defaultDirectUsagePollIntervalSeconds is the §11.2 direct-mode
// ReportUsage poll cadence when the operator has not tuned one. It matches
// the §11.2 line 44 quotaSyncIntervalSeconds default (30s) without being
// coupled to it: this loop pulls direct-mode token deltas, while the quota
// checkpoint loop persists Redis counters to Postgres. spec: §11.2 line 44
// (default 30s).
const defaultDirectUsagePollIntervalSeconds = 30

// minDirectUsagePollIntervalSeconds is the §11.2 floor on the direct-mode
// poll interval. A tighter interval narrows the §8.3 line 435 over-run
// window and the §6.2 line 253 false-idle window, but the RPC fans out to
// every bound session on the replica, so the floor keeps the pull from
// busy-looping. It matches the §11.2 line 44 quotaSyncIntervalSeconds
// minimum (10s). spec: §11.2 line 44 (minimum 10s).
const minDirectUsagePollIntervalSeconds = 10

// clampDirectUsagePollIntervalSeconds applies the §11.2 bounds to a
// configured direct-mode poll interval: a non-positive value selects the
// 30s default, and a positive value below the 10s minimum is raised to the
// floor. Any value at or above the floor is returned unchanged. spec: §11.2
// line 44 (default 30, minimum 10).
func clampDirectUsagePollIntervalSeconds(seconds int) int {
	if seconds <= 0 {
		return defaultDirectUsagePollIntervalSeconds
	}
	if seconds < minDirectUsagePollIntervalSeconds {
		return minDirectUsagePollIntervalSeconds
	}
	return seconds
}

// directUsagePullTimeout bounds a single per-session ReportUsage pull so a
// wedged pod's adapter cannot pin the poll loop or delay the next session's
// pull past the tick. It is derived from the poll interval rather than fixed
// so a long interval does not carry a disproportionately short deadline;
// the cap keeps a short interval from letting a slow adapter overrun the
// tick. A hung pod that never answers is bounded by this deadline and still
// idle-terminates through the §6.2 line 253 non-zero-delta gate (a timed-out
// pull records no delta, so the idle clock is untouched).
func directUsagePullTimeout(interval time.Duration) time.Duration {
	const cap = 5 * time.Second
	if interval <= 0 {
		return cap
	}
	// Use half the interval so the loop has time to drain every session
	// before the next tick, but never longer than the cap.
	half := interval / 2
	if half > cap {
		return cap
	}
	if half <= 0 {
		return cap
	}
	return half
}

// directUsageLeaseLookup resolves the credential leases a replica holds for
// a set of sessions. The credential-lease store satisfies it; the loop reads
// each bound session's delivery mode and tenant from its lease so it can
// filter direct-mode sessions and attribute the pulled usage. It is defined
// at the consumer so the loop does not depend on the concrete store type.
type directUsageLeaseLookup interface {
	// LeasesBySession returns every lease the store holds whose SessionID is
	// one of sessionIDs.
	LeasesBySession(sessionIDs []string) []credential.Lease
}

// directUsageRecorder is the consumer-side seam the loop fans each pulled
// direct-mode delta into. proxyUsageRecorder.RecordDirectUsage satisfies it;
// it reaches the §15.1 usage store, the §8.8 per-session accumulator, the
// §11.2 quota counter, the §12.4 fail-open accumulator, and the §11.2
// under-reporting anomaly detector, gates the §6.2 idle stamp on a non-zero
// delta, and excludes the mid-session enforcer breach path. It is defined at
// the consumer so the loop does not depend on the recorder's construction.
type directUsageRecorder interface {
	RecordDirectUsage(ctx context.Context, lease credential.Lease, u llmproxy.Usage)
}

// directUsagePuller pulls a session's incremental §4.7 ReportUsage delta
// from its pod's adapter, filtering proxy-mode leases. adapterclient.Client
// satisfies it via ReportUsageForLease. It is defined at the consumer so the
// loop does not depend on the adapter-client construction and a test can
// substitute a stub. spec: §4.7 (ReportUsage), §4.9 line 1468 (proxy-mode
// filter).
type directUsagePuller interface {
	ReportUsageForLease(ctx context.Context, sessionID string, deliveryMode credential.DeliveryMode, cumulative bool) (adapterclient.UsageReport, error)
}

// directUsageLoop is the single global §11.2 direct-mode usage-pull loop.
// On each tick it snapshots the replica's live pod bindings, resolves each
// bound session's credential lease, filters to direct-delivery sessions, and
// pulls the incremental §4.7 ReportUsage delta from the session's adapter,
// fanning it into the recorder. The loop is keyed off the session-scoped pod
// registry (podRegistry.Snapshot / Get) so it observes only sessions this
// replica currently coordinates and stops pulling a session the moment its
// binding is removed at teardown; it never touches the transient BindResult
// the bind caller owns and closes.
//
// The mid-session enforcer, the in-path extension, and the terminal
// FINAL_USAGE_REPORT settlement are out of scope: the recorder excludes the
// enforcer breach path (§11.2 line 44 / §8.3 line 631 forbid an in-path
// termination or extension for a direct-mode session), and the adapter's
// terminal FINAL_USAGE_REPORT push keeps its distinct §8.3 quiescence role.
//
// spec: §11.2 line 42 (direct-mode integrity control), §8.3 line 435
// (over-run bounded against the poll interval), §6.2 line 253 (idle reset on
// non-zero delta), §4.7 (ReportUsage pull). F-15.3.7, F-11.2.20.
type directUsageLoop struct {
	registry *podsession.Registry
	leases   directUsageLeaseLookup
	recorder directUsageRecorder
	interval time.Duration
	log      *slog.Logger
	// pullerFor extracts the §4.7 ReportUsage puller from a live binding. It
	// defaults to the binding's *adapterclient.Client and is overridden in
	// tests so the registry-keyed teardown logic runs against a stub adapter
	// without a live gRPC connection. It returns nil when the binding carries
	// no adapter, which the loop treats as nothing to pull.
	pullerFor func(*podsession.BindResult) directUsagePuller
}

// adapterPuller is the default pullerFor: the binding's live adapter client.
// It returns nil (not an interface wrapping a typed nil) when the binding has
// no adapter, so the loop's nil check is correct.
func adapterPuller(b *podsession.BindResult) directUsagePuller {
	if b == nil || b.Adapter == nil {
		return nil
	}
	return b.Adapter
}

// newDirectUsageLoop constructs the direct-mode usage-pull loop. It returns
// nil when any dependency the pull needs is absent (no pod registry, no
// lease store, or no recorder — the recorder is nil when the gateway has no
// usagestore), so the caller can start it unconditionally and the loop is a
// no-op on a minimal gateway that records no usage. intervalSeconds is
// clamped to the §11.2 bounds (default 30s, minimum 10s).
func newDirectUsageLoop(registry *podsession.Registry, leases directUsageLeaseLookup, recorder directUsageRecorder, intervalSeconds int, log *slog.Logger) *directUsageLoop {
	if registry == nil || leases == nil || recorder == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &directUsageLoop{
		registry:  registry,
		leases:    leases,
		recorder:  recorder,
		interval:  time.Duration(clampDirectUsagePollIntervalSeconds(intervalSeconds)) * time.Second,
		log:       log,
		pullerFor: adapterPuller,
	}
}

// Run drives the poll loop until ctx is cancelled. The goroutine's only exit
// is ctx.Done, so it is tied to the gateway's watchdog context and leaks no
// goroutine at shutdown. Run is a no-op on a nil loop so the caller need not
// nil-check before `go loop.Run(ctx)`.
func (l *directUsageLoop) Run(ctx context.Context) {
	if l == nil {
		return
	}
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.pollOnce(ctx)
		}
	}
}

// pollOnce pulls one direct-mode delta for every direct-delivery session the
// replica currently binds. It snapshots the registry (a copy, so a concurrent
// bind or teardown does not race the iteration), resolves the sessions'
// leases in one lookup, and pulls each direct-mode session's incremental
// delta. A session whose binding was removed between the snapshot and the
// per-session Get is skipped: the registry Get returns not-ok, so the loop
// never pulls a torn-down session's closed adapter.
func (l *directUsageLoop) pollOnce(ctx context.Context) {
	bindings := l.registry.Snapshot()
	if len(bindings) == 0 {
		return
	}
	sessionIDs := make([]string, 0, len(bindings))
	for _, b := range bindings {
		sessionIDs = append(sessionIDs, b.SessionID)
	}
	// Resolve each bound session's lease so the loop reads its delivery mode
	// and tenant. A session with no lease (an in-memory session with no §4.9
	// credential pool, or a proxy-mode session whose lease is present) is
	// handled per-lease below.
	leasesByID := make(map[string]credential.Lease, len(sessionIDs))
	for _, lease := range l.leases.LeasesBySession(sessionIDs) {
		// A session may hold more than one lease over its lifetime; the
		// current binding's delivery mode is what matters, and every lease
		// for a live session carries the same session-level delivery mode, so
		// the last one wins deterministically.
		leasesByID[lease.SessionID] = lease
	}
	timeout := directUsagePullTimeout(l.interval)
	for _, b := range bindings {
		lease, ok := leasesByID[b.SessionID]
		if !ok || lease.DeliveryMode != credential.DeliveryDirect {
			// No lease resolved, or the session is proxy-mode: proxy-extracted
			// counts are already authoritative and recorded on the §4.9 path,
			// so pulling here would double-count. Skip. spec: §4.9 line 1468.
			continue
		}
		l.pullSession(ctx, b.SessionID, lease, timeout)
	}
}

// pullSession pulls one direct-mode session's incremental delta and fans it
// into the recorder. It re-reads the binding through the session-scoped
// registry (rather than the snapshot's BindResult) so a session torn down
// since the snapshot is skipped and the loop never dials a closed adapter.
// The pull runs under a per-call timeout derived from the poll interval so a
// wedged adapter cannot pin the loop; a failed or timed-out pull records no
// delta, so the §6.2 line 253 idle clock is untouched and a hung pod still
// idle-terminates.
func (l *directUsageLoop) pullSession(ctx context.Context, sessionID string, lease credential.Lease, timeout time.Duration) {
	b, ok := l.registry.Get(sessionID)
	if !ok {
		// The session unbound between the snapshot and this pull; nothing to
		// pull, and re-reading through the registry (rather than the snapshot's
		// BindResult) is what makes the loop stop at teardown.
		return
	}
	puller := l.pullerFor(b)
	if puller == nil {
		// The binding carries no adapter; nothing to pull.
		return
	}
	pullCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// spec: §4.7 — the steady-state poll pulls the incremental delta
	// (cumulative=false). The §11.2 crash-recovery cumulative read is a
	// distinct recovery path, not this steady-state loop.
	report, err := puller.ReportUsageForLease(pullCtx, sessionID, lease.DeliveryMode, false)
	if err != nil {
		// A proxy-mode misroute (ErrUsageReportProxyMode), a transport error,
		// or a timeout records no delta. This is expected for a hung pod and
		// must not reset the idle clock, so it is logged at debug and dropped.
		l.log.Debug("direct-mode ReportUsage pull failed",
			slog.String("session_id", sessionID),
			slog.String("tenant_id", lease.TenantID),
			slog.Any("error", err))
		return
	}
	// Fan the pulled incremental delta into the recorder. The recorder
	// reaches the accounting sinks and the S5 anomaly detector, gates the
	// §6.2 idle stamp on a non-zero delta, and excludes the mid-session
	// enforcer. A zero-delta pull is still forwarded: it is the primary
	// under-reporting signal the §11.2 anomaly detector counts.
	l.recorder.RecordDirectUsage(ctx, lease, llmproxy.Usage{
		InputTokens:  int(report.InputTokens),
		OutputTokens: int(report.OutputTokens),
	})
}
