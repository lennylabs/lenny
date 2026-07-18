// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
)

// spec: §10.4 line 386 — the readiness probe precedence rules. F-10.4.6.
func TestReadinessVerdict_spec_10_4_386(t *testing.T) {
	healthy := func(context.Context) health.Status { return health.StatusHealthy }
	unhealthy := func(context.Context) health.Status { return health.StatusUnhealthy }
	degraded := func(context.Context) health.Status { return health.StatusDegraded }

	cases := []struct {
		name           string
		draining       bool
		clockDrift     bool
		rebuildPending bool
		dualStoreDown  bool
		probe          hardDepProbe
		wantCode       int
		wantBody       string
	}{
		{
			name:     "all healthy is ready",
			probe:    healthy,
			wantCode: 200,
		},
		// spec: §4.9 — the startup deny-list rebuild gates readiness
		// fail-closed until its authoritative Reset commits, so a fresh
		// replica serves no proxy traffic against a retained revoked lease
		// with an incomplete deny list.
		{
			name:           "rebuild pending removes a healthy-backend replica",
			rebuildPending: true,
			probe:          healthy,
			wantCode:       503,
			wantBody:       "credential_deny_list_rebuild_pending\n",
		},
		{
			name:           "draining takes precedence over a pending rebuild",
			draining:       true,
			rebuildPending: true,
			probe:          healthy,
			wantCode:       503,
			wantBody:       "draining\n",
		},
		{
			name:           "clock drift takes precedence over a pending rebuild",
			clockDrift:     true,
			rebuildPending: true,
			probe:          healthy,
			wantCode:       503,
			wantBody:       "clock_drift_exceeded\n",
		},
		{
			// A pending rebuild fails closed even in dual-store degraded
			// mode: the replica must not serve proxy traffic with an
			// incomplete deny list, so rebuild-pending wins over the
			// dual-store 200 exemption.
			name:           "pending rebuild takes precedence over the dual-store exemption",
			rebuildPending: true,
			dualStoreDown:  true,
			probe:          unhealthy,
			wantCode:       503,
			wantBody:       "credential_deny_list_rebuild_pending\n",
		},
		{
			name:     "draining flips first even with healthy backends",
			draining: true,
			probe:    healthy,
			wantCode: 503,
			wantBody: "draining\n",
		},
		{
			name:       "clock drift removes a healthy-backend replica",
			clockDrift: true,
			probe:      healthy,
			wantCode:   503,
			wantBody:   "clock_drift_exceeded\n",
		},
		{
			name:       "draining takes precedence over clock drift",
			draining:   true,
			clockDrift: true,
			probe:      unhealthy,
			wantCode:   503,
			wantBody:   "draining\n",
		},
		{
			name:          "dual-store down keeps the replica ready to serve PLATFORM_DEGRADED",
			dualStoreDown: true,
			probe:         unhealthy,
			wantCode:      200,
		},
		{
			name:       "clock drift takes precedence over the dual-store exemption",
			clockDrift: true,
			// even though both stores are down, a drifted replica must
			// not stay in traffic — it cannot be trusted for exp checks.
			dualStoreDown: true,
			probe:         unhealthy,
			wantCode:      503,
			wantBody:      "clock_drift_exceeded\n",
		},
		{
			name:     "unhealthy hard dependency removes the replica",
			probe:    unhealthy,
			wantCode: 503,
			wantBody: "backend_unavailable\n",
		},
		{
			name:     "degraded hard dependency stays ready",
			probe:    degraded,
			wantCode: 200,
		},
		{
			name:     "nil probe (no hard backend wired) is ready",
			probe:    nil,
			wantCode: 200,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readinessVerdict(context.Background(), tc.draining, tc.clockDrift, tc.rebuildPending, tc.dualStoreDown, tc.probe)
			if got.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", got.Code, tc.wantCode)
			}
			if got.Body != tc.wantBody {
				t.Fatalf("body = %q, want %q", got.Body, tc.wantBody)
			}
		})
	}
}
