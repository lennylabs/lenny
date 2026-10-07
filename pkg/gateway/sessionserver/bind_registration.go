// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// registerBinding publishes a successful startOnPod / resumeOnPod
// result so the message and teardown paths can reach the pod, and
// persists the bound pod's SandboxName to sessions.pod_assignment so
// a fresh gateway replica can recover the binding after a coordinator
// handoff. The persist is best-effort: a failure leaves the in-memory
// Registry authoritative and the next coordination sweep re-publishes
// the assignment. spec: §4.2 — "Pod-to-session binding".
func (s *Server) registerBinding(ctx context.Context, result *podsession.BindResult) error {
	if result == nil {
		return nil
	}
	// spec: §4.6.1 (coordinating replica holds the lease), §10.1
	// (per-session coordination lease) — acquire the coordination lease on
	// this replica before the binding is published, so the replica that
	// holds the binding is the lease holder from bind time and a peer
	// coordination sweep observes the held lease and skips the session on
	// ErrHeld. The acquire is idempotent for the same holder, so a path
	// whose acquire was already hoisted ahead of an earlier running-commit
	// self-renews here harmlessly. On a live foreign holder (ErrHeld,
	// reachable on the resume paths) publish and persist nothing and return
	// the error so the caller fails the bind closed rather than double-bind
	// a session another replica still coordinates.
	if err := s.acquireCoordinationLease(ctx, result.TenantID, result.SessionID); err != nil {
		return err
	}
	s.publishBinding(ctx, result)
	return nil
}

// publishBinding publishes an already-owned bind into the shared executor
// Registry and persists its pod assignment, workspace root, and any §14
// advisory warnings, without touching the coordination lease. It is the
// publish half of registerBinding, split out so a caller that already holds
// the lease can publish unconditionally instead of routing back through the
// idempotent self-renew acquire. On the early-commit paths (single-call
// /start store.Create, delegated-child materialize store.Update) the lease is
// hoisted-acquired ahead of the running-commit, so the row is already
// committed to `running` by the time the binding publishes; gating the
// publish on a second acquire there would let a transient leaseStore error
// strand a committed running row with a held lease but no binding, the exact
// lease-without-binding decoupling co-location removes. The publish must
// therefore be unconditional once the lease is held.
//
// spec: §4.2; §4.6.1 (coordinating replica
// holds the lease); §10.1 (per-session coordination lease).
func (s *Server) publishBinding(ctx context.Context, result *podsession.BindResult) {
	if result == nil {
		return
	}
	s.podRegistry.Put(result)
	s.persistPodAssignment(ctx, result.TenantID, result.SessionID, result.SandboxName)
	// spec: §7.3; §6.4 — capture the workspace base the adapter reported
	// on the §15.5 handshake (carried verbatim through BindResult on both
	// bind paths) on the first non-empty bind. persistWorkspaceRoot derives
	// the session's slot root from it, so a subsequent Resume can assert
	// the replacement pod's resolved root matches. The pgstore guard
	// ignores an empty payload so a later bind without the field never
	// overwrites a recorded value. F-7.3.15.
	s.persistWorkspaceRoot(ctx, result.TenantID, result.SessionID, result.WorkspaceBase)
	// F-7.4.15: republish any §14 advisory warnings the adapter raised
	// during FinalizeWorkspace materialization. The
	// `workspace_plan_strip_components_skip` warning per §7.4
	// is the only producer in v1; SSE subscribers see one event per
	// skipped archive entry so a client can audit the strip-components
	// rule.
	s.publishWorkspaceWarnings(result)
}

// stampBindingGeneration records on bind the session row's
// coordination_generation under which this replica publishes it, ahead of
// the publish. A nil bind (a claimless session) is left alone.
// spec: §10.1.1, §10.1.5.
func stampBindingGeneration(bind *podsession.BindResult, generation int64) {
	if bind != nil {
		bind.CoordinationGeneration = generation
	}
}

// createdRowGeneration is the coordination_generation a row passed to
// store.Create carries once created: both stores raise a zero generation to
// 1 at create, so a zero input reads as 1. spec: §10.1.1.
func createdRowGeneration(generation int64) int64 {
	if generation == 0 {
		return 1
	}
	return generation
}

// persistPodAssignment writes the bound pod's SandboxName back to the
// session row so a fresh gateway replica can pick up the binding from
// Postgres after a coordinator handoff. Best-effort: an update failure
// is logged via the configured error handler but does not fail the
// claim — the in-memory Registry remains authoritative for this
// replica, and the next coordination sweep will re-publish the
// assignment. spec: §4.2 — "Pod-to-session binding".
func (s *Server) persistPodAssignment(ctx context.Context, tenantID, sessionID, podAssignment string) {
	if podAssignment == "" {
		return
	}
	_, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.PodAssignment = podAssignment
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: persist pod_assignment for session %s: %v", sessionID, err)
	}
}

// persistWorkspaceRoot derives the session's §6.4 slot root,
// `<base>/slots/{sessionId}/current`, from the workspace base the adapter
// reported and records it on the session row at the first non-empty bind.
// The derivation lives here rather than at the call sites because the
// write is first-non-empty-wins: a caller that passed an underived base
// would fix the column at the pod's base whenever it ran first, and the
// §7.3 step (d) guard would then reject every resume.
//
// The pgstore-side write guard ignores empty payloads so a follow-on bind
// that did not capture a base cannot clobber a recorded root. The recorded
// value feeds the §7.3 assertion on a subsequent Resume: the gateway reads
// row.WorkspaceRoot and passes it via ResumeRequest.expected_workspace_root
// for the replacement pod's adapter to compare against the root it
// resolves for the session. Best-effort: a store failure logs and
// continues.
//
// spec: §7.3 step (d); §6.4. F-7.3.15.
func (s *Server) persistWorkspaceRoot(ctx context.Context, tenantID, sessionID, workspaceBase string) {
	root := slotlayout.SessionCurrentDir(workspaceBase, sessionID)
	if root == "" {
		return
	}
	_, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if row.WorkspaceRoot == "" {
			row.WorkspaceRoot = root
		}
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: persist workspace_root for session %s: %v", sessionID, err)
	}
}
