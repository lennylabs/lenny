// SPDX-License-Identifier: MIT

// Package lifecyclefence runs the §12.8 lifecycle operations (user erasure,
// tenant suspension and resume, and tenant-deletion Phases 1 and 2) against
// concurrent session creates on every creation path and concurrent terminal
// writes, over one posture's production session, tenant, user, erasure-job,
// and transcript stores, and checks the creation fence's and the drain's
// invariants. It is test-only code and adds no production seam.
//
// A World is one tenant with the users alice and bob. Its creation paths
// reproduce the store calls each gateway creation path makes, in the order
// the Creation-path rules of §12.8 fix: a plain fenced insert, a derive
// (reserve, copy the snapshot objects and the manifest row, insert through
// CreateReserved, and undo on any failure), a derive-failure audit row, a
// prompt_history replay through CreateWithTranscript, and a delegated child
// with a file export (reserve as a delegated child, export, insert through
// CreateReserved). The lifecycle operations are the production code: the
// admin router's suspend and resume handlers, the shared admin.DrainSessions,
// the erasure job store and Runner, and the tenantdeletion Reconciler.
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
