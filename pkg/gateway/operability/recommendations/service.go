// SPDX-License-Identifier: MIT

package recommendations

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/recommendations/rules"
)

// §25.3 capacity-recommendation errors. The admin handler maps these to
// the §25.3 error-code table (lines 622-625).
var (
	// ErrUnknownCategory is returned when a ?category= filter names a
	// value outside the §25.3 closed category enum. spec: §25.3 line 624
	// — UNKNOWN_RECOMMENDATION_CATEGORY (400).
	ErrUnknownCategory = errors.New("unknown recommendation category")

	// ErrRecommendationsUnavailable is returned when the operator set
	// disableOnPrometheusOutage and the backing reader reports itself
	// unavailable. spec: §25.3 line 625 — RECOMMENDATIONS_UNAVAILABLE
	// (503).
	ErrRecommendationsUnavailable = errors.New("recommendations unavailable")
)

// §25.3 warm-pool sizing constants. The formula on line 582 is
// minWarm = ceil(peak_claim_rate * (startup_p99 + failover_seconds) * 1.3).
const (
	// failoverSeconds is the worst-case controller failover window used
	// in the sizing formula. spec: §4.6.2 line 517 / §17.8 line 1014 —
	// "Use failover_seconds = 25 (leaseDuration 15s + renewDeadline 10s)".
	failoverSeconds = 25.0

	// podWarmBaselineSeconds is the §17.8 pod-warm creation-to-ready
	// baseline substituted for startup_p99 when the startup-latency
	// histogram has no samples yet. spec: §17.8 line 1014 —
	// pod_warmup_seconds = 10 for pod-warm pools.
	podWarmBaselineSeconds = 10.0

	// warmPoolSafetyFactor is the 1.3 headroom multiplier in the §25.3
	// warm-pool formula. spec: §25.3 line 582.
	warmPoolSafetyFactor = 1.3

	// credentialTargetUtilization is the utilization the credential
	// sizing recommendation drives the pool below. spec: §25.3 line 583
	// — "Add N credentials to bring utilization below 60%".
	credentialTargetUtilization = 0.60
)

// Priority is the §25.3 recommendation priority. It is the `priority`
// label on the lenny_recommendations_generated_total counter.
// spec: §25.3 line 618.
type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

// priorityForConfidence derives the §25.3 priority band from a rule's
// confidence: a high-confidence recommendation (>=0.8) is high priority,
// >=0.6 is medium, otherwise low. This keeps the priority deterministic
// and consistent with the confidence the evaluator already computes.
func priorityForConfidence(c float64) Priority {
	switch {
	case c >= 0.8:
		return PriorityHigh
	case c >= 0.6:
		return PriorityMedium
	default:
		return PriorityLow
	}
}

// RecommendationTarget is the §25.3 machine-executable recommendation:
// the admin action an agent applies plus the request body carrying the
// formula-derived target value (e.g. {"minWarm": 15} or
// {"addCredentials": 4}). It mirrors the §25.3 suggestedAction body the
// example responses use (lines 580-587, 3204).
type RecommendationTarget struct {
	// Action is the §25.3 action verb (SCALE_WARM_POOL, ADD_CREDENTIALS,
	// INCREASE_HPA_MAX, INCREASE_MEMORY_LIMIT, REDUCE_RETENTION_TTL,
	// INCREASE_TENANT_QUOTA).
	Action string `json:"action"`

	// Endpoint is the admin API endpoint that applies the action. A
	// pool-scoped recommendation carries a {pool} placeholder because
	// the gateway's in-process reader aggregates across pools; lenny-ops
	// resolves the concrete pool from the Prometheus label set.
	Endpoint string `json:"endpoint,omitempty"`

	// Body is the request body carrying the derived target value. It is
	// omitted when the rule's recommendation is qualitative (no
	// spec-defined numeric formula).
	Body map[string]any `json:"body,omitempty"`
}

// Recommendation is one §25.3 capacity recommendation: an actionable
// suggestion synthesized when a rule's condition holds.
type Recommendation struct {
	// Rule is the recommendation rule that produced this entry.
	Rule string `json:"rule"`

	// Category is the rule's §25.3 recommendation category.
	Category string `json:"category"`

	// Priority is the §25.3 priority band (high|medium|low), the
	// `priority` label on lenny_recommendations_generated_total.
	// spec: §25.3 line 618.
	Priority string `json:"priority"`

	// Summary is the rule's one-line human description.
	Summary string `json:"summary"`

	// Detail elaborates on the recommendation logic.
	Detail string `json:"detail,omitempty"`

	// Value is the rule's triggering metric value (e.g. the exhaustion
	// count or utilization ratio that tripped the condition). The
	// formula-derived target the agent acts on is in Target.
	Value float64 `json:"value,omitempty"`

	// Target is the §25.3 formula-derived, machine-executable
	// recommendation. Nil when the rule did not derive a target.
	Target *RecommendationTarget `json:"target,omitempty"`

	// Confidence is the rule's confidence in [0,1]. §25.3 reports 0.0
	// when the rule evaluated against an empty sliding window.
	Confidence float64 `json:"confidence"`

	// DataAvailable reports whether the rule's metrics were present.
	DataAvailable bool `json:"dataAvailable"`
}

// RecommendationsResponse is the GET /v1/admin/recommendations body.
type RecommendationsResponse struct {
	Recommendations []Recommendation `json:"recommendations"`

	// Degradation carries the §25.2 canonical envelope when the
	// response is derived from anything other than the operator's
	// Prometheus rules. The gateway's in-process evaluator runs the
	// compiled-in defaults, so single-replica reads stamp the envelope
	// with `thresholdSource: "compiled-in-defaults"` (§25.13 line 4848).
	// `lenny-ops` overrides the envelope when it serves from the
	// operator-customized Prometheus rule set.
	// spec: §25.2.
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

	// Value is the rule's triggering metric value.
	Value float64

	// Confidence is the rule's confidence in [0,1].
	Confidence float64

	// Target is the formula-derived recommendation target, when the
	// rule defines one. spec: §25.3 lines 580-587.
	Target *RecommendationTarget
}

// Evaluator computes a rule's Evaluation against a MetricReader over the
// resolved sliding window. The same evaluator runs in the gateway
// (in-process WindowStore reader) and lenny-ops (Prometheus-backed
// reader). The window argument honours the §25.3 per-rule
// windowOverrides operator knob (line 596).
type Evaluator func(m MetricReader, window time.Duration) Evaluation

// AvailabilityReporter is an optional MetricReader extension. A reader
// whose backing source can be unreachable (the Prometheus-backed reader
// in lenny-ops) reports its reachability here so the service can honour
// disableOnPrometheusOutage. The in-process WindowStore is always
// available and does not implement it. spec: §25.3 line 625.
type AvailabilityReporter interface {
	Available() bool
}

// Config tunes the §25.3 capacity-recommendation service per the
// operator knobs in §25.3 (lines 596, 604, 625).
type Config struct {
	// DisabledRules names rules the service skips entirely. spec: §25.3
	// line 604 — platform.recommendations.disabledRules.
	DisabledRules []string

	// WindowOverrides overrides a rule's sliding window, keyed by §25.3
	// category (e.g. {"warm_pool_sizing": 12h}). spec: §25.3 line 596 —
	// gateway.recommendations.windowOverrides.
	WindowOverrides map[string]time.Duration

	// DisableOnPrometheusOutage makes GetRecommendations return
	// ErrRecommendationsUnavailable instead of computing from a fallback
	// reader when the reader reports itself unavailable. spec: §25.3
	// line 625 — ops.recommendations.disableOnPrometheusOutage.
	DisableOnPrometheusOutage bool
}

// CapacityService is the §25.3 capacity-recommendation service. It runs
// the recommendation-rule catalog against a MetricReader.
type CapacityService struct {
	reader     MetricReader
	evaluators map[string]Evaluator
	config     Config
	disabled   map[string]bool
	metrics    *Metrics
}

// NewCapacityService returns a service that evaluates the §25.3
// recommendation-rule catalog against reader with default config (no
// disabled rules, default windows, fallback never disabled).
func NewCapacityService(reader MetricReader) *CapacityService {
	return NewCapacityServiceWithConfig(reader, Config{})
}

// NewCapacityServiceWithConfig returns a service configured with the
// §25.3 operator knobs (disabled rules, window overrides, outage
// opt-out).
func NewCapacityServiceWithConfig(reader MetricReader, cfg Config) *CapacityService {
	disabled := make(map[string]bool, len(cfg.DisabledRules))
	for _, r := range cfg.DisabledRules {
		disabled[r] = true
	}
	return &CapacityService{
		reader:     reader,
		evaluators: defaultEvaluators(),
		config:     cfg,
		disabled:   disabled,
	}
}

// WithMetrics wires the §25.3 recommendation metrics so every emitted
// recommendation increments lenny_recommendations_generated_total
// {category, priority}. Returns the receiver for chaining. spec: §25.3
// line 618.
func (s *CapacityService) WithMetrics(m *Metrics) *CapacityService {
	s.metrics = m
	return s
}

// GetRecommendations evaluates every catalog rule and returns the
// recommendations whose condition holds. A non-nil category narrows
// the result to that §25.3 category; an unrecognised category returns
// ErrUnknownCategory. When disableOnPrometheusOutage is set and the
// reader reports unavailable, it returns ErrRecommendationsUnavailable.
// The ctx parameter satisfies the §25.3 CapacityService interface; the
// in-process evaluation does not use it.
func (s *CapacityService) GetRecommendations(_ context.Context, category *string) (*RecommendationsResponse, error) {
	if category != nil && !rules.Category(*category).IsValid() {
		return nil, ErrUnknownCategory
	}
	// spec: §25.3 line 625 — fail closed with RECOMMENDATIONS_UNAVAILABLE
	// when the operator opted out of fallback and the source is down.
	if s.config.DisableOnPrometheusOutage {
		if a, ok := s.reader.(AvailabilityReporter); ok && !a.Available() {
			return nil, ErrRecommendationsUnavailable
		}
	}
	resp := &RecommendationsResponse{Recommendations: []Recommendation{}}
	// spec: §25.3 — aggregate whether any rule in the whole catalog
	// reported data, independent of the ?category= filter, so a filtered
	// request cannot read the store as wholesale-empty while another rule
	// holds data. An all-empty window set (for example shortly after a
	// gateway restart) surfaces below as a degraded §25.2 envelope so an
	// agent distinguishes a data-starved response from a healthy one.
	evaluated := 0
	anyData := false
	for _, rule := range rules.Catalog() {
		// spec: §25.3 line 604 — operator-disabled rules never run.
		if s.disabled[rule.Name] {
			continue
		}
		ev, ok := s.evaluators[rule.Name]
		if !ok {
			continue
		}
		e := ev(s.reader, s.windowFor(rule))
		evaluated++
		if e.DataAvailable {
			anyData = true
		}
		// spec: §25.3 — the ?category= filter narrows only the emitted
		// recommendations; data presence is aggregated over the whole
		// catalog above, so a filtered request for a starved rule cannot
		// report the store as wholesale-empty while another rule has data.
		if category != nil && string(rule.Category) != *category {
			continue
		}
		if !e.Triggered {
			continue
		}
		priority := string(priorityForConfidence(e.Confidence))
		resp.Recommendations = append(resp.Recommendations, Recommendation{
			Rule:          rule.Name,
			Category:      string(rule.Category),
			Priority:      priority,
			Summary:       rule.Summary,
			Detail:        rule.Description,
			Value:         e.Value,
			Target:        e.Target,
			Confidence:    e.Confidence,
			DataAvailable: e.DataAvailable,
		})
		// spec: §25.3 line 618 — count each generated recommendation by
		// category and priority.
		s.metrics.IncGenerated(string(rule.Category), priority)
	}
	// spec: §25.13 line 4848 — the gateway's in-process recommendation
	// evaluator always runs the compiled-in defaults. lenny-ops layers
	// the operator-customized source on top when it serves the response.
	resp.Degradation = &conventions.Degradation{
		Level:           conventions.DegradationHealthy,
		ThresholdSource: conventions.ThresholdSourceCompiledInDefaults,
	}
	// spec: §25.3 (data-starved degradation), §25.2 (canonical envelope)
	// — when at least one rule was evaluated but no rule in the whole
	// catalog reported data, every ring buffer is empty (the data-starved
	// case, for example shortly after a gateway restart). Report the
	// response-level envelope as degraded with a warning so an agent
	// distinguishes an empty recommendations array caused by starved
	// windows from one caused by a healthy platform with no issues. A
	// literal confidence: 0.0 is not set: Degradation.Confidence is
	// omitempty, so the level and the warning carry the signal.
	if evaluated > 0 && !anyData {
		resp.Degradation.Level = conventions.DegradationDegraded
		resp.Degradation.Warnings = []string{
			"recommendation metric windows are empty; no capacity rule had data to evaluate (for example shortly after a gateway restart, or when the source metrics are not present in this gateway's registry)",
		}
	}
	return resp, nil
}

// windowFor returns the sliding window the rule evaluates over, honouring
// a per-category override from gateway.recommendations.windowOverrides.
// spec: §25.3 line 596.
func (s *CapacityService) windowFor(r rules.Rule) time.Duration {
	if d, ok := s.config.WindowOverrides[string(r.Category)]; ok && d > 0 {
		return d
	}
	return r.Window
}

// computeMinWarm applies the §25.3 warm-pool sizing formula
// minWarm = ceil(peak_claim_rate * (startup_p99 + failover_seconds) * 1.3).
// peak_claim_rate is the windowed claim rate (the in-process reader's
// best estimate of the peak; lenny-ops substitutes the Prometheus
// max_over_time). startup_p99 falls back to the §17.8 pod-warm baseline
// when the startup-latency histogram has no samples. Returns 0 when the
// claim rate is unavailable. spec: §25.3 line 582.
func computeMinWarm(m MetricReader, window time.Duration) int {
	claimRate, ok := m.WindowedRate("lenny_warmpool_claims_total", nil, window)
	if !ok || claimRate <= 0 {
		return 0
	}
	startupP99, ok := m.HistogramQuantile("lenny_warmpool_pod_startup_duration_seconds", nil, 0.99)
	if !ok || startupP99 <= 0 {
		startupP99 = podWarmBaselineSeconds
	}
	return int(math.Ceil(claimRate * (startupP99 + failoverSeconds) * warmPoolSafetyFactor))
}

// computeAddCredentials applies the §25.3 credential-pool target: add
// enough credentials to bring utilization below 60%. With utilization
// u = in_use / total and a known total pool size, the target total is
// in_use / 0.60 = u * total / 0.60, so addCredentials =
// ceil(total * (u/0.60 - 1)). Returns 0 when the pool size is
// unavailable. spec: §25.3 line 583.
func computeAddCredentials(m MetricReader, util float64) int {
	total, ok := m.GaugeValue("lenny_credential_pool_assignable_count", nil)
	if !ok || total <= 0 {
		return 0
	}
	add := total*(util/credentialTargetUtilization) - total
	if add <= 0 {
		return 0
	}
	return int(math.Ceil(add))
}

// defaultEvaluators pairs each shipped §25.3 catalog rule with the Go
// evaluator that reads its metrics, applies its threshold, and (where
// the spec defines a formula) derives the actionable target. A rule
// whose metric is absent from the reader does not trigger — §25.3's
// empty-window behaviour.
func defaultEvaluators() map[string]Evaluator {
	return map[string]Evaluator{
		"WarmPoolUndersized": func(m MetricReader, window time.Duration) Evaluation {
			rate, ok := m.WindowedRate("lenny_warmpool_exhausted_total", nil, window)
			if !ok {
				return Evaluation{}
			}
			increase := rate * window.Seconds()
			if increase < 3 {
				return Evaluation{DataAvailable: true}
			}
			target := &RecommendationTarget{
				Action:   "SCALE_WARM_POOL",
				Endpoint: "PUT /v1/admin/pools/{pool}/warm-count",
			}
			if minWarm := computeMinWarm(m, window); minWarm > 0 {
				target.Body = map[string]any{"minWarm": minWarm}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: increase, Confidence: 0.8, Target: target}
		},
		"CredentialPoolUndersized": func(m MetricReader, _ time.Duration) Evaluation {
			util, ok := m.GaugeValue("lenny_credential_pool_utilization", nil)
			if !ok {
				return Evaluation{}
			}
			if util <= 0.70 {
				return Evaluation{DataAvailable: true}
			}
			target := &RecommendationTarget{
				Action:   "ADD_CREDENTIALS",
				Endpoint: "POST /v1/admin/credential-pools/{pool}/credentials",
			}
			if add := computeAddCredentials(m, util); add > 0 {
				target.Body = map[string]any{"addCredentials": add}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: util, Confidence: 0.8, Target: target}
		},
		"GatewayScalingPressure": func(m MetricReader, _ time.Duration) Evaluation {
			cpu, ok := m.GaugeValue("lenny_gateway_cpu_utilization_ratio", nil)
			if !ok {
				return Evaluation{}
			}
			if cpu <= 0.70 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: cpu, Confidence: 0.7, Target: &RecommendationTarget{
				Action: "INCREASE_HPA_MAX",
			}}
		},
		"ResourceLimitsMemoryPressure": func(m MetricReader, window time.Duration) Evaluation {
			rate, ok := m.WindowedRate("lenny_pod_oom_killed_total", nil, window)
			if !ok {
				return Evaluation{}
			}
			oomCount := rate * window.Seconds()
			if oomCount <= 0 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: oomCount, Confidence: 0.9, Target: &RecommendationTarget{
				Action:   "INCREASE_MEMORY_LIMIT",
				Endpoint: "PUT /v1/admin/pools/{pool}",
			}}
		},
		"RetentionTuningStoragePressure": func(m MetricReader, _ time.Duration) Evaluation {
			util, ok := m.GaugeValue("lenny_storage_utilization_ratio", nil)
			if !ok {
				return Evaluation{}
			}
			if util <= 0.80 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: util, Confidence: 0.75, Target: &RecommendationTarget{
				Action: "REDUCE_RETENTION_TTL",
			}}
		},
		"QuotaAdjustmentRejections": func(m MetricReader, _ time.Duration) Evaluation {
			ratio, ok := m.GaugeValue("lenny_quota_rejection_ratio", nil)
			if !ok {
				return Evaluation{}
			}
			if ratio <= 0.05 {
				return Evaluation{DataAvailable: true}
			}
			return Evaluation{Triggered: true, DataAvailable: true, Value: ratio, Confidence: 0.8, Target: &RecommendationTarget{
				Action:   "INCREASE_TENANT_QUOTA",
				Endpoint: "PUT /v1/admin/tenants/{tenant}/quota",
			}}
		},
	}
}
