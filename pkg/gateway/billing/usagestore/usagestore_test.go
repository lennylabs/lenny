// SPDX-License-Identifier: MIT

package usagestore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billing/usagestore"
)

func TestAggregateEmpty(t *testing.T) {
	s := usagestore.NewMemory()
	rep, err := s.Aggregate(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if rep.TotalSessions != 0 || len(rep.ByTenant) != 0 {
		t.Errorf("empty report: %+v", rep)
	}
}

func TestRecordAndAggregate(t *testing.T) {
	s := usagestore.NewMemory()
	_ = s.Record(context.Background(), usagestore.Record{
		TenantID: "acme", Runtime: "echo", Sessions: 1,
		Tokens: usagestore.Tokens{Input: 100, Output: 50}, PodMinutes: 2.5,
	})
	_ = s.Record(context.Background(), usagestore.Record{
		TenantID: "acme", Runtime: "claude", Sessions: 1,
		Tokens: usagestore.Tokens{Input: 200, Output: 80}, PodMinutes: 3.0,
	})
	rep, _ := s.Aggregate(context.Background(), "", nil)
	if rep.TotalSessions != 2 {
		t.Errorf("totalSessions: %d", rep.TotalSessions)
	}
	if rep.TotalTokens.Input != 300 || rep.TotalTokens.Output != 130 {
		t.Errorf("totalTokens: %+v", rep.TotalTokens)
	}
	if rep.TotalPodMinutes != 5.5 {
		t.Errorf("totalPodMinutes: %v", rep.TotalPodMinutes)
	}
	if len(rep.ByTenant) != 1 || rep.ByTenant[0].Sessions != 2 {
		t.Errorf("byTenant: %+v", rep.ByTenant)
	}
	if len(rep.ByRuntime) != 2 {
		t.Errorf("byRuntime should have echo + claude: %+v", rep.ByRuntime)
	}
}

func TestAggregateTenantFilter(t *testing.T) {
	s := usagestore.NewMemory()
	_ = s.Record(context.Background(), usagestore.Record{TenantID: "acme", Sessions: 5})
	_ = s.Record(context.Background(), usagestore.Record{TenantID: "globex", Sessions: 3})
	rep, _ := s.Aggregate(context.Background(), "acme", nil)
	if rep.TotalSessions != 5 {
		t.Errorf("filtered totalSessions: got %d, want 5", rep.TotalSessions)
	}
	if len(rep.ByTenant) != 1 || rep.ByTenant[0].TenantID != "acme" {
		t.Errorf("filtered byTenant: %+v", rep.ByTenant)
	}
}

func TestByTenantSorted(t *testing.T) {
	s := usagestore.NewMemory()
	for _, tn := range []string{"globex", "acme", "initech"} {
		_ = s.Record(context.Background(), usagestore.Record{TenantID: tn, Sessions: 1})
	}
	rep, _ := s.Aggregate(context.Background(), "", nil)
	if rep.ByTenant[0].TenantID != "acme" || rep.ByTenant[2].TenantID != "initech" {
		t.Errorf("byTenant not sorted: %+v", rep.ByTenant)
	}
}

// TestAggregateLabelFilter_spec_14_106 exercises the §14 line 106
// label-scoped usage report: a non-empty label filter narrows the rollup
// to the records whose denormalized labels contain every requested pair
// (AND-containment), and an empty filter aggregates everything. F-14.1.13.
func TestAggregateLabelFilter_spec_14_106(t *testing.T) {
	s := usagestore.NewMemory()
	_ = s.Record(context.Background(), usagestore.Record{
		TenantID: "acme", Runtime: "echo", Sessions: 1,
		Tokens: usagestore.Tokens{Input: 100},
		Labels: map[string]string{"team": "search", "env": "prod"},
	})
	_ = s.Record(context.Background(), usagestore.Record{
		TenantID: "acme", Runtime: "echo", Sessions: 1,
		Tokens: usagestore.Tokens{Input: 200},
		Labels: map[string]string{"team": "ads"},
	})
	_ = s.Record(context.Background(), usagestore.Record{
		TenantID: "acme", Runtime: "echo", Sessions: 1,
		Tokens: usagestore.Tokens{Input: 400},
		// no labels — must not match any non-empty filter
	})

	// AND-containment: team=search alone matches only the first record.
	rep, _ := s.Aggregate(context.Background(), "acme", map[string]string{"team": "search"})
	if rep.TotalSessions != 1 || rep.TotalTokens.Input != 100 {
		t.Errorf("team=search filter: got sessions=%d input=%d, want 1/100", rep.TotalSessions, rep.TotalTokens.Input)
	}

	// A pair that no record fully contains matches nothing.
	rep, _ = s.Aggregate(context.Background(), "acme", map[string]string{"team": "search", "env": "staging"})
	if rep.TotalSessions != 0 {
		t.Errorf("team=search,env=staging filter: got sessions=%d, want 0", rep.TotalSessions)
	}

	// Empty filter aggregates every record including the unlabelled one.
	rep, _ = s.Aggregate(context.Background(), "acme", nil)
	if rep.TotalSessions != 3 || rep.TotalTokens.Input != 700 {
		t.Errorf("no filter: got sessions=%d input=%d, want 3/700", rep.TotalSessions, rep.TotalTokens.Input)
	}
}
