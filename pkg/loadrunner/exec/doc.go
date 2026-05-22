// SPDX-License-Identifier: MIT

// Package exec runs a tier-12 scenario against a target gateway and
// posts the result to the loadctl callback URL. It is the executable
// core of cmd/lenny-loadrunner.
//
// The default executor invokes the `k6` binary as a subprocess; tests
// override the Runner factory to substitute a deterministic in-process
// scenario.
//
// TESTING.md §12.12 / §24.1.
package exec
