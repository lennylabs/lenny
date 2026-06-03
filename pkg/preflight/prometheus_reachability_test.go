// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func okProber() PrometheusProber {
	return PrometheusProbeFunc(func(context.Context, string) error { return nil })
}

func failProber() PrometheusProber {
	return PrometheusProbeFunc(func(context.Context, string) error { return errors.New("connection refused") })
}

// spec: §25.4 lines 1462-1470 — the prometheus-reachability check is
// non-blocking (always Passed) and tier-specific. This matrix pins the
// INFO/WARN/silent outcomes across tier × configured × reachable ×
// acknowledged.
func TestPrometheusReachabilityCheck_Matrix_spec_25_4_25(t *testing.T) {
	cases := []struct {
		name        string
		cfg         PrometheusConfig
		prober      PrometheusProber
		wantReason  string // "" = silent ok, else substring
		wantWarning bool
	}{
		{
			name:   "tier2 reachable url passes silently",
			cfg:    PrometheusConfig{URL: "http://prom:9090", Tier: "tier2"},
			prober: okProber(),
		},
		{
			name:        "tier2 no url warns",
			cfg:         PrometheusConfig{Tier: "tier2"},
			wantReason:  "strongly recommended",
			wantWarning: true,
		},
		{
			name:       "tier2 no url acknowledged is silent",
			cfg:        PrometheusConfig{Tier: "tier2", AcknowledgeNoPrometheus: true},
			wantReason: "",
		},
		{
			name:        "tier3 url unreachable warns",
			cfg:         PrometheusConfig{URL: "http://prom:9090", Tier: "tier3"},
			prober:      failProber(),
			wantReason:  "unreachable",
			wantWarning: true,
		},
		{
			name:       "tier3 url unreachable but acknowledged is silent",
			cfg:        PrometheusConfig{URL: "http://prom:9090", Tier: "tier3", AcknowledgeNoPrometheus: true},
			prober:     failProber(),
			wantReason: "",
		},
		{
			name:       "tier1 no url emits INFO not WARN",
			cfg:        PrometheusConfig{Tier: "tier1"},
			wantReason: "degraded mode",
		},
		{
			name:       "tier1 acknowledgement irrelevant — still INFO",
			cfg:        PrometheusConfig{Tier: "tier1"},
			wantReason: "degraded mode",
		},
		{
			name:       "empty tier treated as tier1",
			cfg:        PrometheusConfig{},
			wantReason: "degraded mode",
		},
		{
			name:   "nil prober treats configured url as reachable",
			cfg:    PrometheusConfig{URL: "http://prom:9090", Tier: "tier2"},
			prober: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := PrometheusReachabilityCheck{Config: tc.cfg, Prober: tc.prober}.Decide(context.Background())
			if !d.Passed {
				t.Fatalf("check must be non-blocking (Passed=true), got Passed=false: %s", d.Reason)
			}
			if tc.wantReason == "" {
				if d.Reason != "" {
					t.Errorf("want silent ok, got reason %q", d.Reason)
				}
				return
			}
			if !strings.Contains(d.Reason, tc.wantReason) {
				t.Errorf("reason %q does not contain %q", d.Reason, tc.wantReason)
			}
			if tc.wantWarning && !strings.HasPrefix(d.Reason, "WARNING:") {
				t.Errorf("expected WARNING prefix, got %q", d.Reason)
			}
			if !tc.wantWarning && strings.HasPrefix(d.Reason, "WARNING:") {
				t.Errorf("did not expect WARNING prefix, got %q", d.Reason)
			}
		})
	}
}

// spec: §25.4 line 1470 — the message names the configured URL when one
// was set but unreachable, so the operator can tell "not configured" from
// "configured but down".
func TestPrometheusReachabilityCheck_NamesURLWhenUnreachable_spec_25_4_25(t *testing.T) {
	d := PrometheusReachabilityCheck{
		Config: PrometheusConfig{URL: "http://prom.example:9090", Tier: "tier2"},
		Prober: failProber(),
	}.Decide(context.Background())
	if !strings.Contains(d.Reason, "prom.example:9090") {
		t.Errorf("reason should name the configured URL, got %q", d.Reason)
	}
}
