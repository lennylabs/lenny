// SPDX-License-Identifier: MIT

package usagestore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
)

func TestAggregateEmpty(t *testing.T) {
	s := usagestore.NewMemory()
	rep, err := s.Aggregate(context.Background(), "")
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
	rep, _ := s.Aggregate(context.Background(), "")
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
	rep, _ := s.Aggregate(context.Background(), "acme")
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
	rep, _ := s.Aggregate(context.Background(), "")
	if rep.ByTenant[0].TenantID != "acme" || rep.ByTenant[2].TenantID != "initech" {
		t.Errorf("byTenant not sorted: %+v", rep.ByTenant)
	}
}
