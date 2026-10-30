// SPDX-License-Identifier: MIT

package replication

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// AuditEvent is one §25.11 ArtifactStore-replication audit event the
// Controller emits. The sink is supplied by the caller so the audit
// trail integrates with the platform's audit pipeline.
type AuditEvent struct {
	// Type is the §25.11 audit event type:
	// artifact.cross_region_replication_verified on a passing preflight,
	// DataResidencyViolationAttempt on a failing one,
	// artifact_replication.resumed when an operator resumes replication.
	Type string
	// Region is the source region the event concerns.
	Region string
	// SourceDataResidencyRegion is the source region's residency
	// jurisdiction.
	SourceDataResidencyRegion string
	// DestinationEndpoint and DestinationBucket identify the replication
	// destination.
	DestinationEndpoint string
	DestinationBucket   string
	// DestinationJurisdictionTag is the lenny.dev/jurisdiction-region tag
	// read from the destination bucket.
	DestinationJurisdictionTag string
	// Operation is the §25.11 DataResidencyViolationAttempt operation
	// label; "artifact_replication" for this subsystem.
	Operation string
	// Detail is the failure reason on a violation event.
	Detail string
	// At is the event timestamp.
	At time.Time
}

// AuditSink receives the §25.11 ArtifactStore-replication audit events.
type AuditSink func(AuditEvent)

// Metrics receives the §25.11 ArtifactStore-replication metric
// signals. The implementation increments the platform's Prometheus
// counters; a nil Metrics drops the signals.
type Metrics interface {
	// ResidencyViolation increments lenny_minio_replication_residency_-
	// violation_total and the shared lenny_data_residency_violation_total
	// for a region.
	ResidencyViolation(region string)
	// ReplicationVerified records a passing residency preflight for a
	// region (the positive-audit counterpart).
	ReplicationVerified(region string)
}

// StateStore persists the §25.11 ops_artifact_replication_state rows:
// one row per region recording the replication state and the last
// preflight outcome. A nil StateStore keeps the state in memory only.
type StateStore interface {
	// PutReplicationState writes a region's replication state row.
	PutReplicationState(ctx context.Context, st RegionState) error
	// GetReplicationState reads a region's replication state row. ok is
	// false when no row exists yet.
	GetReplicationState(ctx context.Context, region string) (st RegionState, ok bool, err error)
}

// RegionState is the §25.11 ops_artifact_replication_state row for one
// region.
type RegionState struct {
	Region                     string    `json:"region"`
	State                      State     `json:"status"`
	LastPreflightAt            time.Time `json:"lastPreflightAt"`
	LastPreflightResult        string    `json:"lastPreflightResult"`
	DestinationEndpoint        string    `json:"destinationEndpoint"`
	DestinationBucket          string    `json:"destinationBucket"`
	DestinationJurisdictionTag string    `json:"destinationJurisdictionTag,omitempty"`
	ReplicationLagSeconds      int       `json:"replicationLagSeconds"`
	SuspendedSince             time.Time `json:"suspendedSince,omitempty"`
}

// ControllerConfig assembles a §25.11 replication Controller.
type ControllerConfig struct {
	// Config is the §25.11 minio.artifactBackup configuration. Required.
	Config Config
	// Driver is the MinIO-facing seam. Required.
	Driver Driver
	// State persists the ops_artifact_replication_state rows; nil keeps
	// state in memory.
	State StateStore
	// Audit receives the §25.11 audit events; nil drops them.
	Audit AuditSink
	// Metrics receives the §25.11 metric signals; nil drops them.
	Metrics Metrics
	// Lag receives the §25.11 / §17.3 replication-lag and
	// replication-failure signals MeasureAll samples; nil drops them.
	Lag LagObserver
	// Now supplies the current time; nil uses time.Now in UTC.
	Now func() time.Time
}

// Controller runs the §25.11 ArtifactStore continuous replication: it
// configures replication on each region, runs the runtime residency
// preflight before every batch and on the periodic tick, and suspends
// replication fail-closed on a jurisdiction mismatch.
type Controller struct {
	cfg     Config
	driver  Driver
	state   StateStore
	audit   AuditSink
	metrics Metrics
	lag     LagObserver
	now     func() time.Time

	mu sync.Mutex
	// inMem holds the per-region state when no StateStore is configured.
	inMem map[string]RegionState
	// lastAuditAt records the last positive-audit emission per region,
	// for the §25.11 sampled artifact.cross_region_replication_verified
	// event.
	lastAuditAt map[string]time.Time
}

// NewController builds a §25.11 replication Controller from cfg. It
// returns an error when the configuration is invalid or a required
// dependency is missing.
func NewController(cfg ControllerConfig) (*Controller, error) {
	if cfg.Driver == nil {
		return nil, fmt.Errorf("replication: Controller requires a Driver")
	}
	if err := cfg.Config.Validate(); err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Controller{
		cfg:         cfg.Config,
		driver:      cfg.Driver,
		state:       cfg.State,
		audit:       cfg.Audit,
		metrics:     cfg.Metrics,
		lag:         cfg.Lag,
		now:         now,
		inMem:       map[string]RegionState{},
		lastAuditAt: map[string]time.Time{},
	}, nil
}

// Configure establishes continuous replication on every enabled region.
// It runs the residency preflight first: a region that fails the
// preflight is left suspended and is not configured for replication.
// Configure is idempotent — re-running it re-asserts the rule.
func (c *Controller) Configure(ctx context.Context) error {
	if !c.cfg.Enabled {
		return nil
	}
	var firstErr error
	for _, rc := range c.cfg.Regions {
		if rc.Target.Endpoint == "" {
			// A region with no target declared (a dev region) is skipped.
			continue
		}
		if err := c.Preflight(ctx, rc.Region); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			// §25.11: a failing preflight suspends the region; do not
			// configure replication for it.
			continue
		}
		if err := c.driver.ConfigureReplication(ctx, rc); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("region %s: %w", rc.Region, err)
			}
		}
	}
	return firstErr
}

// Preflight runs the §25.11 runtime residency preflight for one region:
// it probes the destination bucket's jurisdiction tag, compares it to
// the source region's dataResidencyRegion, and verifies the destination
// endpoint resolves into the allowed CIDRs. On any failure it suspends
// replication fail-closed, records the suspension, emits the
// DataResidencyViolationAttempt audit event, increments the residency-
// violation counter, and returns ErrRegionUnresolvable. On success it
// records the active state and emits the sampled positive-audit event.
//
// §25.11: there is no silent retry and no automatic resume — a
// residency mismatch is a hard compliance fault. Resume re-runs the
// preflight and only clears the suspension when it passes.
func (c *Controller) Preflight(ctx context.Context, region string) error {
	rc, ok := c.regionConfig(region)
	if !ok {
		return fmt.Errorf("replication: no configuration for region %q", region)
	}
	now := c.now()

	tag, present, err := c.driver.ProbeJurisdiction(ctx, rc.Target)
	if err != nil {
		return c.fail(ctx, rc, now, "destination tag probe failed: "+err.Error())
	}
	if !present {
		return c.fail(ctx, rc, now, fmt.Sprintf(
			"destination bucket %s has no %s tag", rc.Target.Bucket, jurisdictionTagKey,
		))
	}
	// §25.11: when the source region carries a residency constraint, the
	// destination's jurisdiction tag MUST equal it.
	if rc.DataResidencyRegion != "" && tag != rc.DataResidencyRegion {
		return c.fail(ctx, rc, now, fmt.Sprintf(
			"destination jurisdiction tag %q does not match source residency region %q",
			tag, rc.DataResidencyRegion,
		))
	}
	// §25.11 second-layer DNS-rebinding guard: when allowedDestinationCidrs
	// is set, the destination endpoint MUST resolve into one of them.
	if len(rc.AllowedDestinationCIDRs) > 0 {
		ips, err := c.driver.ResolveEndpointIPs(ctx, rc.Target.Endpoint)
		if err != nil {
			return c.fail(ctx, rc, now, "destination DNS resolution failed: "+err.Error())
		}
		if !ipsWithinCIDRs(ips, rc.AllowedDestinationCIDRs) {
			return c.fail(ctx, rc, now, fmt.Sprintf(
				"destination endpoint %s resolves outside allowedDestinationCidrs", rc.Target.Endpoint,
			))
		}
	}

	// Preflight passed: record the active state and emit the sampled
	// positive-audit event.
	st := RegionState{
		Region:                     region,
		State:                      StateActive,
		LastPreflightAt:            now,
		LastPreflightResult:        "ok",
		DestinationEndpoint:        rc.Target.Endpoint,
		DestinationBucket:          rc.Target.Bucket,
		DestinationJurisdictionTag: tag,
	}
	if err := c.putState(ctx, st); err != nil {
		return err
	}
	if c.metrics != nil {
		c.metrics.ReplicationVerified(region)
	}
	c.emitVerified(rc, tag, now)
	return nil
}

// fail records a §25.11 residency violation: it suspends replication,
// records the suspended state, emits the DataResidencyViolationAttempt
// audit event, increments the residency-violation counter, and returns
// ErrRegionUnresolvable wrapping the detail.
func (c *Controller) fail(ctx context.Context, rc RegionConfig, now time.Time, detail string) error {
	// §25.11: suspend replication on the source MinIO cluster
	// fail-closed. A SuspendReplication error does not mask the
	// violation — the violation is still recorded and returned.
	_ = c.driver.SuspendReplication(ctx, rc)

	st := RegionState{
		Region:              rc.Region,
		State:               StateSuspendedResidencyViolation,
		LastPreflightAt:     now,
		LastPreflightResult: detail,
		DestinationEndpoint: rc.Target.Endpoint,
		DestinationBucket:   rc.Target.Bucket,
		SuspendedSince:      now,
	}
	_ = c.putState(ctx, st)

	if c.audit != nil {
		c.audit(AuditEvent{
			Type:                      "DataResidencyViolationAttempt",
			Region:                    rc.Region,
			SourceDataResidencyRegion: rc.DataResidencyRegion,
			DestinationEndpoint:       rc.Target.Endpoint,
			DestinationBucket:         rc.Target.Bucket,
			Operation:                 "artifact_replication",
			Detail:                    detail,
			At:                        now,
		})
	}
	if c.metrics != nil {
		c.metrics.ResidencyViolation(rc.Region)
	}
	return fmt.Errorf("%w: region %s: %s", ErrRegionUnresolvable, rc.Region, detail)
}

// Resume re-runs the §25.11 residency preflight for a suspended region
// and clears the suspension only when the preflight passes. just
// records the operator justification on the audit trail. §25.11
// requires platform-admin; the HTTP layer enforces the role and this
// method assumes an authorized caller.
func (c *Controller) Resume(ctx context.Context, region, operatorSub, justification string) error {
	rc, ok := c.regionConfig(region)
	if !ok {
		return fmt.Errorf("replication: no configuration for region %q", region)
	}
	// §25.11: the preflight re-runs synchronously on resume — if the
	// mismatch is still present, the resume is rejected and replication
	// remains suspended.
	if err := c.Preflight(ctx, region); err != nil {
		return err
	}
	// The preflight passed; re-enable replication on the source cluster.
	if err := c.driver.ResumeReplication(ctx, rc); err != nil {
		return fmt.Errorf("replication: resume region %s: %w", region, err)
	}
	now := c.now()
	tag := ""
	if st, ok, _ := c.GetState(ctx, region); ok {
		tag = st.DestinationJurisdictionTag
	}
	if c.audit != nil {
		c.audit(AuditEvent{
			Type:                       "artifact_replication.resumed",
			Region:                     region,
			SourceDataResidencyRegion:  rc.DataResidencyRegion,
			DestinationEndpoint:        rc.Target.Endpoint,
			DestinationBucket:          rc.Target.Bucket,
			DestinationJurisdictionTag: tag,
			Detail:                     justification,
			At:                         now,
		})
	}
	return nil
}

// PreflightAll runs the §25.11 periodic residency tick: it runs the
// preflight for every enabled region with a declared target. A region
// already suspended is re-checked so a fixed misconfiguration is not
// auto-resumed (resume is operator-driven) but a newly-drifted region
// is caught. PreflightAll returns the first error encountered; it does
// not stop on the first failing region.
func (c *Controller) PreflightAll(ctx context.Context) error {
	if !c.cfg.Enabled {
		return nil
	}
	var firstErr error
	for _, rc := range c.cfg.Regions {
		if rc.Target.Endpoint == "" {
			continue
		}
		if err := c.Preflight(ctx, rc.Region); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ResidencyTickInterval returns the §25.11 residency-preflight tick
// interval, so the lenny-ops loop runner schedules PreflightAll at the
// configured cadence.
func (c *Controller) ResidencyTickInterval() time.Duration {
	return c.cfg.residencyCheckInterval()
}

// ReplicationLagRPO returns the §25.11 replication-lag RPO threshold,
// for the lag-alert evaluation.
func (c *Controller) ReplicationLagRPO() time.Duration {
	return c.cfg.lagRPO()
}

// GetState returns a region's current §25.11 ops_artifact_replication_-
// state row.
func (c *Controller) GetState(ctx context.Context, region string) (RegionState, bool, error) {
	if c.state != nil {
		return c.state.GetReplicationState(ctx, region)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.inMem[region]
	return st, ok, nil
}

// emitVerified emits the §25.11 sampled artifact.cross_region_-
// replication_verified positive-audit event: the first event per
// region per residencyAuditSamplingWindowSeconds window.
func (c *Controller) emitVerified(rc RegionConfig, tag string, now time.Time) {
	if c.audit == nil {
		return
	}
	c.mu.Lock()
	last, seen := c.lastAuditAt[rc.Region]
	within := seen && now.Sub(last) < c.cfg.auditSamplingWindow()
	if !within {
		c.lastAuditAt[rc.Region] = now
	}
	c.mu.Unlock()
	if within {
		return
	}
	c.audit(AuditEvent{
		Type:                       "artifact.cross_region_replication_verified",
		Region:                     rc.Region,
		SourceDataResidencyRegion:  rc.DataResidencyRegion,
		DestinationEndpoint:        rc.Target.Endpoint,
		DestinationBucket:          rc.Target.Bucket,
		DestinationJurisdictionTag: tag,
		At:                         now,
	})
}

// putState persists a region's replication state, falling back to the
// in-memory map when no StateStore is configured.
func (c *Controller) putState(ctx context.Context, st RegionState) error {
	if c.state != nil {
		if err := c.state.PutReplicationState(ctx, st); err != nil {
			return fmt.Errorf("replication: persist state for %s: %w", st.Region, err)
		}
		return nil
	}
	c.mu.Lock()
	c.inMem[st.Region] = st
	c.mu.Unlock()
	return nil
}

// regionConfig looks up a region's configuration by key.
func (c *Controller) regionConfig(region string) (RegionConfig, bool) {
	for _, rc := range c.cfg.Regions {
		if rc.Region == region {
			return rc, true
		}
	}
	return RegionConfig{}, false
}

// ipsWithinCIDRs reports whether every resolved IP falls within one of
// the allowed CIDRs. An unparseable CIDR is skipped; an IP matching no
// CIDR fails the check.
func ipsWithinCIDRs(ips []net.IP, cidrs []string) bool {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}
	if len(nets) == 0 {
		return false
	}
	for _, ip := range ips {
		matched := false
		for _, n := range nets {
			if n.Contains(ip) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
