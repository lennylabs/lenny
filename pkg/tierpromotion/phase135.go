// SPDX-License-Identifier: MIT

package tierpromotion

import (
	"fmt"
	"math"
	"strings"
)

// Autoscaling provider literals match the chart's `autoscaling.provider`
// value (`hpa` or `keda`). The chart fails the render on any other
// value (§10.1 SCL-024), so the gate compares against this closed set.
const (
	providerHPA  = "hpa"
	providerKEDA = "keda"
)

// burstArrivalRate names the §17.8.2 line 971-975 / 989-991 burst
// arrival rate the SCL-036 formula expects per target tier (sessions
// per second).
var burstArrivalRate = map[Tier]int{
	Tier1: 5,
	Tier2: 30,
	Tier3: 200,
}

// pipelineLag names the §17.8.2 worst-case HPA pipeline-lag seconds
// per autoscaling provider. The KEDA path holds the lag near 20s with
// `pollingInterval: 10s`; the Prometheus Adapter path runs at ~60s.
// spec: §17.8.2 lines 960, 965, 981.
var pipelineLag = map[string]int{
	providerKEDA: 20,
	providerHPA:  60,
}

// CheckAutoscalingProvider verifies the deployed autoscaling provider
// is KEDA when the promotion target is Tier 3. §17.8.3 line 1285 makes
// "KEDA is not deployed" a NO-GO criterion for Tier 3, mirroring
// §17.8.2 Path A line 963 ("mandatory for Tier 3, optional for Tier
// 1/2"). The check is SKIPPED for promotions whose target is Tier 1 or
// Tier 2 (KEDA is optional there).
// spec: §17.8.3 line 1285, §17.8.2 line 963.
func CheckAutoscalingProvider(in Inputs) CheckResult {
	const name = "autoscaling-provider"
	if in.To != Tier3 {
		return CheckResult{
			Name:   name,
			Status: StatusSkip,
			Detail: fmt.Sprintf(
				"KEDA is optional for %s (mandatory only at Tier 3 per §17.8.2 line 963)",
				in.To,
			),
		}
	}
	provider := strings.ToLower(strings.TrimSpace(in.AutoscalingProvider))
	if provider == "" {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: "autoscaling.provider is unset in the rendered chart values; Tier 3 requires " +
				"\"keda\" (§17.8.3 line 1285 / §17.8.2 line 963)",
		}
	}
	if provider != providerKEDA {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"autoscaling.provider=%q is incompatible with Tier 3; KEDA is mandatory "+
					"(§17.8.3 line 1285 / §17.8.2 line 963). The Prometheus Adapter path's 60s "+
					"pipeline lag would require minReplicas=30 (equal to maxReplicas, no headroom)",
				in.AutoscalingProvider,
			),
		}
	}
	return CheckResult{
		Name:   name,
		Status: StatusPass,
		Detail: "autoscaling.provider=keda satisfies the §17.8.3 line 1285 Tier 3 NO-GO criterion",
	}
}

// CheckBurstAbsorption verifies the deployed `autoscaling.minReplicas`
// satisfies the §17.8.2 SCL-036 burst-absorption floor for the active
// autoscaling provider:
//
//	minReplicas >= ceil(burst_arrival_rate * pipeline_lag / maxSessionsPerReplica)
//
// The check uses the §17.8.2 burst-arrival rate per tier (5/30/200)
// and the pipeline-lag-seconds per provider (KEDA 20s, HPA 60s). The
// Tier 3 KEDA path is a documented carve-out: §17.8.2 line 975 / 977
// allows `minReplicas: 5` because the aggressive scale-up policy
// (100%/15s or 8 pods/15s) absorbs the remaining burst within one 15s
// period; the carve-out applies only when `provider: keda` and the
// computed raw floor is 10. The check is SKIPPED when the autoscaling
// provider or required SCL-036 inputs are absent.
// spec: §17.8.2 lines 950, 963-977.
func CheckBurstAbsorption(in Inputs) CheckResult {
	const name = "burst-absorption"
	provider := strings.ToLower(strings.TrimSpace(in.AutoscalingProvider))
	if provider == "" {
		return CheckResult{
			Name:   name,
			Status: StatusSkip,
			Detail: "autoscaling provider unknown; SCL-036 cannot be evaluated",
		}
	}
	if in.MaxSessionsPerReplica <= 0 {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"gateway.maxSessionsPerReplica=%d is not a positive integer; SCL-036 requires it",
				in.MaxSessionsPerReplica,
			),
		}
	}
	burst, ok := burstArrivalRate[in.To]
	if !ok {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf("no §17.8.2 burst-arrival rate defined for target tier %s", in.To),
		}
	}
	lag, ok := pipelineLag[provider]
	if !ok {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"autoscaling.provider=%q is not one of \"hpa\" or \"keda\"; SCL-036 cannot pick a "+
					"pipeline-lag (§10.1 SCL-024)",
				in.AutoscalingProvider,
			),
		}
	}
	rawFloor := int(math.Ceil(float64(burst*lag) / float64(in.MaxSessionsPerReplica)))
	requiredFloor := rawFloor
	carveOutApplied := false
	if in.To == Tier3 && provider == providerKEDA && rawFloor == 10 {
		// §17.8.2 line 975-977: the Tier 3 KEDA path allows minReplicas=5
		// because the aggressive 100%/15s or 8 pods/15s scale-up policy
		// absorbs the remaining 2,000-attempt burst within one 15s period.
		requiredFloor = 5
		carveOutApplied = true
	}
	if in.MinReplicas < requiredFloor {
		formula := fmt.Sprintf("ceil(%d * %d / %d) = %d", burst, lag, in.MaxSessionsPerReplica, rawFloor)
		carveOutNote := ""
		if carveOutApplied {
			carveOutNote = " (with the §17.8.2 line 975 Tier 3 KEDA carve-out: 5)"
		}
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: fmt.Sprintf(
				"autoscaling.minReplicas=%d is below the SCL-036 floor of %d%s; formula %s "+
					"for provider=%s at %s (burst=%d/s, pipeline_lag=%ds, sessions_per_replica=%d)",
				in.MinReplicas, requiredFloor, carveOutNote, formula, provider, in.To,
				burst, lag, in.MaxSessionsPerReplica,
			),
		}
	}
	detail := fmt.Sprintf(
		"autoscaling.minReplicas=%d meets the SCL-036 floor of %d for provider=%s at %s "+
			"(burst=%d/s, pipeline_lag=%ds, sessions_per_replica=%d)",
		in.MinReplicas, requiredFloor, provider, in.To, burst, lag, in.MaxSessionsPerReplica,
	)
	if carveOutApplied {
		detail += " (§17.8.2 line 975 Tier 3 KEDA carve-out applied)"
	}
	return CheckResult{Name: name, Status: StatusPass, Detail: detail}
}

// CheckPhase135Attestations verifies the operator has attested the
// three Phase 13.5 benchmark results §17.8.3 Step 1 enumerates: the
// LLM Proxy extraction-ratio benchmark (§17.8.3 line 1263 / 1282), the
// gateway GC pressure benchmark (§17.8.3 line 1264 / 1283), and the
// `maxSessionsPerReplica` calibration (§17.8.3 line 1265 / 1284). The
// `lenny-tier-promote` binary cannot run the benchmark harness itself,
// but it requires the operator to attest each result before allowing a
// Tier 3 promotion to proceed. The check is SKIPPED when the target is
// Tier 1 or Tier 2: §17.8.3 is the Tier 2 → Tier 3 gate.
// spec: §17.8.3 Step 1 (lines 1257-1267), Step 2 NO-GO (lines 1281-1284).
func CheckPhase135Attestations(in Inputs) CheckResult {
	const name = "phase-13.5-attestations"
	if in.To != Tier3 {
		return CheckResult{
			Name:   name,
			Status: StatusSkip,
			Detail: fmt.Sprintf(
				"§17.8.3 Phase 13.5 attestations gate the %s → Tier 3 promotion; not required for %s",
				in.From, in.To,
			),
		}
	}
	var missing []string
	if !in.LLMProxyExtractionAttested {
		missing = append(missing,
			"LLM Proxy extraction ratio (§17.8.3 line 1263 / 1282)")
	}
	if !in.GatewayGCPauseAttested {
		missing = append(missing,
			"gateway GC pause P99 below 50 ms (§17.8.3 line 1264 / 1283)")
	}
	if !in.MaxSessionsPerReplicaCalibrated {
		missing = append(missing,
			"maxSessionsPerReplica empirically calibrated (§17.8.3 line 1265 / 1284)")
	}
	if len(missing) > 0 {
		return CheckResult{
			Name:   name,
			Status: StatusFail,
			Detail: "Phase 13.5 benchmarks not attested: " + strings.Join(missing, "; "),
		}
	}
	return CheckResult{
		Name:   name,
		Status: StatusPass,
		Detail: "operator attested all three §17.8.3 Phase 13.5 benchmarks (LLM Proxy ratio, " +
			"GC pause P99, maxSessionsPerReplica calibration)",
	}
}
