// SPDX-License-Identifier: MIT

// Package lifecyclefence runs the §12.8 lifecycle operations (user erasure,
// tenant suspension and resume, tenant-deletion Phases 1 through 4, and the
// §11.4 full_revoke) against
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
// suspend, resume, and user invalidation handlers, the shared
// admin.DrainSessions, the erasure job store and Runner, and the
// tenantdeletion Reconciler. The Reconciler's Phase 3 revoker and Phase 4
// eraser list are harness seams that follow cmd/lenny-gateway's
// tenantCredentialRevoker and the Phase 4 order of the stores a World holds,
// because package main cannot be imported. Each seeded live session holds a
// credential lease in the World's lease pool, and each user holds TokenStore
// rows. A twin World is a second tenant sharing the blob store, the
// manifests, the lease pool, and the TokenStore.
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
//   - When a tenant-deletion job leaves Phase 3, the tenant row is deleting
//     and not registered, no session row of the tenant holds a lease, and
//     the tenant holds no TokenStore row.
//   - When it leaves Phase 4, and at the end of the scenario, the tenant
//     holds no session row, reservation, user row, transcript entry,
//     object, manifest, lease, or TokenStore row.
//   - A full_revoke that answers 200 leaves no live session of the user
//     that predates the call, no lease on a session row of the user, and no
//     failed cancel write.
//   - A twin tenant is unchanged.
//
// Faults lets a test inject a store error into the drain's listing, into
// one forced termination, and into each tenant-deletion Phase 3 step and
// Phase 4 entry, and run a hook after each of those steps. Steps and
// DeletionSteps count each step's runs so a test can assert what a resumed
// job re-ran.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §12.8
// (Processing restriction during erasure (GDPR Article 18)), §12.8 (Tenant
// deletion lifecycle), §15.1 (REST API), §11.4 (User Invalidation), §10.2
// (Authentication).
package lifecyclefence
