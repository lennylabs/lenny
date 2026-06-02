// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/blobstore/replication"
	"github.com/lennylabs/lenny/pkg/gateway/gatewaymetrics"
)

// metricsBody renders the gateway /metrics body once.
func metricsBody(t *testing.T, m *gatewaymetrics.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d", rr.Code)
	}
	return rr.Body.String()
}

// fakeAppender captures audit.Append calls for the replication-sink tests.
type fakeAppender struct {
	mu    sync.Mutex
	calls []appendCall
}

type appendCall struct {
	tenant    string
	eventType string
	payload   json.RawMessage
}

func (f *fakeAppender) Append(_ context.Context, tenant, eventType string, payload json.RawMessage, _ time.Time) (audit.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, appendCall{tenant, eventType, append(json.RawMessage(nil), payload...)})
	return audit.Row{}, nil
}

func (f *fakeAppender) snapshot() []appendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]appendCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// spec: §25.11 — parseReplicationConfig decodes the JSON config and runs
// the startup CONFIG_INVALID validation. F-12.5.20.
func TestParseReplicationConfig(t *testing.T) {
	t.Run("empty is disabled", func(t *testing.T) {
		cfg, err := parseReplicationConfig("")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if cfg.Enabled {
			t.Fatalf("empty config must be disabled")
		}
	})
	t.Run("whitespace is disabled", func(t *testing.T) {
		cfg, err := parseReplicationConfig("   \n ")
		if err != nil || cfg.Enabled {
			t.Fatalf("whitespace config: cfg.Enabled=%v err=%v", cfg.Enabled, err)
		}
	})
	t.Run("valid enabled region", func(t *testing.T) {
		raw := `{"enabled":true,"residencyCheckIntervalSeconds":120,"regions":[{"region":"eu-west-1","sourceBucket":"src","dataResidencyRegion":"eu-west-1","target":{"endpoint":"https://dest:9000","bucket":"dest-bucket","accessCredentialSecret":"repl-dest"}}]}`
		cfg, err := parseReplicationConfig(raw)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !cfg.Enabled || len(cfg.Regions) != 1 {
			t.Fatalf("parsed: %+v", cfg)
		}
		if cfg.Regions[0].Target.Bucket != "dest-bucket" || cfg.Regions[0].DataResidencyRegion != "eu-west-1" {
			t.Fatalf("region fields not mapped: %+v", cfg.Regions[0])
		}
	})
	t.Run("malformed json errors", func(t *testing.T) {
		if _, err := parseReplicationConfig("{not json"); err == nil {
			t.Fatalf("expected error")
		}
	})
	t.Run("unknown field errors", func(t *testing.T) {
		if _, err := parseReplicationConfig(`{"enabled":true,"bogus":1}`); err == nil {
			t.Fatalf("expected error on unknown field")
		}
	})
	t.Run("CONFIG_INVALID on residency without target", func(t *testing.T) {
		raw := `{"enabled":true,"regions":[{"region":"eu-west-1","sourceBucket":"src","dataResidencyRegion":"eu-west-1"}]}`
		_, err := parseReplicationConfig(raw)
		if err == nil || !strings.Contains(err.Error(), "CONFIG_INVALID") {
			t.Fatalf("expected CONFIG_INVALID, got %v", err)
		}
	})
	t.Run("CONFIG_INVALID on out-of-range interval", func(t *testing.T) {
		raw := `{"enabled":true,"residencyCheckIntervalSeconds":5,"regions":[{"region":"r","sourceBucket":"b"}]}`
		_, err := parseReplicationConfig(raw)
		if err == nil || !strings.Contains(err.Error(), "CONFIG_INVALID") {
			t.Fatalf("expected CONFIG_INVALID, got %v", err)
		}
	})
}

// spec: §25.11 — the destination endpoint scheme controls TLS. F-12.5.20.
func TestSplitMinioEndpoint(t *testing.T) {
	cases := []struct {
		in         string
		wantHost   string
		wantSecure bool
	}{
		{"https://dest:9000", "dest:9000", true},
		{"http://dest:9000", "dest:9000", false},
		{"dest:9000", "dest:9000", false},
		{"minio.lenny-system:9000", "minio.lenny-system:9000", false},
	}
	for _, c := range cases {
		host, secure := splitMinioEndpoint(c.in)
		if host != c.wantHost || secure != c.wantSecure {
			t.Errorf("splitMinioEndpoint(%q) = (%q,%v), want (%q,%v)", c.in, host, secure, c.wantHost, c.wantSecure)
		}
	}
}

// spec: §16.7 — replication audit events land on the platform chain.
// F-12.5.20 / F-16.7.2.
func TestReplicationAuditSinkRoutesToPlatformChain(t *testing.T) {
	app := &fakeAppender{}
	sink := replicationAuditSink{appender: app}
	sink.emit(replication.AuditEvent{
		Type:                "DataResidencyViolationAttempt",
		Region:              "eu-west-1",
		DestinationEndpoint: "https://dest:9000",
		Operation:           "artifact_replication",
		Detail:              "jurisdiction mismatch",
		At:                  time.Unix(1700000000, 0).UTC(),
	})
	calls := app.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want 1 append, got %d", len(calls))
	}
	if calls[0].tenant != "platform" {
		t.Errorf("tenant = %q, want platform", calls[0].tenant)
	}
	if calls[0].eventType != "DataResidencyViolationAttempt" {
		t.Errorf("eventType = %q", calls[0].eventType)
	}
	if !strings.Contains(string(calls[0].payload), "jurisdiction mismatch") ||
		!strings.Contains(string(calls[0].payload), "eu-west-1") {
		t.Errorf("payload missing fields: %s", calls[0].payload)
	}
}

// A nil appender drops the event without panicking.
func TestReplicationAuditSinkNilAppender(t *testing.T) {
	replicationAuditSink{appender: nil}.emit(replication.AuditEvent{Type: "x"})
}

// spec: §25.11 — the metrics adapter bridges the Controller's residency
// signal onto the gateway Prometheus counters; the positive case has no
// counter. F-12.5.20 / F-16.7.2.
func TestReplicationMetricsAdapter(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := replicationMetricsAdapter{m: m}
	a.ResidencyViolation("eu-west-1")
	a.ReplicationVerified("eu-west-1") // no-op, must not panic or emit

	body := metricsBody(t, m)
	if want := `lenny_minio_replication_residency_violation_total{region="eu-west-1"} 1`; !strings.Contains(body, want) {
		t.Errorf("missing %q in:\n%s", want, body)
	}
}

// TestReplicationControllerEndToEnd drives the real Controller with the
// FakeDriver through the gateway adapters: a jurisdiction mismatch must
// suspend, emit DataResidencyViolationAttempt onto the platform chain,
// and bump the residency counter; a matching tag must emit the verified
// event and configure replication. spec: §25.11. F-12.5.20 / F-16.7.2.
func TestReplicationControllerEndToEnd(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app := &fakeAppender{}
	fd := replication.NewFakeDriver()
	fd.SetJurisdictionTag("dest-bucket", "us-east-1") // != source residency eu-west-1

	cfg := replication.Config{
		Enabled: true,
		Regions: []replication.RegionConfig{{
			Region:              "eu-west-1",
			SourceBucket:        "src",
			DataResidencyRegion: "eu-west-1",
			Target: replication.Target{
				Endpoint:               "https://dest:9000",
				Bucket:                 "dest-bucket",
				AccessCredentialSecret: "repl-dest",
			},
		}},
	}
	ctrl, err := replication.NewController(replication.ControllerConfig{
		Config:  cfg,
		Driver:  fd,
		Audit:   replicationAuditSink{appender: app}.emit,
		Metrics: replicationMetricsAdapter{m: m},
	})
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}

	// Mismatch: Configure runs the preflight, which fails closed.
	_ = ctrl.Configure(context.Background())
	if !fd.IsSuspended("eu-west-1") {
		t.Fatalf("region should be suspended on jurisdiction mismatch")
	}
	if fd.IsConfigured("eu-west-1") {
		t.Fatalf("replication must not be configured for a violating region")
	}
	if got := violationEvents(app.snapshot()); got != 1 {
		t.Fatalf("want 1 DataResidencyViolationAttempt, got %d", got)
	}
	if body := metricsBody(t, m); !strings.Contains(body,
		`lenny_minio_replication_residency_violation_total{region="eu-west-1"} 1`) {
		t.Fatalf("residency violation metric not incremented:\n%s", body)
	}

	// Fix the tag and re-run: the preflight passes and emits the verified
	// positive-audit event.
	fd.SetJurisdictionTag("dest-bucket", "eu-west-1")
	if err := ctrl.Preflight(context.Background(), "eu-west-1"); err != nil {
		t.Fatalf("preflight after fix: %v", err)
	}
	verified := 0
	for _, c := range app.snapshot() {
		if c.eventType == "artifact.cross_region_replication_verified" {
			verified++
		}
	}
	if verified != 1 {
		t.Fatalf("want 1 verified event, got %d", verified)
	}
}

// runReplicationController returns promptly when its context is cancelled.
func TestRunReplicationControllerStops(t *testing.T) {
	fd := replication.NewFakeDriver()
	ctrl, err := replication.NewController(replication.ControllerConfig{
		Config: replication.Config{Enabled: false},
		Driver: fd,
	})
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runReplicationController(ctx, ctrl, func(string, ...any) {})
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("runReplicationController did not return after cancel")
	}
}

func violationEvents(calls []appendCall) int {
	n := 0
	for _, c := range calls {
		if c.eventType == "DataResidencyViolationAttempt" {
			n++
		}
	}
	return n
}
