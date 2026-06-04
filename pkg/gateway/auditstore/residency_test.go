// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// --- fakes for the §11.7 CMP-058 residency seams -------------------------

// regionRouterFake satisfies PlatformRegionRouter. byRegion maps a region
// to its pool; an absent region returns ErrPlatformRegionUnresolvable
// (the storerouter missing_entry contract).
type regionRouterFake struct {
	byRegion map[string]*pgxpool.Pool
	global   *pgxpool.Pool
}

func (f regionRouterFake) PlatformPostgresForRegion(_ context.Context, region string) (*pgxpool.Pool, error) {
	if region == "" {
		return f.global, nil
	}
	if p, ok := f.byRegion[region]; ok {
		return p, nil
	}
	return nil, storerouter.ErrPlatformRegionUnresolvable
}

// lookupFake satisfies ResidencyLookup.
type lookupFake struct {
	region string
	err    error
}

func (f lookupFake) TargetResidencyRegion(context.Context, string) (string, error) {
	return f.region, f.err
}

// metricsFake satisfies ResidencyMetrics and records the bumps.
type metricsFake struct {
	residency    []string    // operations passed to IncDataResidencyViolation
	unresolvable [][2]string // (region, failureMode) pairs
}

func (m *metricsFake) IncDataResidencyViolation(op string) {
	m.residency = append(m.residency, op)
}
func (m *metricsFake) IncPlatformAuditRegionUnresolvable(region, failureMode string) {
	m.unresolvable = append(m.unresolvable, [2]string{region, failureMode})
}

func newResidencyRouter(regions regionRouterFake, lookup lookupFake, metrics ResidencyMetrics, ping func(context.Context, *pgxpool.Pool) error) *platformResidencyRouter {
	return &platformResidencyRouter{
		platformTenantID: defaultPlatformTenantID,
		regions:          regions,
		lookup:           lookup,
		metrics:          metrics,
		ping:             ping,
	}
}

// --- decide: the §11.7 lines 431-433 three-rule routing ------------------

// spec: §11.7 line 432 (rule 2) — a target tenant with no
// dataResidencyRegion routes to the global platform-Postgres.
func TestResidencyDecideRule2GlobalWhenNoRegion_spec_11_7_432(t *testing.T) {
	pr := newResidencyRouter(regionRouterFake{}, lookupFake{region: ""}, &metricsFake{}, nil)
	out, err := pr.decide(context.Background(), "acme")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !out.global || out.pool != nil || out.failureMode != "" {
		t.Fatalf("rule 2: got %+v, want global=true", out)
	}
}

// spec: §11.7 line 431 (rule 1) — a target tenant with a resolvable
// dataResidencyRegion routes to that region's platform-Postgres.
func TestResidencyDecideRule1RegionalPool_spec_11_7_431(t *testing.T) {
	euPool := &pgxpool.Pool{}
	pr := newResidencyRouter(
		regionRouterFake{byRegion: map[string]*pgxpool.Pool{"eu-west-1": euPool}},
		lookupFake{region: "eu-west-1"},
		&metricsFake{},
		func(context.Context, *pgxpool.Pool) error { return nil }, // reachable
	)
	out, err := pr.decide(context.Background(), "acme")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if out.global || out.failureMode != "" || out.pool != euPool || out.region != "eu-west-1" {
		t.Fatalf("rule 1: got %+v, want pool=euPool region=eu-west-1", out)
	}
}

// spec: §11.7 line 433 (rule 3, missing_entry) — a dataResidencyRegion
// with no storage.regions entry fails closed.
func TestResidencyDecideRule3MissingEntry_spec_11_7_433(t *testing.T) {
	pr := newResidencyRouter(
		regionRouterFake{byRegion: map[string]*pgxpool.Pool{"eu-west-1": &pgxpool.Pool{}}},
		lookupFake{region: "ap-south-1"}, // not configured
		&metricsFake{},
		func(context.Context, *pgxpool.Pool) error { return nil },
	)
	out, err := pr.decide(context.Background(), "acme")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if out.failureMode != failureModeMissingEntry || out.region != "ap-south-1" || out.pool != nil {
		t.Fatalf("rule 3 missing_entry: got %+v", out)
	}
}

// spec: §11.7 line 433 (rule 3, postgres_unreachable) — a configured
// region whose platform-Postgres is unreachable fails closed.
func TestResidencyDecideRule3PostgresUnreachable_spec_11_7_433(t *testing.T) {
	euPool := &pgxpool.Pool{}
	pr := newResidencyRouter(
		regionRouterFake{byRegion: map[string]*pgxpool.Pool{"eu-west-1": euPool}},
		lookupFake{region: "eu-west-1"},
		&metricsFake{},
		func(context.Context, *pgxpool.Pool) error { return errors.New("dial timeout") },
	)
	out, err := pr.decide(context.Background(), "acme")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if out.failureMode != failureModePostgresUnreachable || out.region != "eu-west-1" || out.pool != nil {
		t.Fatalf("rule 3 postgres_unreachable: got %+v", out)
	}
}

// A residency-lookup failure halts the write (audit must be durable before
// any externally observable side effect). spec: §11.7 line 433.
func TestResidencyDecideLookupErrorHaltsWrite_spec_11_7_433(t *testing.T) {
	pr := newResidencyRouter(regionRouterFake{}, lookupFake{err: errors.New("store down")}, &metricsFake{}, nil)
	if _, err := pr.decide(context.Background(), "acme"); err == nil {
		t.Fatal("lookup error: expected a propagated error, got nil")
	}
}

// --- failClosedPlatformAudit through Append ------------------------------

// errAuditRouter satisfies the auditstore Router but returns an error from
// every shard accessor, so the fail-closed violation-record write is
// skipped (s.writeShard returns an error) and no Postgres dial occurs.
type errAuditRouter struct{}

func (errAuditRouter) AuditShard(context.Context, storerouter.TenantID) (*pgxpool.Pool, error) {
	return nil, errors.New("no audit shard in unit test")
}
func (errAuditRouter) AllAuditShards(context.Context) ([]storerouter.ShardHandle, error) {
	return nil, errors.New("no audit shards in unit test")
}

// spec: §11.7 line 433 (CMP-058) — Append for a platform-tenant event that
// references an unresolvable target tenant fails closed with
// PlatformAuditRegionUnresolvableError and bumps both residency counters.
func TestAppendPlatformTargetedFailsClosedAndBumpsMetrics_spec_11_7_433(t *testing.T) {
	mx := &metricsFake{}
	s := New(errAuditRouter{}, WithPlatformAuditResidency(
		"platform",
		regionRouterFake{}, // empty map: every region is missing_entry
		lookupFake{region: "eu-west-1"},
		mx,
	))
	payload := json.RawMessage(`{"target_tenant_id":"acme","operation":"impersonation"}`)
	_, err := s.Append(context.Background(), "platform", "admin.impersonation_started", payload, time.Unix(0, 0))

	var unresolvable *PlatformAuditRegionUnresolvableError
	if !errors.As(err, &unresolvable) {
		t.Fatalf("Append: got err %v, want *PlatformAuditRegionUnresolvableError", err)
	}
	if unresolvable.Region != "eu-west-1" || unresolvable.FailureMode != failureModeMissingEntry {
		t.Errorf("error fields: got %+v", unresolvable)
	}
	if unresolvable.Code() != "PLATFORM_AUDIT_REGION_UNRESOLVABLE" || unresolvable.HTTPStatus() != 422 {
		t.Errorf("error mapping: code=%q status=%d", unresolvable.Code(), unresolvable.HTTPStatus())
	}
	if len(mx.residency) != 1 || mx.residency[0] != operationPlatformAuditWrite {
		t.Errorf("data-residency counter: got %v, want [platform_audit_write]", mx.residency)
	}
	if len(mx.unresolvable) != 1 || mx.unresolvable[0] != [2]string{"eu-west-1", failureModeMissingEntry} {
		t.Errorf("unresolvable counter: got %v", mx.unresolvable)
	}
}

// The gate is keyed on (platform tenant + non-platform target_tenant_id).
// A non-platform tenant, an absent target, or a self-target does NOT engage
// residency routing: the write follows the normal §12.3 R-03 shard path
// (here the errAuditRouter surfaces its shard error, and no residency
// counter is bumped). spec: §11.7 lines 430, 435.
func TestAppendDoesNotEngageResidencyGateOffPath_spec_11_7_435(t *testing.T) {
	cases := []struct {
		name    string
		tenant  string
		payload string
	}{
		{"non-platform tenant", "acme", `{"target_tenant_id":"globex"}`},
		{"no target_tenant_id", "platform", `{"operation":"upgrade"}`},
		{"self-target", "platform", `{"target_tenant_id":"platform"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mx := &metricsFake{}
			s := New(errAuditRouter{}, WithPlatformAuditResidency(
				"platform", regionRouterFake{}, lookupFake{region: "eu-west-1"}, mx))
			_, err := s.Append(context.Background(), tc.tenant, "evt", json.RawMessage(tc.payload), time.Unix(0, 0))

			var unresolvable *PlatformAuditRegionUnresolvableError
			if errors.As(err, &unresolvable) {
				t.Fatalf("gate should not engage: got residency error %v", err)
			}
			if err == nil {
				t.Fatal("expected the normal-path shard error, got nil")
			}
			if len(mx.residency) != 0 || len(mx.unresolvable) != 0 {
				t.Errorf("no residency counter expected: residency=%v unresolvable=%v", mx.residency, mx.unresolvable)
			}
		})
	}
}

// --- helpers -------------------------------------------------------------

func TestTargetTenantIDExtraction_spec_11_7_430(t *testing.T) {
	cases := []struct {
		payload string
		want    string
	}{
		{`{"target_tenant_id":"acme"}`, "acme"},
		{`{"operation":"x"}`, ""},
		{``, ""},
		{`not-json`, ""},
		{`{"target_tenant_id":""}`, ""},
	}
	for _, tc := range cases {
		if got := targetTenantID(json.RawMessage(tc.payload)); got != tc.want {
			t.Errorf("targetTenantID(%q) = %q, want %q", tc.payload, got, tc.want)
		}
	}
}

// spec: §11.7 line 433 — the DataResidencyViolationAttempt payload carries
// operation, tenant_id (platform), target_tenant_id, requested_region,
// event_type, and failure_mode.
func TestBuildViolationPayloadFields_spec_11_7_433(t *testing.T) {
	raw, err := buildViolationPayload("platform", "acme", "eu-west-1", "admin.impersonation_started", failureModePostgresUnreachable)
	if err != nil {
		t.Fatalf("buildViolationPayload: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]string{
		"operation":        "platform_audit_write",
		"tenant_id":        "platform",
		"target_tenant_id": "acme",
		"requested_region": "eu-west-1",
		"event_type":       "admin.impersonation_started",
		"failure_mode":     "postgres_unreachable",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("payload[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// recordViolation bumps both residency series with the spec labels.
// spec: §11.7 line 433.
func TestRecordViolationBumpsBothCounters_spec_11_7_433(t *testing.T) {
	mx := &metricsFake{}
	pr := newResidencyRouter(regionRouterFake{}, lookupFake{}, mx, nil)
	pr.recordViolation("us-east-1", failureModeMissingEntry)
	if len(mx.residency) != 1 || mx.residency[0] != operationPlatformAuditWrite {
		t.Errorf("data-residency: got %v", mx.residency)
	}
	if len(mx.unresolvable) != 1 || mx.unresolvable[0] != [2]string{"us-east-1", failureModeMissingEntry} {
		t.Errorf("unresolvable: got %v", mx.unresolvable)
	}
}

// The CMP-058 event-type literal must match the audit catalog so the two
// cannot drift. spec: §11.7 line 433; §16.7 audit catalog.
func TestPlatformAuditViolationEventTypeMatchesCatalog(t *testing.T) {
	if eventDataResidencyViolationAttempt != string(obsaudit.EventDataResidencyViolationAttempt) {
		t.Fatalf("event-type drift: local %q vs catalog %q",
			eventDataResidencyViolationAttempt, obsaudit.EventDataResidencyViolationAttempt)
	}
}

// WithPlatformAuditResidency is inert when a required collaborator is nil,
// so a misconfiguration cannot silently swallow audit writes; it defaults
// the platform tenant id and a nil metrics sink. F-11.7.9.
func TestWithPlatformAuditResidencyGuards(t *testing.T) {
	// nil regions -> gate not installed.
	s := New(errAuditRouter{}, WithPlatformAuditResidency("platform", nil, lookupFake{}, &metricsFake{}))
	if s.platformResidency != nil {
		t.Error("nil regions: gate should not be installed")
	}
	// nil lookup -> gate not installed.
	s = New(errAuditRouter{}, WithPlatformAuditResidency("platform", regionRouterFake{}, nil, &metricsFake{}))
	if s.platformResidency != nil {
		t.Error("nil lookup: gate should not be installed")
	}
	// empty platform tenant id defaults to "platform"; nil metrics -> noop.
	s = New(errAuditRouter{}, WithPlatformAuditResidency("", regionRouterFake{}, lookupFake{}, nil))
	if s.platformResidency == nil {
		t.Fatal("gate should be installed")
	}
	if s.platformResidency.platformTenantID != defaultPlatformTenantID {
		t.Errorf("platform tenant id default: got %q", s.platformResidency.platformTenantID)
	}
	if s.platformResidency.metrics == nil {
		t.Error("nil metrics should default to a noop sink, not nil")
	}
	// the noop sink must not panic.
	s.platformResidency.recordViolation("eu-west-1", failureModeMissingEntry)
}
