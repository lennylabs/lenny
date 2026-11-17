// SPDX-License-Identifier: MIT

// Package journeys runs the persona journey tier of lenny-test.
//
// A journey is a written scenario for one persona (an operator, a tenant
// administrator, an end user) kept as a YAML file under tests/journeys/.
// The harness creates a dedicated Kind cluster for each trial, starts a
// Claude Code agent in headless mode with the scenario's goal, the
// documentation pages it may read, and a shell limited to kubectl, helm,
// and lenny-ctl, and then evaluates the scenario's postconditions against
// the cluster and the gateway's API. The postconditions decide the
// verdict. The agent never does.
//
// The package holds the parts that can be tested without a cluster or a
// model: the scenario parser (scenario.go), the postcondition runner
// (postcondition.go, selectpath.go), the cost meter that reads the agent's
// stream-json output (cost.go), the pilot ledger (ledger.go), the verdict
// logic (verdict.go), the agent command line (agent.go), and the trial
// orchestration over injected cluster and agent implementations
// (runner.go). kind.go and gateway.go hold the implementations that touch
// a real cluster.
//
// The tier is contributor tooling for TESTING.md and change 0157. No
// specification section governs it, so nothing in this package carries a
// spec annotation.
package journeys
