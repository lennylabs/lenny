// SPDX-License-Identifier: MIT

// Package lifecyclefence runs the §12.8 lifecycle operations (user erasure,
// tenant suspension and resume, and tenant-deletion Phases 1 and 2) against
// concurrent session creates on every creation path and concurrent terminal
// writes, over one posture's production session, tenant, user, erasure-job,
// and transcript stores, and checks the creation fence's and the drain's
// invariants. It is test-only code and adds no production seam.
//
// A World is one tenant with the users alice and bob. Its creation paths
// follow the Creation-path rules of §12.8. A plain fenced insert and a
// prompt_history replay through CreateWithTranscript are single store
// calls. A derive and a failing derive run the production session server's
// derive handler, and a delegated child with a file export runs the
// production delegation service, so the reservation, the pre-insert writes,
// the insert through CreateReserved, the undo, and the derive-failure row
// are the gateway's own code. The World injects its object store, its
// checkpoint manifest store, and a session store that observes the calls
// keyed by the new session ID, and that records a violation when a path
// releases its reservation while anything it wrote remains under the ID.
// The lifecycle operations are the production code: the admin router's
// suspend and resume handlers, the shared admin.DrainSessions, the erasure
// job store and Runner, and the tenantdeletion Reconciler.
//
// The World records a violation, rather than failing the test, when an
// invariant breaks inside a concurrent goroutine; Check reports every
// recorded violation and then runs the end-of-scenario checks. The checks:
//
//   - When an erasure job leaves initiated, or a tenant-deletion job leaves
//     Phase 2, a primary listing of the scope returns no non-terminal
//     session.
//   - After a suspend answered 200 with no failed sessions, and before any
//     resume, a primary listing of the tenant returns no non-terminal
//     session.
//   - A refused create left no session row, object, manifest row,
//     transcript entry, or reservation under its session ID.
//   - A path releases its reservation only after deleting every object and
//     manifest row it wrote under the ID, and keeps the reservation when a
//     deletion fails.
//   - A session ID reserved for a user before that user's erasure completed
//     holds no object, no reservation, and no row once it has completed,
//     and a replay created for the user before then holds no transcript.
//   - Exactly one tenant.suspended audit row with already_suspended false
//     is written per suspension-marker transition.
//   - A user has at most one in-progress erasure job.
//
// Faults lets a test inject a store error into the drain's listing and into
// one forced termination, and Steps counts each erasure step's runs so a
// test can assert that a resumed job re-ran no completed step.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §12.8
// (Processing restriction during erasure (GDPR Article 18)), §12.8 (Tenant
// deletion lifecycle), §15.1 (REST API).
package lifecyclefence
