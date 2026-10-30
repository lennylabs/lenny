// SPDX-License-Identifier: MIT

// Package lifecycleharness runs two or more session servers over one session
// store, one coordination lease store, one credential lease store, one Redis,
// and one Kubernetes API server, under one stepped clock, with an in-process
// adapter and a recording runtime behind every pod. It is test-only code and
// adds no production seam.
//
// The harness records every call the servers make to the shared session
// store as one ordered trace whose entries name the call and its call site
// (the gateway function that issued it), and can inject a fault at any trace
// index: a store error, a context cancel, a crash of the calling replica, or
// a peer takeover. A crash ends the replica's server-lifetime context, drops
// its in-memory records, and lets its coordination lease lapse.
//
// The invariant checker (Checker) steps the clock, ticks each surviving
// replica's coordination Sweeper and watchdog, waits for the work those ticks
// started to settle, repeats until the horizon has passed and two passes
// change nothing, and then asserts the lifecycle invariants for every
// session the scenario created: pods and claims, credential leases,
// coordination, per-replica records, queues, and client-visible effects.
//
// With Options.MaxConcurrentSessions above 1 the warm pool is a
// concurrent-workspace pool: each replica's binder holds its own slot
// counter on the shared Redis, and the checker also asserts that every
// pod's slot set holds exactly the non-terminal sessions naming the pod
// plus its leaked: members, that no pod with an empty set keeps a bound
// claim, and that no member was removed twice in one reservation.
//
// spec: §7.2 (Interactive Session Model), §7.3 (Resume driver), §10.1.1
// (Stateless Replicas and Per-Session Coordination), §10.1.5 (Stale Replica
// Behavior), §5.2 (Slot assignment atomicity)
package lifecycleharness
