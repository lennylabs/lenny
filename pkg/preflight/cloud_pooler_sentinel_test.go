// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// spec: §17.6 line 488; §17.9.7 line 1541; §12.3 line 56 — when
// postgres.connectionPooler is external the cloud-managed proxy cannot
// run the connect_query __unset__ sentinel, so the lenny_tenant_guard
// per-transaction trigger is the load-bearing RLS isolation defense; the
// preflight check fails the install when it is absent.
func TestCloudPoolerSentinelCheck_spec_17_6_488(t *testing.T) {
	cases := []struct {
		name       string
		pooler     string
		gaps       []string
		probeErr   error
		nilProber  bool
		wantPass   bool
		wantReason string // substring expected in the reason
	}{
		{name: "pgbouncer skips", pooler: "pgbouncer", nilProber: true, wantPass: true, wantReason: "not external"},
		{name: "empty skips", pooler: "", nilProber: true, wantPass: true, wantReason: "not external"},
		{name: "external no prober defers to runtime defense", pooler: "external", nilProber: true, wantPass: true, wantReason: "LENNY_POOLER_MODE=external startup defense"},
		{name: "external no gaps passes", pooler: "external", gaps: nil, wantPass: true, wantReason: "present on all tenant-scoped tables"},
		{name: "external case-insensitive no gaps passes", pooler: "External", gaps: nil, wantPass: true, wantReason: "present on all tenant-scoped tables"},
		{name: "external with gaps fails closed", pooler: "external", gaps: []string{"sessions", "usage_events"}, wantPass: false, wantReason: "lenny_tenant_guard"},
		{name: "external with gaps lists tables", pooler: "external", gaps: []string{"sessions"}, wantPass: false, wantReason: "sessions"},
		{name: "external unreachable fails closed", pooler: "external", probeErr: errors.New("dial tcp: connection refused"), wantPass: false, wantReason: "POSTGRES_UNREACHABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := CloudPoolerSentinelCheck{ConnectionPooler: tc.pooler}
			if !tc.nilProber {
				check.Prober = PoolerSentinelProbeFunc(func(context.Context) ([]string, error) {
					return tc.gaps, tc.probeErr
				})
			}
			got := check.Decide(context.Background())
			if got.Passed != tc.wantPass {
				t.Fatalf("Passed = %v, want %v (reason: %q)", got.Passed, tc.wantPass, got.Reason)
			}
			if tc.wantReason != "" && !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want substring %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// The fail message reproduces the §17.6 line 488 error-column text
// verbatim so operators can match it against the documented remediation.
func TestCloudPoolerSentinelFailMessage_spec_17_6_488(t *testing.T) {
	check := CloudPoolerSentinelCheck{
		ConnectionPooler: "external",
		Prober: PoolerSentinelProbeFunc(func(context.Context) ([]string, error) {
			return []string{"sessions"}, nil
		}),
	}
	got := check.Decide(context.Background())
	if got.Passed {
		t.Fatal("expected a fail-closed decision when a tenant-scoped table lacks the trigger")
	}
	for _, frag := range []string{
		"Cloud-managed pooler detected",
		"lenny_tenant_guard",
		"connect_query",
		"Section 12.3",
	} {
		if !strings.Contains(got.Reason, frag) {
			t.Errorf("fail message missing %q; got %q", frag, got.Reason)
		}
	}
}
