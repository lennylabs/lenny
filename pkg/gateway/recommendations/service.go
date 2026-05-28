// SPDX-License-Identifier: MIT

package recommendations

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/recommendations/rules"
)

// Recommendation is one §25.3 capacity recommendation: an actionable
// suggestion synthesized when a rule's condition holds.
type Recommendation struct {
	// Rule is the recommendation rule that produced this entry.
	Rule string `json:"rule"`

	// Category is the rule's §25.3 recommendation category.
	Category string `json:"category"`

	// Summary is the rule's one-line human description.
	Summary string `json:"summary"`

	// Detail elaborates on the recommendation logic.
	Detail string `json:"detail,omitempty"`

	// Value is the rule's formula-derived metric value.
	Value float64 `json:"value,omitempty"`

	// Confidence is the rule's confidence in [0,1]. §25.3 reports 0.0
	// when the rule evaluated against an empty sliding window.
	Confidence float64 `json:"confidence"`

	// DataAvailable reports whether the rule's metrics were present.
	DataAvailable bool `json:"dataAvailable"`
}

// RecommendationsResponse is the GET /v1/admin/recommendations body.
type RecommendationsResponse struct {
	Recommendations []Recommendation `json:"recommendations"`

	// Degradation carries the §25.4 canonical envelope when the
	// response is derived from anything other than the operator's
	// Prometheus rules. The gateway's in-process evaluator runs the
	// compiled-in defaults, so single-replica reads stamp the envelope
	// with `thresholdSource: "compiled-in-defaults"` (§25.13 line 4848).
	// `lenny-ops` overrides the envelope when it serves from the
	// operator-customized Prometheus rule set.
	// spec: §25.4 line 215.
	Degradation *conventions.Degradation `json:"degradation,omitempty"`
}

// Evaluation is the outcome of running one rule's evaluator. A
// recommendation is emitted only when Triggered is true.
type Evaluation struct {
	// Triggered reports whether the rule's condition holds.
	Triggered bool

	// DataAvailable reports whether the rule's metrics were present in
	// the metric reader. §25.3: an empty sliding window yields false.
	DataAvailable bool

	// Value is the rule's formula-derived metric value.
	Value float64

	// Confidence is the rule's confidence in [0,1].
	Confidence float64
}

// Evaluator computes a rule's Evaluation against a MetricReader. The
// same evaluator runs in the gateway (in-process WindowStore reader)
// and lenny-ops (Prometheus-backed reader).
type Evaluator func(MetricReader) Evaluation

// CapacityService is the §25.3 capacity-recommendation service. It runs
// the recommendation-rule catalog against a MetricReader.
type CapacityService struct {
	reader     MetricReader
	evaluators map[string]Evaluator
}

// NewCapacityService returns a service that evaluates the §25.3
// recommendation-rule catalog against reader.
func NewCapacityService(reader MetricReader) *CapacityService {
	return &CapacityService{reader: reader, evaluators: defaultEvaluators()}
}

// GetRecommendations evaluates every catalog rule and returns the
// recommendations whose condition holds. A non-nil category narrows
// the result to that §25.3 category. The ctx parameter satisfies the
// §25.3 CapacityService interface; the in-process evaluation does not
// use it.
func (s *CapacityService) GetRecommendations(_ context.Context, category *string) (*RecommendationsResponse, error) {
	resp := &RecommendationsResponse{Recommendations: []Recommendation{}}
	for _, rule := range rules.Catalog() {
		if category != nil && string(rule.Category) != *category {
			continue
		}
		ev, ok := s.evaluators[rule.Name]
		if !ok {
			continue
		}
		e := ev(s.reader)
		if !e.Triggered {
			continue
		}
		resp.Recommendations = append(resp.Recommendations, Recommendation{
			Rule:          rule.Name,
			Category:      string(rule.Category),
			Summary:       rule.Summary,
			Detail:        rule.Description,
			Value:         e.Value,
			Confidence:    e.Confidence,
			DataAvailable: e.DataAvailable,
		})
	}
	// spec: §25.13 line 4848 — the gateway's in-process recommendation
	// evaluator always runs the compiled-in defaults. lenny-ops layers
	// the operator-customized source on top when it serves the response.
	resp.Degradation = &conventions.Degradation{
		Level:           conventions.DegradationHealthy,
		ThresholdSource: conventions.ThresholdSourceCompiledInDefaults,
	}
	return resp, nil
}

// defaultEvaluators pairs each shipped §25.3 catalog rule with the Go
// evaluator that reads its metrics and applies its threshold. A rule
// whose metric is absent from the reader does not trigger — §25.3's
// empty-window behaviour.
func defaultEvaluators() map[string]Evaluator {
	return map[string]Evaluator{
		"WarmPoolUndersized": func(m MetricReader) Evaluation {
			rate, ok := m.WindowedRate("lenny_warmpool_exhausted_total", nil, 24*time.Hour)
			if !ok {
				return Evaluation{}
			}
			increase := rate * (24 * time.Hour).Seconds()
			if increase < 3 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: increase, Confidence: 0.8}
		},
		"CredentialPoolUndersized": func(m MetricReader) Evaluation {
			util, ok := m.GaugeValue("lenny_credential_pool_utilization", nil)
			if !ok {
				return Evaluation{}
			}
			if util <= 0.70 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: util, Confidence: 0.8}
		},
		"GatewayScalingPressure": func(m MetricReader) Evaluation {
			cpu, ok := m.GaugeValue("lenny_gateway_cpu_utilization_ratio", nil)
			if !ok {
				return Evaluation{}
			}
			if cpu <= 0.70 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: cpu, Confidence: 0.7}
		},
		"ResourceLimitsMemoryPressure": func(m MetricReader) Evaluation {
			rate, ok := m.WindowedRate("lenny_pod_oom_killed_total", nil, 24*time.Hour)
			if !ok {
				return Evaluation{}
			}
			oomCount := rate * (24 * time.Hour).Seconds()
			if oomCount <= 0 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: oomCount, Confidence: 0.9}
		},
		"RetentionTuningStoragePressure": func(m MetricReader) Evaluation {
			util, ok := m.GaugeValue("lenny_storage_utilization_ratio", nil)
			if !ok {
				return Evaluation{}
			}
			if util <= 0.80 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: util, Confidence: 0.75}
		},
		"QuotaAdjustmentRejections": func(m MetricReader) Evaluation {
			ratio, ok := m.GaugeValue("lenny_quota_rejection_ratio", nil)
			if !ok {
				return Evaluation{}
			}
			if ratio <= 0.05 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: ratio, Confidence: 0.8}
		},
	}
}
