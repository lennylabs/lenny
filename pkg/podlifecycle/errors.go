// SPDX-License-Identifier: MIT

package podlifecycle

import "errors"

// Sentinel errors the §4.6.1 interfaces surface to callers. The
// implementations wrap these so callers can branch on errors.Is
// without depending on the agent-sandbox CRD client error taxonomy.
var (
	// ErrPoolNotFound reports that GetPoolStatus / ReconcilePool /
	// ApplyPoolDefinition was called for a pool with no
	// SandboxTemplate.
	ErrPoolNotFound = errors.New("podlifecycle: pool not found")
	// ErrPodNotFound reports that the handle's Sandbox no longer
	// exists (deleted, never created, or terminated and GC'd).
	ErrPodNotFound = errors.New("podlifecycle: pod not found")
	// ErrPodNotIdle reports that ClaimPod could not find an idle pod
	// in the named pool. The caller falls back to the §4.6.1 Postgres
	// mirror path or returns POOL_EXHAUSTED to the client.
	ErrPodNotIdle = errors.New("podlifecycle: no idle pod available in pool")
	// ErrClaimConflict reports that ClaimPod observed an HTTP 409
	// resource-version conflict on the Sandbox status update — another
	// gateway replica claimed the pod between Read and CAS. The caller
	// loops back to ClaimPod with a fresh selection. spec:
	// spec/04_system-components.md line 386.
	ErrClaimConflict = errors.New("podlifecycle: claim conflict — retry")
	// ErrInvalidTransition reports that TransitionPodState attempted
	// a phase transition the §6.2 state machine does not allow.
	ErrInvalidTransition = errors.New("podlifecycle: invalid phase transition")
)
