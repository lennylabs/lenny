// SPDX-License-Identifier: MIT

// Package health implements the §25.3 Platform Health API. It
// aggregates the health of the gateway's subsystems into the
// agent-operability surface an AI-DevOps agent reads to decide
// whether the platform needs remediation.
//
// The package is transport-agnostic: a Checker reports a Component
// status, the Aggregator rolls Components into an overall verdict,
// and the §15.1 HTTP handler in handler.go serves the result.
//
// spec: GWOPS
package health
