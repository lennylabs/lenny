// SPDX-License-Identifier: MIT

package replication_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore/replication"
)

// auditRecorder collects the §25.11 replication audit events.
type auditRecorder struct {
	events []replication.AuditEvent
}

func (a *auditRecorder) sink(e replication.AuditEvent) { a.events = append(a.events, e) }

func (a *auditRecorder) count(eventType string) int {
	n := 0
	for _, e := range a.events {
		if e.Type == eventType {
			n++
		}
	}
	return n
}

// metricsRecorder collects the §25.11 replication metric signals.
type metricsRecorder struct {
	violations map[string]int
	verified   map[string]int
}

func newMetricsRecorder() *metricsRecorder {
	return &metricsRecorder{violations: map[string]int{}, verified: map[string]int{}}
}

func (m *metricsRecorder) ResidencyViolation(region string)  { m.violations[region]++ }
func (m *metricsRecorder) ReplicationVerified(region string) { m.verified[region]++ }

var replNow = time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

// euRegion is a §25.11 region with an EU residency constraint and a
// declared target.
func euRegion() replication.RegionConfig {
	return replication.RegionConfig{
		Region:              "eu-west-1",
		SourceBucket:        "lenny-artifacts-eu",
		DataResidencyRegion: "eu-west-1",
		Target: replication.Target{
			Endpoint:               "https://artifact-backup.lenny-dr:9000",
			Bucket:                 "lenny-artifacts-eu-backup",
			AccessCredentialSecret: "lenny-backup-minio-eu",
			KMSKeyID:               "kms-eu",
		},
	}
}

func TestConfigValidateRejectsIncompleteTarget(t *testing.T) {
	cfg := replication.Config{
		Enabled: true,
		Regions: []replication.RegionConfig{{
			Region:              "eu-west-1",
			SourceBucket:        "lenny-artifacts-eu",
			DataResidencyRegion: "eu-west-1",
			// Target left empty: a residency-constrained region must have
			// a complete target.
		}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a residency region with no target")
	}
}

func TestConfigValidateRejectsBadInterval(t *testing.T) {
	cfg := replication.Config{
		Enabled:                       true,
		ResidencyCheckIntervalSeconds: 30, // below the §25.11 minimum of 60
		Regions:                       []replication.RegionConfig{euRegion()},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate accepted residencyCheckIntervalSeconds below the minimum")
	}
}

func TestConfigValidateDisabledSkipsChecks(t *testing.T) {
	// A disabled config (Tier 1 dev) is always valid.
	cfg := replication.Config{Enabled: false, Regions: []replication.RegionConfig{{Region: "x"}}}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate of a disabled config = %v, want nil", err)
	}
}

// newController builds a replication Controller over a FakeDriver with
// a deterministic clock.
func newController(t *testing.T, cfg replication.Config, driver *replication.FakeDriver, audit replication.AuditSink, metrics replication.Metrics) *replication.Controller {
	t.Helper()
	c, err := replication.NewController(replication.ControllerConfig{
		Config:  cfg,
		Driver:  driver,
		Audit:   audit,
		Metrics: metrics,
		Now:     func() time.Time { return replNow },
	})
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return c
}

func TestPreflightPassesWhenJurisdictionMatches(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "eu-west-1")
	audit := &auditRecorder{}
	metrics := newMetricsRecorder()
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newController(t, cfg, driver, audit.sink, metrics)

	if err := c.Preflight(context.Background(), "eu-west-1"); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	st, ok, _ := c.GetState(context.Background(), "eu-west-1")
	if !ok || st.State != replication.StateActive {
		t.Errorf("state = %+v, want active", st)
	}
	// §25.11: a passing preflight emits the positive-audit event and
	// increments the verified counter.
	if audit.count("artifact.cross_region_replication_verified") != 1 {
		t.Error("a passing preflight did not emit the positive-audit event")
	}
	if metrics.verified["eu-west-1"] != 1 {
		t.Error("a passing preflight did not increment the verified counter")
	}
}

func TestPreflightSuspendsOnJurisdictionMismatch(t *testing.T) {
	driver := replication.NewFakeDriver()
	// The destination advertises a US jurisdiction; the source is EU.
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "us-east-1")
	audit := &auditRecorder{}
	metrics := newMetricsRecorder()
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newController(t, cfg, driver, audit.sink, metrics)

	err := c.Preflight(context.Background(), "eu-west-1")
	if !errors.Is(err, replication.ErrRegionUnresolvable) {
		t.Fatalf("Preflight error = %v, want ErrRegionUnresolvable", err)
	}
	// §25.11: the region's replication is suspended fail-closed.
	if !driver.IsSuspended("eu-west-1") {
		t.Error("a jurisdiction mismatch did not suspend replication")
	}
	st, _, _ := c.GetState(context.Background(), "eu-west-1")
	if st.State != replication.StateSuspendedResidencyViolation {
		t.Errorf("state = %q, want suspended_residency_violation", st.State)
	}
	// §25.11: a DataResidencyViolationAttempt audit event and the
	// residency-violation counter.
	if audit.count("DataResidencyViolationAttempt") != 1 {
		t.Error("a mismatch did not emit DataResidencyViolationAttempt")
	}
	if metrics.violations["eu-west-1"] != 1 {
		t.Error("a mismatch did not increment the residency-violation counter")
	}
}

func TestPreflightSuspendsOnMissingTag(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetTagAbsent("lenny-artifacts-eu-backup")
	audit := &auditRecorder{}
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newController(t, cfg, driver, audit.sink, nil)

	err := c.Preflight(context.Background(), "eu-west-1")
	if !errors.Is(err, replication.ErrRegionUnresolvable) {
		t.Fatalf("Preflight error = %v, want ErrRegionUnresolvable", err)
	}
	if !driver.IsSuspended("eu-west-1") {
		t.Error("a missing jurisdiction tag did not suspend replication")
	}
}

func TestPreflightSuspendsOnProbeFailure(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetProbeError("lenny-artifacts-eu-backup", errors.New("destination unreachable"))
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newController(t, cfg, driver, nil, nil)

	err := c.Preflight(context.Background(), "eu-west-1")
	if !errors.Is(err, replication.ErrRegionUnresolvable) {
		t.Fatalf("Preflight error = %v, want ErrRegionUnresolvable", err)
	}
	if !driver.IsSuspended("eu-west-1") {
		t.Error("a failed tag probe did not suspend replication")
	}
}

func TestPreflightDNSRebindingGuard(t *testing.T) {
	rc := euRegion()
	// §25.11 second-layer guard: the destination must resolve into the
	// allowed CIDR.
	rc.AllowedDestinationCIDRs = []string{"10.0.0.0/8"}
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "eu-west-1")
	// The endpoint resolves outside the allowed CIDR — a DNS rebinding.
	driver.SetResolvedIPs("https://artifact-backup.lenny-dr:9000", net.ParseIP("203.0.113.5"))
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{rc}}
	c := newController(t, cfg, driver, nil, nil)

	err := c.Preflight(context.Background(), "eu-west-1")
	if !errors.Is(err, replication.ErrRegionUnresolvable) {
		t.Fatalf("Preflight error = %v, want ErrRegionUnresolvable for the rebinding", err)
	}
	if !driver.IsSuspended("eu-west-1") {
		t.Error("a DNS rebinding outside the allowed CIDRs did not suspend replication")
	}
}

func TestPreflightDNSGuardPassesWithinCIDR(t *testing.T) {
	rc := euRegion()
	rc.AllowedDestinationCIDRs = []string{"10.0.0.0/8"}
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "eu-west-1")
	driver.SetResolvedIPs("https://artifact-backup.lenny-dr:9000", net.ParseIP("10.4.2.1"))
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{rc}}
	c := newController(t, cfg, driver, nil, nil)

	if err := c.Preflight(context.Background(), "eu-west-1"); err != nil {
		t.Errorf("Preflight within the allowed CIDR = %v, want nil", err)
	}
}

func TestResumeRejectedWhileMismatchPersists(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "us-east-1")
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newController(t, cfg, driver, nil, nil)

	// Suspend via a failing preflight.
	if err := c.Preflight(context.Background(), "eu-west-1"); err == nil {
		t.Fatal("Preflight should have failed")
	}
	// §25.11: resume re-runs the preflight; a persistent mismatch is
	// rejected and replication stays suspended.
	err := c.Resume(context.Background(), "eu-west-1", "alice", "fixed the config")
	if !errors.Is(err, replication.ErrRegionUnresolvable) {
		t.Fatalf("Resume with a persistent mismatch = %v, want ErrRegionUnresolvable", err)
	}
	if !driver.IsSuspended("eu-west-1") {
		t.Error("a rejected resume left replication un-suspended")
	}
}

func TestResumeSucceedsAfterFix(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "us-east-1")
	audit := &auditRecorder{}
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newController(t, cfg, driver, audit.sink, nil)

	if err := c.Preflight(context.Background(), "eu-west-1"); err == nil {
		t.Fatal("Preflight should have failed")
	}
	// The operator fixes the destination's jurisdiction tag.
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "eu-west-1")
	if err := c.Resume(context.Background(), "eu-west-1", "alice", "re-provisioned the bucket"); err != nil {
		t.Fatalf("Resume after the fix: %v", err)
	}
	if driver.IsSuspended("eu-west-1") {
		t.Error("a successful resume left replication suspended")
	}
	// §25.11: resume emits the artifact_replication.resumed audit event.
	if audit.count("artifact_replication.resumed") != 1 {
		t.Error("a successful resume did not emit artifact_replication.resumed")
	}
}

func TestConfigureSkipsRegionThatFailsPreflight(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "us-east-1") // mismatch
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newController(t, cfg, driver, nil, nil)

	if err := c.Configure(context.Background()); err == nil {
		t.Fatal("Configure should report the failing region")
	}
	// §25.11: a region that fails the preflight is not configured for
	// replication; it is left suspended.
	if driver.IsConfigured("eu-west-1") {
		t.Error("a region that failed the preflight was configured for replication")
	}
}

func TestConfigureEstablishesReplicationOnHealthyRegion(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "eu-west-1")
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{euRegion()}}
	c := newController(t, cfg, driver, nil, nil)

	if err := c.Configure(context.Background()); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if !driver.IsConfigured("eu-west-1") {
		t.Error("Configure did not establish replication on the healthy region")
	}
}

func TestPositiveAuditIsSampled(t *testing.T) {
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-eu-backup", "eu-west-1")
	audit := &auditRecorder{}
	cfg := replication.Config{
		Enabled:                             true,
		ResidencyAuditSamplingWindowSeconds: 3600,
		Regions:                             []replication.RegionConfig{euRegion()},
	}
	c := newController(t, cfg, driver, audit.sink, nil)

	// Two preflights within the same sampling window.
	for i := 0; i < 2; i++ {
		if err := c.Preflight(context.Background(), "eu-west-1"); err != nil {
			t.Fatalf("Preflight %d: %v", i, err)
		}
	}
	// §25.11: only the first positive-audit event per window is emitted.
	if got := audit.count("artifact.cross_region_replication_verified"); got != 1 {
		t.Errorf("positive-audit events = %d, want 1 (sampled per window)", got)
	}
}

func TestRegionWithoutResidencyConstraintStillProbed(t *testing.T) {
	// A single-region deployment with no residency constraint: the
	// preflight still verifies the destination tag is present and the
	// destination is reachable, but does not compare jurisdictions.
	rc := replication.RegionConfig{
		Region:       "default",
		SourceBucket: "lenny-artifacts",
		Target: replication.Target{
			Endpoint:               "https://artifact-backup:9000",
			Bucket:                 "lenny-artifacts-backup",
			AccessCredentialSecret: "lenny-backup-minio",
		},
	}
	driver := replication.NewFakeDriver()
	driver.SetJurisdictionTag("lenny-artifacts-backup", "any-region")
	cfg := replication.Config{Enabled: true, Regions: []replication.RegionConfig{rc}}
	c := newController(t, cfg, driver, nil, nil)

	if err := c.Preflight(context.Background(), "default"); err != nil {
		t.Errorf("Preflight of an unconstrained region = %v, want nil", err)
	}
}
