// SPDX-License-Identifier: MIT

package main

// allTiers returns the tier names in execution order. The order is the gate
// hierarchy from TESTING.md §3: lower-numbered tiers gate higher ones.
func allTiers() []string {
	return []string{
		"static",
		"unit",
		"component",
		"contract",
		"integration",
		"e2e_kind",
		"e2e_cloud",
		"load",
		"chaos",
		"security",
		"conformance",
		"docs",
	}
}

// tiersForGroup returns the tier plan implied by a named group.
//
// Phase 0 implementation: hard-coded mapping for the canonical groups. Phase 1
// reads tests/groups.yaml at runtime and resolves subsets from
// tests/groups.subsets.yaml.
func tiersForGroup(name string) []tierPlan {
	switch name {
	case "pr-fast":
		return []tierPlan{
			{name: "static", notes: "changed-only when implemented"},
			{name: "unit", notes: "changed-only when implemented"},
			{name: "component", notes: "changed-only when implemented"},
		}
	case "pr":
		return []tierPlan{
			{name: "static"},
			{name: "unit"},
			{name: "component"},
			{name: "contract"},
			{name: "integration"},
			{name: "e2e_kind", subsets: []string{"critical-path"}},
			{name: "security", subsets: []string{"critical"}},
			{name: "chaos", subsets: []string{"core"}},
			{name: "conformance", subsets: []string{"bundled-runtimes"}},
			{name: "docs"},
		}
	case "nightly":
		return tiersAll(map[string][]string{
			"e2e_cloud":   {"critical-path-rotated"},
			"load":        {"per-phase-baseline"},
			"conformance": {"reference-catalog"},
		})
	case "weekly":
		return tiersAll(map[string][]string{
			"e2e_cloud":   {"full-non-sandbox"},
			"load":        {"full-system"},
			"conformance": {"reference-catalog"},
		})
	case "pre-release":
		return tiersAll(map[string][]string{
			"e2e_cloud":   {"full"},
			"load":        {"full-system"},
			"chaos":       {"full"},
			"security":    {"full", "pentest"},
			"conformance": {"reference-catalog"},
		})
	case "phase-0-gate":
		return []tierPlan{
			{name: "static"},
			{name: "docs"},
		}
	case "phase-1-gate":
		return []tierPlan{
			{name: "static", notes: "schemas validate, contract tests compile, examples round-trip"},
			{name: "unit", notes: "state-machine packages: Session, TaskRecord, Sandbox, SandboxClaim"},
		}
	case "phase-1.5-gate":
		return []tierPlan{
			{name: "static", notes: "lint-schema.sh (R-01) + lint-queries.sh (R-02) + schema validation"},
			{name: "component", subsets: []string{"migrations"}, notes: "migration framework: round-trip, idempotency, rollback, regression"},
		}
	case "phase-2-gate":
		return []tierPlan{
			{name: "static", notes: "ImageResolver builds; all schemas validate"},
			{name: "unit", notes: "state machines + ImageResolver precedence and digest enforcement"},
			{name: "contract", subsets: []string{"adapter-jsonl"}, notes: "adapter JSONL contract round-trips against cmd/runtimes/echo (workspaceplan parser is a separate Phase 2 sub-track per spec §14)"},
			{name: "conformance", subsets: []string{"basic"}, notes: "lenny-compliance --level basic against echo"},
		}
	case "phase-2.5-gate":
		return []tierPlan{
			{name: "static", notes: "observability + alerting packages build; PromQL parses"},
			{name: "unit", notes: "correlation, logging, tracing, metrics, alerting/rules — every primitive validated"},
			{name: "component", subsets: []string{"observability"}, notes: "observability primitives wire together against the in-process collector"},
		}
	case "phase-2.8-gate":
		return []tierPlan{
			{name: "static", notes: "streaming-echo + lifecycle-events schema compile; examples validate"},
			{name: "unit", notes: "no new unit packages — streaming-echo behaviour is covered by the conformance battery"},
			{name: "conformance", subsets: []string{"full"}, notes: "lenny-compliance --level full against cmd/runtimes/streaming-echo"},
		}
	case "phase-3-gate":
		return []tierPlan{
			{name: "static", notes: "poolscaling/strategy + runtime/upgrade/state + mtls/spiffe + mtls/denylist build"},
			{name: "unit", notes: "§4.6.2 scaling formula, §10.5 upgrade state machine, §10.3 SPIFFE validation + cert deny list — every spec-mandated invariant tested"},
		}
	case "phase-3.5-gate":
		return []tierPlan{
			{name: "static", notes: "admission decision packages build"},
			{name: "unit", notes: "§4.6.1 sandboxclaim_guard, §17.2 label_immutability, §4.6.3 ownership matrix — every rule asserted"},
		}
	case "phase-4-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/api/v1/session + sessionstore/memstore + sessionserver + cmd/lenny-gateway build"},
			{name: "unit", notes: "§15.1 SessionState + precondition validator; memstore CRUD + tenant isolation"},
			{name: "contract", subsets: []string{"rest-sessions"}, notes: "§15.1 REST session lifecycle against the minimal gateway: create / get / list / transitions / precondition rejections / cross-tenant 404 / INVALID_STATE_TRANSITION envelope"},
		}
	case "phase-4.5-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/auth + pkg/auth/jwt + gateway/middleware/auth build"},
			{name: "unit", notes: "§10.2 TokenType + Role enums, tenant_id format, tenant-claim extraction; HMAC-SHA256 sign/verify round-trip; tamper and expiry rejection with §13.3 ±1s skew"},
			{name: "contract", subsets: []string{"rest-auth"}, notes: "§10.2 wire contract end-to-end: single- vs multi-tenant extraction, TENANT_CLAIM_MISSING/INVALID_FORMAT/NOT_FOUND envelopes, expired/tampered token rejection, dev-header transport"},
		}
	case "phase-5.5-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/credential builds"},
			{name: "unit", notes: "§4.9 Provider, AssignmentStrategy, LeaseSource, RotationTrigger enums; §4.7 ceiling-applicable rule; §4.9 proactive-renewal budget-exclusion rule; PoolConfig.Validate"},
		}
	case "phase-5.75-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/quota builds"},
			{name: "unit", notes: "§11.2 ResetPeriod enum, 80%/100% threshold checks, global→tenant→user hierarchical check, §11.2 fail-open ceiling formula, MAX-rule reconciliation"},
		}
	case "phase-9-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/delegation/cycle + pkg/delegation/lease build"},
			{name: "unit", notes: "§8.2 three-layer AND gate (enforce|warn|permissive); §8.2.bis maxDepth precedence chain; LeaseSlice budget validation; depth check"},
		}
	case "phase-8-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/checkpoint builds"},
			{name: "unit", notes: "§4.4 Level + Trigger + Outcome + ResumeMode enums; ConsistencyForLevel; RetryBudgetFor (eviction vs non-eviction); WorkspaceSizePreCheck; FreshnessCheck"},
		}
	case "phase-13-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/audit builds"},
			{name: "unit", notes: "§11.7 ChainIntegrity, ComplianceProfile, OCSFTranslationState enums; §16.4 RetentionPreset → days mapping and compatibility matrix"},
		}
	case "phase-7-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/{circuitbreaker, idempotency} + gateway/middleware/{idempotency, circuitbreaker} build"},
			{name: "unit", notes: "§11.5 idempotency primitives; §11.6 circuit breaker primitives; middleware in-memory stores"},
			{name: "contract", subsets: []string{"rest-idempotency", "rest-circuitbreaker"}, notes: "§11.5 + §11.6 wire contracts end-to-end against the minimal gateway"},
		}
	case "phase-16-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/experiment builds"},
			{name: "unit", notes: "§10.7 Status, TargetingMode, Sticky, Propagation enums; Definition validation including reserved control id + Σ weights < 1; HMAC-SHA256 bucketing determinism + distribution + ordering + experiment-id independence"},
		}
	case "phase-14-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/podsecurity builds"},
			{name: "unit", notes: "§13.1 host-sharing prohibition (shareProcessNamespace/hostPID/hostNetwork/hostIPC); SecurityContext invariants (runAsNonRoot, allowPrivilegeEscalation, privileged, readOnlyRootFilesystem, drop ALL, no add); fsGroup = lenny-cred-readers GID"},
		}
	case "phase-15-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/environment builds"},
			{name: "unit", notes: "§10.6 Role enum + AtLeast; LabelSelector matchLabels + matchExpressions (In/NotIn/Exists/DoesNotExist); types filter; include/exclude overrides (exclude beats include); Filter helper"},
		}
	case "phase-10-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/elicitation builds"},
			{name: "unit", notes: "§9.2 EnforcementMode (off|detect-only|enforce) strict ordering + platform-floor clamp; DepthPolicy (allow_all|suppress_at_depth|block_all); InitiatorType (connector|agent); canonicalised SHA-256 content digest + tamper detection; Provenance validation"},
		}
	case "phase-12a-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/tokenexchange + pkg/tokenservice + cmd/lenny-token-service build"},
			{name: "unit", notes: "§13.3 RFC 8693 invariants: scope narrowing, tenant match, caller_type cannot elevate, audience cannot broaden, typ rules, depth = parent+1, exp = min(requested, subject, actor, cap), ±1s skew, Unix-seconds exp truncation"},
			{name: "contract", subsets: []string{"oauth-token"}, notes: "§13.3 POST /v1/oauth/token wire contract: rotation, scope broaden rejection, cross-tenant rejection, child minting, expired subject, missing caller, unsupported grant, per-dialect cap"},
		}
	case "phase-13.4-gate":
		return []tierPlan{
			{name: "static", notes: "pkg/upload builds"},
			{name: "unit", notes: "§13.4 archive validators: per-entry kind / path / size / depth / length checks; aggregate decompressed size + ratio + entry count; symlink default-deny with workspace-root canonicalisation"},
		}
	}

	// Phase 0 stub: every other phase-<N>-gate is recognized so the CLI does
	// not error, but resolution is deferred to Phase 1.
	if isPhaseGate(name) {
		return []tierPlan{
			{name: "static", notes: "phase-0-stub: full phase-gate resolution pending"},
		}
	}
	return nil
}

func isPhaseGate(name string) bool {
	const prefix = "phase-"
	const suffix = "-gate"
	if len(name) < len(prefix)+len(suffix) {
		return false
	}
	return name[:len(prefix)] == prefix && name[len(name)-len(suffix):] == suffix
}

func tiersAll(subsets map[string][]string) []tierPlan {
	plans := []tierPlan{}
	for _, t := range allTiers() {
		p := tierPlan{name: t}
		if s, ok := subsets[t]; ok {
			p.subsets = s
		}
		plans = append(plans, p)
	}
	return plans
}
