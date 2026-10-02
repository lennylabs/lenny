// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// ClaimResult reports the §7.1-step-4 pod claim made at session create,
// for the gateway to persist on the session row so a later Prepare and
// Launch can reconnect to the claimed pod from the binding alone (§4.6).
// spec: §4.1 (proposal), §7.1 step 4.
type ClaimResult struct {
	// SandboxName is the claimed Sandbox the binding is persisted against.
	SandboxName string
	// Pool is the pool the pod was claimed from, persisted so Prepare and
	// Launch (and the §4.5 created-expiry reclaim) can name the pool.
	Pool string
	// PodIP is the claimed pod's address, returned for the §15.1 create
	// response and the §6.3 pod-claim metric.
	PodIP string
	// SlotID identifies the §5.2 concurrent-workspace slot reserved at
	// create by ClaimSlot. It equals SessionID (one session per slot), so
	// the binding is reconstructable from SandboxName + Pool + the session
	// id. Empty for an exclusive (maxConcurrentSessions=1) Claim, where the
	// whole pod is claimed for the session; non-empty marks the create-time
	// disposition as a reserved concurrent slot that /start reconnects to
	// via BindReservedSlot rather than re-reserving. spec: §5.2.
	SlotID string
	// WorkspaceBase is the §6.4 workspace base the pod's adapter reported
	// on the §15.5 handshake at claim, verbatim. Prepare's archive symlink
	// canonicalization and Launch's SDK-warm ConfigureWorkspace cwd derive
	// the session's `<base>/slots/{sessionId}/current` root from it without
	// re-handshaking before they need it. Empty for a ClaimSlot result: the
	// base is reported when BindReservedSlot reconnects.
	WorkspaceBase string
	// PodClaim is the §6.3 "pod claim and routing" phase duration (claim,
	// pod-IP resolution, mTLS dial, version handshake), for the create
	// handler to record on the §6.3 / §16.1 pod_claim phase histogram.
	PodClaim time.Duration
}

// Claim performs the §7.1 step-4 pod claim at session create: it claims an
// idle warm pod from the pool, resolves the pod's adapter address, and runs
// the §15.5 version handshake to confirm the pod is usable and to negotiate
// the workspace root. It records the §6.3 pod-claim phase duration and
// returns the claimed Sandbox name, pool, pod IP, and negotiated workspace
// root for the gateway to persist on the session row, so a later Prepare
// and Launch reconnect from the binding without a held connection (§4.6).
// The handshake connection is closed before Claim returns. A pool-exhaustion
// or handshake failure is returned so the create handler surfaces it before
// the client uploads. spec: §4.1 (proposal), §7.1 step 4; §6.3.
func (b *Binder) Claim(ctx context.Context, req BindRequest) (*ClaimResult, error) {
	phaseStart := time.Now()
	sb, cl, neg, err := b.connect(ctx, podclaim.ClaimRequest{
		Pool: req.Pool, SessionID: req.SessionID, TenantID: req.TenantID, KeepsRuntime: req.KeepsRuntime,
	})
	if err != nil {
		return nil, err
	}
	// Claim runs only the claim and handshake; the setup chain reconnects
	// at Prepare, so the connection is not held across the upload window.
	cl.Close()
	return &ClaimResult{
		SandboxName:   sb.Name,
		Pool:          req.Pool,
		PodIP:         sb.Status.PodIP,
		WorkspaceBase: neg.WorkspaceBase,
		PodClaim:      time.Since(phaseStart),
	}, nil
}
