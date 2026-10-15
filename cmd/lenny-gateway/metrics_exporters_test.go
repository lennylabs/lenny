// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// unavailabilityRatio reads lenny_session_unavailability_ratio from m.
func unavailabilityRatio(t *testing.T, m *gatewaymetrics.Metrics) float64 {
	t.Helper()
	families, err := m.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == "lenny_session_unavailability_ratio" {
			return f.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatal("lenny_session_unavailability_ratio not registered")
	return 0
}

// availabilityRow is one seeded session of the availability-ratio test.
type availabilityRow struct {
	id    string
	state session.State
	pod   string
}

// TestSessionAvailabilityRatioExcludesSuspended_spec_16_5 pins the
// population of the §16.5 session-availability SLI: the sessions the §11.1
// concurrent-session count counts. Suspended sessions, with or without
// their pod, are in neither the numerator nor the denominator, and a
// gateway whose only sessions are suspended reports a ratio of 0.
//
// spec: §16.5 (Alerting Rules and SLOs), §11.1 (Admission and Fairness)
func TestSessionAvailabilityRatioExcludesSuspended_spec_16_5(t *testing.T) {
	ctx := context.Background()
	seed := func(t *testing.T, store *memstore.Store, id string, st session.State, pod string) {
		t.Helper()
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: "acme", State: st, RuntimeRef: "echo", PodAssignment: pod,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	cases := []struct {
		name string
		rows []availabilityRow
		want float64
	}{
		{
			name: "only suspended sessions",
			rows: []availabilityRow{
				{"s1", session.StateSuspended, "pod-a"},
				{"s2", session.StateSuspended, ""},
			},
			want: 0,
		},
		{
			name: "suspended sessions stay out of the denominator",
			rows: []availabilityRow{
				{"r1", session.StateRunning, "pod-a"},
				{"r2", session.StateRunning, "pod-b"},
				{"r3", session.StateInputRequired, "pod-c"},
				{"p1", session.StateResumePending, ""},
				{"s1", session.StateSuspended, "pod-d"},
				{"s2", session.StateSuspended, ""},
				{"s3", session.StateSuspended, ""},
				{"c1", session.StateCompleted, ""},
			},
			// One recovering session over four counted ones; a population
			// that counted the three suspended rows would read 1/7.
			want: 0.25,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			for _, r := range tc.rows {
				seed(t, store, r.id, r.state, r.pod)
			}
			m, err := gatewaymetrics.New()
			if err != nil {
				t.Fatalf("metrics: %v", err)
			}
			// A stale value proves the export wrote the gauge.
			m.SetSessionUnavailabilityRatio(0.9)
			exportSessionAvailabilityRatio(ctx, store, m)
			if got := unavailabilityRatio(t, m); got != tc.want {
				t.Errorf("lenny_session_unavailability_ratio = %v, want %v", got, tc.want)
			}
		})
	}
}
