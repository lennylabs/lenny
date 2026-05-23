// SPDX-License-Identifier: MIT

// Package dispatch is the work-dispatch interface for tier-12
// lenny-loadrunner. A Dispatcher is the queue the runner reads jobs
// from. The package defines the interface and a portable in-memory
// implementation; cloud-specific implementations (SQS, Pub/Sub,
// Service Bus) live in sibling subpackages.
//
// TESTING.md §12.12 (Wave 5 tier-12 work-dispatch).
package dispatch
