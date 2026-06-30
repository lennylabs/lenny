// SPDX-License-Identifier: MIT

package health

import "github.com/lennylabs/lenny/pkg/ops/conventions"

// ActionsForIssue resolves a §25.3 health-API issue code to the
// machine-executable remediation hint the response carries. It returns
// the singular suggestedAction form when one canonical response exists,
// or the ordered (descending confidence) suggestedActions form for the
// capacity/throttling issues that present ranked alternatives
// (WARM_POOL_EXHAUSTED, WARM_POOL_LOW, CREDENTIAL_POOL_EXHAUSTED,
// CIRCUIT_BREAKER_OPEN — see conventions.UsesRankedActions). target is
// the affected resource name (e.g. the pool name) substituted into the
// endpoint path; an empty target leaves a "{pool}" placeholder so the
// structure is still legible. An unknown issue yields (nil, nil).
//
// The runbook on each primary action is resolved through the §25.7
// Path B issueRunbooks table (RunbookForIssue) so the (issue, runbook)
// edge is not duplicated here; alternatives that point at a different
// runbook name it explicitly.
//
// spec: §25.3 lines 459-501 — the SuggestedAction struct and the
// singular vs. ranked contract; lines 484-487 enumerate the four ranked
// issues and require descending-confidence ordering.
func ActionsForIssue(issue, target string) (single *conventions.SuggestedAction, ranked []conventions.SuggestedAction) {
	if target == "" {
		target = "{pool}"
	}
	switch issue {
	case "WARM_POOL_EXHAUSTED":
		return nil, sortRanked([]conventions.SuggestedAction{
			{
				Action:     "SCALE_WARM_POOL",
				Endpoint:   "PUT /v1/admin/pools/" + target + "/warm-count",
				Reasoning:  "The warm pool is exhausted and session creation is rejecting requests. Raise minWarm to absorb current demand with headroom.",
				Runbook:    RunbookForIssue(issue),
				Confidence: 0.85,
				Risk:       "low",
			},
			{
				Action:     "INVESTIGATE_UPSTREAM",
				Endpoint:   "GET /v1/admin/diagnostics/pools/" + target,
				Reasoning:  "Pool exhaustion can be a symptom of excessive retries from failing sessions. Investigate the upstream cause before scaling as a stopgap.",
				Runbook:    "oom-root-cause",
				Confidence: 0.55,
				Risk:       "none",
			},
		})
	case "WARM_POOL_LOW":
		return nil, sortRanked([]conventions.SuggestedAction{
			{
				Action:     "SCALE_WARM_POOL",
				Endpoint:   "PUT /v1/admin/pools/" + target + "/warm-count",
				Reasoning:  "Warm pods are trending below minWarm. Raise minWarm to restore the buffer before the pool exhausts.",
				Runbook:    RunbookForIssue(issue),
				Confidence: 0.7,
				Risk:       "low",
			},
			{
				Action:     "INVESTIGATE_UPSTREAM",
				Endpoint:   "GET /v1/admin/diagnostics/pools/" + target,
				Reasoning:  "A draining warm buffer can reflect elevated claim churn. Confirm the demand is genuine before scaling.",
				Runbook:    "oom-root-cause",
				Confidence: 0.5,
				Risk:       "none",
			},
		})
	case "CREDENTIAL_POOL_EXHAUSTED":
		return nil, sortRanked([]conventions.SuggestedAction{
			{
				Action:     "ADD_CREDENTIALS",
				Endpoint:   "PUT /v1/admin/credential-pools/" + target,
				Reasoning:  "The credential pool is exhausted and sessions cannot lease a credential. Add credentials to bring utilization below 60%.",
				Runbook:    RunbookForIssue(issue),
				Confidence: 0.85,
				Risk:       "low",
			},
			{
				Action:     "INVESTIGATE_UPSTREAM",
				Endpoint:   "GET /v1/admin/diagnostics/credential-pools/" + target,
				Reasoning:  "Exhaustion can be driven by leased credentials that never release. Investigate leaked leases before provisioning more.",
				Runbook:    RunbookForIssue(issue),
				Confidence: 0.5,
				Risk:       "none",
			},
		})
	case "CIRCUIT_BREAKER_OPEN":
		return nil, sortRanked([]conventions.SuggestedAction{
			{
				Action:     "INVESTIGATE_UPSTREAM",
				Endpoint:   "GET /v1/admin/diagnostics/connectivity",
				Reasoning:  "A circuit breaker is open and the gateway is shedding load away from a failing dependency. Identify and recover the upstream before forcing traffic back.",
				Runbook:    RunbookForIssue(issue),
				Confidence: 0.8,
				Risk:       "low",
			},
			{
				Action:     "WAIT_FOR_RECOVERY",
				Reasoning:  "The breaker half-opens automatically and probes for recovery. If the upstream is already healing, no operator action is required.",
				Runbook:    RunbookForIssue(issue),
				Confidence: 0.4,
				Risk:       "none",
			},
		})
	case "POSTGRES_UNREACHABLE":
		return &conventions.SuggestedAction{
			Action:    "RUN_POSTGRES_FAILOVER",
			Reasoning: "Postgres is unreachable and the gateway rejects tenant-scoped writes until it recovers. Follow the runbook; there is no automated API-driven remediation.",
			Runbook:   RunbookForIssue(issue),
		}, nil
	case "REDIS_UNREACHABLE":
		return &conventions.SuggestedAction{
			Action:    "INVESTIGATE_REDIS",
			Reasoning: "Redis is unreachable; circuit-breaker and coordination state degrade to per-replica behaviour until it recovers.",
			Runbook:   RunbookForIssue(issue),
		}, nil
	case "MINIO_UNREACHABLE":
		return &conventions.SuggestedAction{
			Action:    "INVESTIGATE_OBJECT_STORE",
			Reasoning: "MinIO is unreachable; artifact reads and writes fail until it recovers.",
			Runbook:   RunbookForIssue(issue),
		}, nil
	case "CERT_EXPIRY_IMMINENT":
		return &conventions.SuggestedAction{
			Action:    "RENEW_CERTIFICATE",
			Reasoning: "A serving certificate is approaching expiry. Renew it through cert-manager before it lapses.",
			Runbook:   RunbookForIssue(issue),
		}, nil
	case "AUDIT_SIEM_DELIVERY_DEGRADED":
		return &conventions.SuggestedAction{
			Action:    "VERIFY_SIEM_DELIVERY",
			Reasoning: "SIEM delivery is failing. The audit hash chain remains durable in Postgres, but the independent SIEM copy is incomplete until delivery recovers.",
			Runbook:   RunbookForIssue(issue),
		}, nil
	default:
		return nil, nil
	}
}

// sortRanked orders ranked alternatives by descending confidence in
// place (§25.3 line 487) and returns the slice for chaining.
func sortRanked(actions []conventions.SuggestedAction) []conventions.SuggestedAction {
	conventions.SortByConfidence(actions)
	return actions
}
