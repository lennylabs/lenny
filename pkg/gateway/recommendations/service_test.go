// SPDX-License-Identifier: MIT

package recommendations_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/recommendations"
)

func TestGetRecommendationsEmptyWhenNoData(t *testing.T) {
	// §25.3: with empty sliding windows, no rule triggers.
	svc := recommendations.NewCapacityService(recommendations.NewWindowStore(7 * 24 * time.Hour))
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(resp.Recommendations) != 0 {
		t.Errorf("no-data recommendations: got %+v, want empty", resp.Recommendations)
	}
}

func TestGetRecommendationsTriggersCredentialPool(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.85)
	svc := recommendations.NewCapacityService(store)

	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	var found *recommendations.Recommendation
	for i := range resp.Recommendations {
		if resp.Recommendations[i].Rule == "CredentialPoolUndersized" {
			found = &resp.Recommendations[i]
		}
	}
	if found == nil {
		t.Fatalf("CredentialPoolUndersized must trigger at 0.85 utilisation: %+v", resp.Recommendations)
	}
	if found.Category != "credential_pool_sizing" || !found.DataAvailable || found.Value != 0.85 {
		t.Errorf("recommendation fields: %+v", found)
	}
	if found.Confidence <= 0 || found.Confidence > 1 {
		t.Errorf("confidence must be in (0,1]: %v", found.Confidence)
	}
}

func TestGetRecommendationsBelowThresholdDoesNotTrigger(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.40)
	svc := recommendations.NewCapacityService(store)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	for _, r := range resp.Recommendations {
		if r.Rule == "CredentialPoolUndersized" {
			t.Errorf("CredentialPoolUndersized must not trigger at 0.40 utilisation")
		}
	}
}

func TestGetRecommendationsCategoryFilter(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.85)
	store.Record("lenny_gateway_cpu_utilization_ratio", nil, 0.90)
	svc := recommendations.NewCapacityService(store)

	cat := "gateway_scaling"
	resp, err := svc.GetRecommendations(context.Background(), &cat)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(resp.Recommendations) != 1 || resp.Recommendations[0].Category != "gateway_scaling" {
		t.Errorf("category filter: got %+v, want only gateway_scaling", resp.Recommendations)
	}
}

func hasRule(resp *recommendations.RecommendationsResponse, rule string) bool {
	for _, r := range resp.Recommendations {
		if r.Rule == rule {
			return true
		}
	}
	return false
}

func TestGetRecommendationsResourceLimits(t *testing.T) {
	// §25.3 resource_limits: OOM kills in the 24h window trigger a
	// memory-limit recommendation.
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_pod_oom_killed_total", nil, 0)
	store.Record("lenny_pod_oom_killed_total", nil, 2)
	svc := recommendations.NewCapacityService(store)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	if !hasRule(resp, "ResourceLimitsMemoryPressure") {
		t.Errorf("ResourceLimitsMemoryPressure must trigger on OOM kills: %+v", resp.Recommendations)
	}
}

func TestGetRecommendationsRetentionAndQuota(t *testing.T) {
	// §25.3 retention_tuning and quota_adjustment.
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_storage_utilization_ratio", nil, 0.92)
	store.Record("lenny_quota_rejection_ratio", nil, 0.12)
	svc := recommendations.NewCapacityService(store)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	if !hasRule(resp, "RetentionTuningStoragePressure") {
		t.Errorf("RetentionTuningStoragePressure must trigger above 80%% storage: %+v", resp.Recommendations)
	}
	if !hasRule(resp, "QuotaAdjustmentRejections") {
		t.Errorf("QuotaAdjustmentRejections must trigger above 5%% rejections: %+v", resp.Recommendations)
	}
}

func TestGetRecommendationsRetentionBelowThreshold(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_storage_utilization_ratio", nil, 0.50)
	svc := recommendations.NewCapacityService(store)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	if hasRule(resp, "RetentionTuningStoragePressure") {
		t.Error("RetentionTuningStoragePressure must not trigger at 50 percent storage")
	}
}

func TestGetRecommendationsWarmPoolIncrease(t *testing.T) {
	// §25.3: WarmPoolUndersized triggers when the pool was exhausted
	// 3+ times across the 24h window.
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_warmpool_exhausted_total", nil, 10)
	store.Record("lenny_warmpool_exhausted_total", nil, 14) // +4 over the window
	svc := recommendations.NewCapacityService(store)

	resp, _ := svc.GetRecommendations(context.Background(), nil)
	triggered := false
	for _, r := range resp.Recommendations {
		if r.Rule == "WarmPoolUndersized" {
			triggered = true
			if r.Value < 3 {
				t.Errorf("WarmPoolUndersized value (the 24h increase) = %v, want >= 3", r.Value)
			}
		}
	}
	if !triggered {
		t.Errorf("WarmPoolUndersized must trigger on a 24h increase of 4: %+v", resp.Recommendations)
	}
}
