// SPDX-License-Identifier: MIT

// Package auditretention implements the §16.4 audit-log retention
// pruner. A periodic leader-gated sweep deletes audit rows that have
// aged past the configured `audit.retentionDays` window, holding two
// classes of rows the spec exempts:
//
//   - gdpr.* erasure-receipt rows are retained under the separate,
//     longer `audit.gdprRetentionDays` window (>= 2190 days under any
//     regulated complianceProfile), never under the general window.
//   - When an external SIEM is configured, a row is held until the SIEM
//     forwarder acknowledges it (its sequence_number is at or below the
//     forwarder's high-water mark), so a stalled forwarder never loses
//     an undelivered event to retention.
//
// The general window is resolved from the §16.4 retention preset (soc2,
// fedramp-high, hipaa, nis2-dora) or an explicit custom day count via
// audit.ResolveRetentionDays. The pruner does not enforce the SIEM
// guard's operator override; that path is ForceDrop, which records the
// §16.7 audit.partition_drop_forced event before it deletes.
//
// spec: §16.4 lines 378-382 (retention partition GC, gdpr.* carve-out,
// SIEM delivery guard); §11.7 line 456 (regulated retention floor);
// §12.8 line 839 (gdprRetentionDays floor).
package auditretention

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
)

// DefaultPruneInterval is how often the retention sweep runs when no
// interval is configured. Retention is a daily-granularity obligation,
// so an hourly cadence keeps the audit table bounded without the
// per-cycle cost of the §12.5 artifact GC.
const DefaultPruneInterval = time.Hour

// MinPruneInterval floors the sweep cadence so a misconfigured interval
// cannot turn the leader-elected sweep into a busy-loop of DELETE
// statements against the audit table.
const MinPruneInterval = time.Minute

// RetentionStore is the §16.4 retention surface the pruner deletes
// through. *auditstore.Store satisfies it.
type RetentionStore interface {
	// PruneRetention deletes the tenant's rows past the supplied
	// windows and returns the count deleted.
	PruneRetention(ctx context.Context, tenantID string, opts auditstore.PruneOptions) (int, error)
	// RetentionWindowStats summarizes the rows a force-drop would
	// delete, for the §16.7 audit.partition_drop_forced payload.
	RetentionWindowStats(ctx context.Context, tenantID string, cutoff time.Time) (auditstore.RetentionWindow, error)
	// SIEMHeldCount reports how many non-gdpr.* rows past cutoff the
	// SIEM delivery guard is withholding from the drop (their
	// sequence_number is above the forwarder's high-water mark). A
	// non-zero result is the §16.4 / §16.5 AuditPartitionDropBlocked
	// condition.
	SIEMHeldCount(ctx context.Context, tenantID string, cutoff time.Time) (int, error)
}

// TenantLister enumerates the tenant chains the sweep covers. The
// platform pseudo-tenant ("platform") is included so platform-admin
// audit rows are pruned alongside per-tenant chains.
type TenantLister interface {
	ListTenants(ctx context.Context) ([]string, error)
}

// MetricsSink receives the retention sweep's observability signals. The
// gateway wires it to gatewaymetrics; the minimal gateway leaves it nil.
type MetricsSink interface {
	// AddAuditRowsPruned records rows deleted by a sweep cycle.
	AddAuditRowsPruned(n int)
	// IncAuditRetentionRun records a sweep outcome (success | error).
	IncAuditRetentionRun(outcome string)
	// SetAuditPartitionDropBlocked sets the §16.4 / §16.5
	// lenny_audit_partition_drop_blocked gauge for a partition (audit
	// chain): 1 when the SIEM delivery guard is holding past-TTL rows
	// the GC could otherwise drop, 0 once the forwarder has caught up.
	SetAuditPartitionDropBlocked(partition string, blocked bool)
}

// Options configures a Pruner. A zero field selects its default.
type Options struct {
	// RetentionDays is the resolved §16.4 general window
	// (audit.ResolveRetentionDays). A non-positive value disables the
	// general sweep (gdpr.* rows are still subject to GDPRRetentionDays).
	RetentionDays int
	// GDPRRetentionDays is the §12.8 gdpr.* erasure-receipt window. A
	// non-positive value holds every gdpr.* row indefinitely.
	GDPRRetentionDays int
	// SIEMConfigured reports whether audit.siem.endpoint is set; it
	// activates the §16.4 SIEM delivery guard.
	SIEMConfigured bool
	// Interval overrides DefaultPruneInterval. Values below
	// MinPruneInterval are raised to the floor.
	Interval time.Duration
	// Clock overrides time.Now (UTC).
	Clock func() time.Time
	// Metrics, when set, receives the sweep observability signals.
	Metrics MetricsSink
}

// Pruner runs the periodic §16.4 audit-retention sweep.
type Pruner struct {
	store     RetentionStore
	tenants   TenantLister
	emitter   appendFunc
	retention int
	gdpr      int
	siem      bool
	interval  time.Duration
	clock     func() time.Time
	metrics   MetricsSink

	// blocked tracks which partitions currently carry a
	// lenny_audit_partition_drop_blocked=1 gauge so a recovered
	// partition is cleared to 0 exactly once and a never-blocked
	// partition never creates a series (bounding gauge cardinality to
	// partitions that have actually stalled the SIEM forwarder). Guarded
	// by mu because the gauge is touched only from Tick, but ForceDrop
	// and Tick can run on different goroutines.
	mu      sync.Mutex
	blocked map[string]bool
}

// appendFunc is the audit-append closure the force-drop path emits
// through. It matches both *auditstore.Store.Append and the admin
// AuditLog.Append signatures without coupling this package to either's
// concrete return type.
type appendFunc func(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) error

// New returns a Pruner. emit may be nil; when nil, ForceDrop deletes
// without emitting the §16.7 event (and returns an error instead, so a
// caller cannot silently lose the audit record of a forced drop).
func New(store RetentionStore, tenants TenantLister, emit appendFunc, opts Options) *Pruner {
	p := &Pruner{
		store:     store,
		tenants:   tenants,
		emitter:   emit,
		retention: opts.RetentionDays,
		gdpr:      opts.GDPRRetentionDays,
		siem:      opts.SIEMConfigured,
		interval:  clampInterval(opts.Interval),
		clock:     opts.Clock,
		metrics:   opts.Metrics,
		blocked:   make(map[string]bool),
	}
	if p.clock == nil {
		p.clock = func() time.Time { return time.Now().UTC() }
	}
	return p
}

func clampInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultPruneInterval
	}
	if d < MinPruneInterval {
		return MinPruneInterval
	}
	return d
}

// cutoffs returns the general and gdpr.* retention boundaries at now. A
// non-positive window returns a zero time, which PruneRetention treats
// as "match nothing" for that class.
func (p *Pruner) cutoffs(now time.Time) (general, gdpr time.Time) {
	if p.retention > 0 {
		general = now.Add(-time.Duration(p.retention) * 24 * time.Hour)
	}
	if p.gdpr > 0 {
		gdpr = now.Add(-time.Duration(p.gdpr) * 24 * time.Hour)
	}
	return general, gdpr
}

// Tick runs one retention sweep at now across every tenant and returns
// the total rows pruned. A non-positive general window disables the
// sweep entirely (gdpr.* rows still drop once they clear the GDPR
// window, but only if a general cutoff is set; a zero general cutoff
// short-circuits PruneRetention's required-argument check, so the
// sweep no-ops). On the first per-tenant error the sweep stops and
// returns the count pruned so far.
//
// spec: §16.4 lines 378-382.
func (p *Pruner) Tick(ctx context.Context, now time.Time) (int, error) {
	general, gdpr := p.cutoffs(now)
	if general.IsZero() {
		// No general window configured: nothing to prune. gdpr.* rows
		// have no general cutoff to ride, so the sweep is a no-op.
		return 0, nil
	}
	tenants, err := p.tenants.ListTenants(ctx)
	if err != nil {
		p.incRun("error")
		return 0, err
	}
	pruned := 0
	for _, tenant := range tenants {
		n, err := p.store.PruneRetention(ctx, tenant, auditstore.PruneOptions{
			GeneralCutoff:  general,
			GDPRCutoff:     gdpr,
			SIEMConfigured: p.siem,
			Force:          false,
		})
		pruned += n
		if err != nil {
			p.incRun("error")
			return pruned, fmt.Errorf("auditretention: prune tenant %s: %w", tenant, err)
		}
		// spec: §16.4 line 378 — when the SIEM delivery guard withholds
		// past-TTL rows from the drop, the GC is "holding the partition"
		// and the AuditPartitionDropBlocked alert must fire. Refresh the
		// per-partition gauge from the count of held rows this tenant
		// still carries. The guard is inactive unless SIEM is configured.
		p.refreshPartitionBlocked(ctx, tenant, general)
	}
	p.addPruned(pruned)
	p.incRun("success")
	return pruned, nil
}

// refreshPartitionBlocked updates the §16.4 / §16.5
// lenny_audit_partition_drop_blocked gauge for a partition (audit
// chain). It is a no-op unless the SIEM delivery guard is active and a
// metrics sink is wired. A guard-held row count above zero sets the
// gauge to 1; a partition that was previously blocked but has since
// drained (the forwarder caught up) is cleared to 0 exactly once. A
// partition that has never blocked never creates a gauge series, so the
// gauge cardinality is bounded to partitions that have actually stalled
// the SIEM forwarder. A held-count read error leaves the prior gauge
// state untouched (the destructive prune already succeeded; a transient
// count failure must not flip the alert).
func (p *Pruner) refreshPartitionBlocked(ctx context.Context, partition string, cutoff time.Time) {
	if !p.siem || p.metrics == nil {
		return
	}
	held, err := p.store.SIEMHeldCount(ctx, partition, cutoff)
	if err != nil {
		return
	}
	blocked := held > 0
	p.mu.Lock()
	defer p.mu.Unlock()
	was := p.blocked[partition]
	switch {
	case blocked:
		p.blocked[partition] = true
		p.metrics.SetAuditPartitionDropBlocked(partition, true)
	case was:
		// Recovered: clear the series exactly once.
		delete(p.blocked, partition)
		p.metrics.SetAuditPartitionDropBlocked(partition, false)
	}
}

// ForceDropResult reports the outcome of an operator-acknowledged
// force-drop, mirroring the §16.7 audit.partition_drop_forced payload.
type ForceDropResult struct {
	Partition            string    `json:"partition"`
	OldestEventTS        time.Time `json:"oldestEventTs"`
	NewestEventTS        time.Time `json:"newestEventTs"`
	SIEMHighWaterAtDrop  uint64    `json:"siemHighWaterMarkAtDrop"`
	EventsLost           int       `json:"eventsLostCount"`
	RequesterSub         string    `json:"requesterSub"`
	AcknowledgedDataLoss bool      `json:"acknowledgedDataLoss"`
}

// ForceDrop deletes the tenant's non-gdpr.* rows older than the general
// retention window regardless of the SIEM delivery guard, after an
// operator has explicitly acknowledged the resulting data loss. It
// records the §16.7 audit.partition_drop_forced event (with the window
// statistics captured before the delete) and returns the result.
//
// The event is emitted before the delete so the forced override is
// recorded even if the delete then fails; the event itself is exempt
// from the drop (its created_at is now, well inside the retention
// window). An unconfigured emitter is rejected so a forced drop can
// never bypass its own audit trail.
//
// spec: §16.4 line 378 (force-drop override); §16.7 line 687
// (audit.partition_drop_forced payload); §25.9 (the backing
// POST /v1/admin/audit-partitions/{partition}/drop endpoint).
func (p *Pruner) ForceDrop(ctx context.Context, tenantID, requesterSub string, now time.Time) (ForceDropResult, error) {
	if tenantID == "" {
		return ForceDropResult{}, fmt.Errorf("auditretention: ForceDrop requires a non-empty tenant id")
	}
	if p.emitter == nil {
		return ForceDropResult{}, fmt.Errorf("auditretention: ForceDrop requires an audit emitter so the override is recorded")
	}
	general, gdpr := p.cutoffs(now)
	if general.IsZero() {
		return ForceDropResult{}, fmt.Errorf("auditretention: ForceDrop requires a configured retention window")
	}
	win, err := p.store.RetentionWindowStats(ctx, tenantID, general)
	if err != nil {
		return ForceDropResult{}, fmt.Errorf("auditretention: window stats for %s: %w", tenantID, err)
	}
	res := ForceDropResult{
		Partition:            tenantID,
		OldestEventTS:        win.OldestEvent,
		NewestEventTS:        win.NewestEvent,
		SIEMHighWaterAtDrop:  win.SIEMHighWater,
		EventsLost:           win.Count,
		RequesterSub:         requesterSub,
		AcknowledgedDataLoss: true,
	}
	payload, err := json.Marshal(map[string]any{
		"partition":                    res.Partition,
		"oldest_event_ts":              forceTS(res.OldestEventTS),
		"newest_event_ts":              forceTS(res.NewestEventTS),
		"siem_high_water_mark_at_drop": res.SIEMHighWaterAtDrop,
		"events_lost_count":            res.EventsLost,
		"requester_sub":                res.RequesterSub,
		"acknowledged_data_loss":       true,
	})
	if err != nil {
		return ForceDropResult{}, err
	}
	// The §16.7 event is a platform-tenant administrative override.
	if err := p.emitter(ctx, "platform", "audit.partition_drop_forced", payload, now); err != nil {
		return ForceDropResult{}, fmt.Errorf("auditretention: emit partition_drop_forced: %w", err)
	}
	deleted, err := p.store.PruneRetention(ctx, tenantID, auditstore.PruneOptions{
		GeneralCutoff:  general,
		GDPRCutoff:     gdpr,
		SIEMConfigured: p.siem,
		Force:          true,
	})
	if err != nil {
		return ForceDropResult{}, fmt.Errorf("auditretention: forced prune of %s: %w", tenantID, err)
	}
	p.addPruned(deleted)
	// The recorded events_lost_count is the pre-delete window count; the
	// actual deleted count can differ if rows landed between the stats
	// read and the delete. Report the actual deletion in the result.
	res.EventsLost = deleted
	return res, nil
}

func forceTS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// Run drives the sweep on the configured interval until ctx is done.
// onTick, when non-nil, receives each sweep's pruned-count and error.
// Production runs this under the §10.1 leader lease so a single replica
// owns the destructive sweep; the minimal in-memory gateway has no
// durable audit store and does not start it.
func (p *Pruner) Run(ctx context.Context, onTick func(int, error)) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := p.Tick(ctx, p.clock())
			if onTick != nil {
				onTick(n, err)
			}
		}
	}
}

func (p *Pruner) addPruned(n int) {
	if p.metrics != nil && n > 0 {
		p.metrics.AddAuditRowsPruned(n)
	}
}

func (p *Pruner) incRun(outcome string) {
	if p.metrics != nil {
		p.metrics.IncAuditRetentionRun(outcome)
	}
}
