// SPDX-License-Identifier: MIT

// Package dispatch is the work-dispatch interface for the load_cloud tier
// lenny-loadrunner. A Dispatcher is the queue the runner reads jobs
// from. The package defines the interface and a portable in-memory
// implementation; cloud-specific implementations (SQS, Pub/Sub,
// Service Bus) live in sibling subpackages.
//
// Wave 5 load_cloud-tier work-dispatch.
package dispatch
