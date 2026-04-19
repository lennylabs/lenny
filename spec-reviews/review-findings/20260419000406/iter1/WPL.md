# Warm Pool & Pod Lifecycle Management Review (WPL)

**Reviewed:** 20260419  
**Spec sections:** 06_warm-pod-model.md, 05_runtime-registry-and-pool-model.md, 04_system-components.md (4.6 controllers)  
**Scope:** Pool sizing formulas, SDK-warm tradeoffs, concurrent-mode pod eviction, experiment variants, pod draining

## Findings

No real issues found.

**Summary:** All pool sizing formulas are mathematically correct and consistently applied across sections 4.6.2 (base formula), 5.2 (execution mode adjustments), and 6.3 (latency budget). SDK-warm demotion logic is correctly specified with three escalation levels (warning at >60%, circuit-breaker at >=90%). Concurrent-workspace pod draining accounts correctly for `maxConcurrent` simultaneous slots in the `terminationGracePeriodSeconds` validation formula. Variant pool formulas correctly reference mode-adjusted divisors without over-specification. Cross-section references (4.6.1 failover, 4.6.2 base formula, 5.2 mode factors, 6.1 demotion thresholds, 6.2 draining behavior, 6.3 latency) are consistent. Lease election timing (25s worst-case) is used uniformly across all capacity planning calculations. The concurrent-workspace slot retry policy (1 retry, 2 total attempts) correctly differs from session mode (2 retries, 3 total) based on slot economics and retry-on-same-pod semantics.

