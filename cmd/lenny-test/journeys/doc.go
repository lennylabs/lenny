// SPDX-License-Identifier: MIT

// Package journeys runs the persona journey tier of lenny-test.
//
// A journey is a written scenario for one persona (an operator, a tenant
// administrator, an end user) kept as a YAML file under tests/journeys/.
// The harness creates a dedicated Kind cluster for each trial, starts a
// Claude Code agent in headless mode in an isolated sandbox (sandbox.go:
// the image tests/journeys/Dockerfile defines, on an internal Docker
// network whose only way out is an egress proxy that tunnels to the
// Anthropic API and the trial cluster's API server) with the scenario's
// goal, a read-only copy of the documentation, a shell limited to kubectl,
// helm, and lenny-ctl, and only the credentials the scenario grants, and
// then
// evaluates the scenario's postconditions against the cluster and the
// gateway's API from the host. The postconditions decide the verdict. The
// agent never does.
//
// The parts that can be tested without a cluster or a model: the scenario
// parser (scenario.go), the postcondition runner (postcondition.go,
// selectpath.go), the cost meter that reads the agent's stream-json output
// (cost.go), the pilot ledger with its pending reservations and the
// host-wide lock (ledger.go), the verdict logic (verdict.go), the agent
// container's command line (agent.go), the credential grant (grant.go),
// the record redaction by known value and by form (redact.go,
// redact_forms.go) and the record writers that apply it (record.go), the
// token file (token.go), the agent image build (image.go), the
// configuration probe (probe.go), and the trial orchestration over
// injected cluster and agent implementations (runner.go). target.go
// defines the cluster target interface, kind.go its Kind implementation,
// and access.go the harness's own access to a cluster.
//
// The tier is contributor tooling for TESTING.md and change 0157. No
// specification section governs it, so nothing in this package carries a
// spec annotation.
package journeys
