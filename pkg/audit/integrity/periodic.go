// SPDX-License-Identifier: MIT

package integrity

import (
	"context"
	"fmt"
	"time"
)

// §11.7 item 2 lines 357-358 — the periodic background integrity check
// cadence. The default and maximum both depend on whether any tenant
// carries a regulated complianceProfile (soc2, fedramp, hipaa):
//
//   - regulated:   default 60s, max 120s
//   - unregulated: default 300s (5 min), max 900s (15 min)
//
// A configured value above the active profile's maximum is a fatal
// startup error (spec: the gateway "clamps the value at startup with a
// fatal error if the configured value exceeds the maximum").
const (
	RegulatedGrantCheckDefault   = 60 * time.Second
	RegulatedGrantCheckMax       = 120 * time.Second
	UnregulatedGrantCheckDefault = 300 * time.Second
	UnregulatedGrantCheckMax     = 900 * time.Second
)

// ResolveGrantCheckInterval validates a configured audit.grantCheckInterval
// against the active compliance posture and returns the effective cadence.
// A non-positive configured value selects the profile default. A value
// exceeding the profile maximum returns an error so the caller can fail
// startup. spec: §11.7 item 2 lines 357-358. F-11.7.3.
func ResolveGrantCheckInterval(configured time.Duration, regulated bool) (time.Duration, error) {
	def, max := UnregulatedGrantCheckDefault, UnregulatedGrantCheckMax
	profile := "unregulated"
	if regulated {
		def, max = RegulatedGrantCheckDefault, RegulatedGrantCheckMax
		profile = "regulated"
	}
	if configured <= 0 {
		return def, nil
	}
	if configured > max {
		return 0, fmt.Errorf("audit.grantCheckInterval %s exceeds the %s-profile maximum of %s (§11.7 item 2)", configured, profile, max)
	}
	return configured, nil
}

// PeriodicConfig configures the §11.7 item 2 background integrity check.
type PeriodicConfig struct {
	// Interval is the resolved grant-check cadence (see
	// ResolveGrantCheckInterval).
	Interval time.Duration

	// HardFailOnDrift, when true, makes a detected drift initiate a
	// graceful shutdown via the Shutdown hook (spec: "if
	// audit.hardFailOnDrift is enabled — initiates a graceful shutdown").
	HardFailOnDrift bool

	// ChainSampleN bounds the recent-window chain-continuity sample run
	// each cycle. A non-positive value walks each tenant chain in full.
	// spec: §11.7 line 370 — the periodic check "also samples random
	// chain segments and verifies hash continuity".
	ChainSampleN int
}

// PeriodicCheck runs the §11.7 item 2 background integrity check: on a
// fixed cadence it re-verifies the append-only ledger grants, triggers,
// and erasure guard, and samples recent chain segments for hash
// continuity. On drift it logs a critical line, increments the
// grant-drift counter (OnGrantDrift), reports chain states
// (OnChainState), and — when HardFailOnDrift is set — invokes Shutdown
// once and stops.
//
// All hooks are optional; a nil hook is skipped. The check reuses the
// startup-path Verify and CheckChainContinuityRecent functions so the
// runtime control and the startup control share one implementation.
type PeriodicCheck struct {
	DB  Querier
	Cfg PeriodicConfig

	// OnGrantDrift is invoked once per cycle that detects a grant /
	// trigger / erasure-guard drift. The gateway adapter increments
	// lenny_audit_grant_drift_total.
	OnGrantDrift func()

	// OnChainState is invoked once per sampled tenant with the §11.7
	// chain integrity state. The gateway adapter increments
	// lenny_audit_chain_integrity_total{state}.
	OnChainState func(state string)

	// OnRedactionReceiptMissing is invoked once per tenant that carries
	// §12.8-redaction-marked audit rows without a signature-verifying
	// RedactionReceipt, with the per-tenant count. The gateway adapter
	// advances lenny_audit_redaction_receipt_missing_total{tenant_id};
	// the §16.5 AuditRedactionReceiptMissing critical alert pages on any
	// non-zero value. F-11.7.15.
	OnRedactionReceiptMissing func(tenantID string, n int)

	// Logf emits the spec-mandated critical alert log line. Defaults to
	// a no-op when nil.
	Logf func(format string, args ...any)

	// Shutdown initiates a graceful shutdown when HardFailOnDrift is set
	// and drift is detected. Defaults to a no-op when nil.
	Shutdown func(reason string)
}

// CheckOnce runs a single integrity cycle and reports whether any drift
// was detected (grant/trigger/erasure-guard drift or a broken chain).
// It never returns an error: a transient query failure is logged and
// treated as no-drift so a flaky database does not trigger a hard-fail.
func (p *PeriodicCheck) CheckOnce(ctx context.Context) bool {
	drift := false
	if err := Verify(ctx, p.DB); err != nil {
		drift = true
		p.logf("CRITICAL: §11.7 item 2 audit integrity drift detected at runtime: %v", err)
		if p.OnGrantDrift != nil {
			p.OnGrantDrift()
		}
	}
	// §11.7 line 370 — sample recent chain segments; a broken chain
	// triggers the same critical alert path.
	// p.DB is passed as both the ledger and the control-plane pool: the
	// co-located topology where audit_log and tenants share one Postgres.
	// The dedicated control-plane wiring (a separate CtrlDB field) is
	// threaded in a later build step; the same-pool call preserves the
	// current co-located behavior. spec: §12.3 line 103.
	results, err := CheckChainContinuityRecent(ctx, p.DB, p.DB, p.Cfg.ChainSampleN)
	if err != nil {
		p.logf("WARNING: §11.7 item 2 periodic chain-continuity sample could not run: %v", err)
		return drift
	}
	for _, r := range results {
		if p.OnChainState != nil {
			p.OnChainState(string(r.Result.Integrity))
		}
		if r.Broken() {
			drift = true
			p.logf("CRITICAL: §11.7 audit chain broken for tenant %s at sequence %d: %s",
				r.TenantID, r.Result.BreakSeq, r.Result.Detail)
		}
	}
	// §12.8 lines 810-827 — detect audit rows rewritten by the in-place
	// GDPR redaction but lacking a signature-verifying RedactionReceipt.
	// This is the §16.5 AuditRedactionReceiptMissing compliance signal
	// (page on-call), distinct from the chain-tamper hard-fail path: an
	// orphaned redaction does not self-terminate the gateway, it raises a
	// metric the alert pages on. A scan error (for example the
	// audit_redaction_receipts relation absent on a partial migration) is
	// logged and treated as no-drift so a flaky query does not hard-fail.
	orphans, err := CheckRedactionReceipts(ctx, p.DB)
	if err != nil {
		p.logf("WARNING: §11.7 item 2 periodic redaction-receipt scan could not run: %v", err)
		return drift
	}
	for _, o := range orphans {
		if p.OnRedactionReceiptMissing != nil {
			p.OnRedactionReceiptMissing(o.TenantID, o.Missing)
		}
		p.logf("CRITICAL: §12.8 tenant %s has %d redaction-marked audit row(s) with no signature-verifying RedactionReceipt",
			o.TenantID, o.Missing)
	}
	return drift
}

// Run drives CheckOnce on the configured cadence until ctx is cancelled.
// When HardFailOnDrift is set and a cycle detects drift, Run invokes
// Shutdown once and returns; the shutdown hook is responsible for
// terminating the process gracefully.
func (p *PeriodicCheck) Run(ctx context.Context) {
	if p.Cfg.Interval <= 0 {
		return
	}
	t := time.NewTicker(p.Cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if p.CheckOnce(ctx) && p.Cfg.HardFailOnDrift {
				p.shutdown("§11.7 item 2 audit integrity drift detected with audit.hardFailOnDrift enabled")
				return
			}
		}
	}
}

func (p *PeriodicCheck) logf(format string, args ...any) {
	if p.Logf != nil {
		p.Logf(format, args...)
	}
}

func (p *PeriodicCheck) shutdown(reason string) {
	p.logf("CRITICAL: %s — initiating graceful shutdown", reason)
	if p.Shutdown != nil {
		p.Shutdown(reason)
	}
}
