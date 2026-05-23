// SPDX-License-Identifier: MIT

// Package loadctl implements the tier-12 control plane: the HTTP API
// surface, the run-state store, the runner registry, the scenario
// catalogue, and the WebSocket telemetry hub.
//
// The binary entry point is `cmd/lenny-loadctl/`. The Server type
// is the testable core; cmd just wires flag parsing and signal
// handling around it.
//
// TESTING.md §12.12 and §24.1.
package loadctl
