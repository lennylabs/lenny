// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.3 credential-pool point-in-time-check
// race for `credentialPropagation: inherit` / `independent` delegation
// leases. Concurrent lenny/delegate_task calls that each pass the
// pre-claim credential availability check individually can still
// collectively exhaust the pool; the loser(s) must observe
// CREDENTIAL_POOL_EXHAUSTED and any pod claimed for a loser must be
// released back to the warm pool.
//
// The shared mcpClient, callTool, and delegateChild helpers live in
// elicitation_test.go (same package).

package tier4_integration_test

import (
	"testing"
)

// spec: §8.3 (spec/08_recursive-delegation.md) — "When the gateway
// processes a `delegate_task` call with `credentialPropagation:
// inherit` or `independent`, it performs the same pre-claim credential
// availability check ... For `inherit` mode, the gateway verifies that
// the parent's credential pool has at least one assignable slot
// (`active leases < maxConcurrentSessions` for at least one credential
// in the pool). ... This is a point-in-time check, not a reservation:
// concurrent `delegate_task` calls can each pass the check individually
// while collectively exhausting the pool. If the actual credential
// assignment fails after pod claim (due to this race), the gateway
// releases the pod back to the warm pool and returns
// `CREDENTIAL_POOL_EXHAUSTED`, consistent with the session-creation
// behavior in [Section 7.1]."
//
// diagnosis: a failure here (once unskipped) means concurrent
// delegate_task calls racing a single-slot credential pool did not
// leave exactly one winner and N-1 CREDENTIAL_POOL_EXHAUSTED losers,
// or a loser's claimed pod was not released back to the warm pool.
func TestDelegateTaskConcurrentCredentialPoolExhaustionRace(t *testing.T) {
	// `credentialPropagation` (inherit / independent / deny) has no
	// production implementation anywhere in the delegate_task path:
	// pkg/gateway/mcpfabric/delegation, pkg/delegation, and the MCP
	// tool schema in pkg/gateway/mcpfabric/mcptools carry no
	// `credentialPropagation` field, no per-hop credential-pool
	// assignment, and no CREDENTIAL_POOL_EXHAUSTED /
	// CREDENTIAL_PROVIDER_MISMATCH rejection at delegation time. There
	// is consequently no pre-claim availability check for this test to
	// race against, and no pod-release-on-assignment-failure path to
	// observe. This is tracked as a separate, larger implementation
	// gap (the §8.3 credentialPropagation feature as a whole); this
	// test is a faithful scaffold for the adversarial race case and
	// stays skipped until that feature exists.
	t.Skip("credentialPropagation is unimplemented in the delegate_task path (no pre-claim credential-pool check exists to race); see the open §8.3 credentialPropagation implementation gap in TEST-GAPS.md")

	// Intended shape once credentialPropagation is implemented:
	//
	//   1. Start the gateway (gateway.StartWith(t, "--dev-mode")) and
	//      seed, via the admin credential-pools REST surface, a
	//      credential pool with exactly one credential whose
	//      maxConcurrentSessions is 1 (one assignable slot), and wire
	//      it into the acme tenant's credentialPolicy.
	//   2. Create and start a parent session (mcpClient.runningSession)
	//      under that tenant; capture the gateway's warm-pool pod count
	//      as the baseline.
	//   3. Fire N (>= 2) concurrent lenny/delegate_task calls from the
	//      parent, each with `credentialPropagation: "inherit"` and a
	//      distinct target runtime, using goroutines + a WaitGroup so
	//      the calls race against the single-slot pool.
	//   4. Assert exactly one call succeeds (returns a childSessionId)
	//      and every other call's tool result is an error carrying
	//      CREDENTIAL_POOL_EXHAUSTED.
	//   5. Assert the gateway's warm-pool pod count returns to the
	//      Step 2 baseline: a pod claimed for a losing delegation that
	//      lost the assignment race after claim must be released back
	//      to the warm pool rather than leaked to a losing child.
}
