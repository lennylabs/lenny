// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingcheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/createdsweeper"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/podlifecycle"
)

// tenantsLister adapts a tenantstore.Store into a
// watchdog.TenantLister so the watchdog sweeps every registered
// tenant. In single-tenant deployments it also returns "default" so
// dev-mode sessions are bounded.
// agentPodStateMirror adapts the §4.6.1 agent_pod_state store to the
// §10.1 orphan-session reconciler's MirrorReader, mapping the store's
// PodState onto the reconciler's narrow MirrorPod view. spec: §10.1. F-10.1.5.
type agentPodStateMirror struct {
	store agentpodstate.Store
}

func (a agentPodStateMirror) GetByPodID(ctx context.Context, podID string) (orphansession.MirrorPod, bool, error) {
	p, found, err := a.store.GetByPodID(ctx, podID)
	if err != nil || !found {
		return orphansession.MirrorPod{}, found, err
	}
	return orphansession.MirrorPod{PoolID: p.PoolID, Phase: p.State}, true, nil
}

func (a agentPodStateMirror) MirrorLagSeconds(ctx context.Context, poolID string) (float64, error) {
	return a.store.MirrorLagSeconds(ctx, poolID)
}

// sandboxPhaseReader is the §10.1.4 direct-Kubernetes fallback the
// orphan-session reconciler consults when the agent_pod_state mirror is
// stale or missing. It reads the authoritative Sandbox phase through the
// §4.6.1 PodLifecycleManager.GetPodStatus surface; a deleted Sandbox
// (ErrPodNotFound) reports found=false, itself a terminal signal.
// spec: §10.1. F-10.1.5.
type sandboxPhaseReader struct {
	mgr podlifecycle.PodLifecycleManager
	ns  string
}

func (r sandboxPhaseReader) PodPhase(ctx context.Context, sessionID, podID, poolID string) (string, bool, error) {
	st, err := r.mgr.GetPodStatus(ctx, podlifecycle.PodHandle{
		SandboxName: podID,
		Namespace:   r.ns,
		SessionID:   sessionID,
		PoolName:    poolID,
	})
	if errors.Is(err, podlifecycle.ErrPodNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(st.Phase), true, nil
}

type tenantsLister struct {
	store tenantstore.Store
}

func (t tenantsLister) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows)+1)
	out = append(out, "default")
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// createdSweeperReclaim adapts the podsession Binder's claimless reclaim into
// the createdsweeper.Reclaimer the §7.1 created-expiry sweep invokes before it
// drops an abandoned `created`-state row. It closes over the binder (which
// carries the kube Client, Namespace, and CredentialAssigner) so the sweep
// releases the pod claimed at /create and revokes any assigned lease through
// the same ReclaimClaimed call /terminate uses. ReclaimClaimed releases by pod
// name, so the poolRef the Reclaimer carries is dropped here. Returns nil when
// the gateway runs without a pod binder (in-memory mode), leaving the sweep to
// drop the row without a pod release.
//
// spec: §15.1; proposal §4.5.
func createdSweeperReclaim(binder *podsession.Binder) createdsweeper.Reclaimer {
	if binder == nil {
		return nil
	}
	return func(ctx context.Context, podName, _ /* poolRef */, sessionID string) error {
		return binder.ReclaimClaimed(ctx, podName, sessionID)
	}
}

// billingSessionLister enumerates the active (non-terminal) sessions the
// §11.2.1 token_usage.checkpoint producer snapshots, walking every
// registered tenant's session rows. It mirrors
// quotacheckpoint.SessionSubjectLister but returns the per-session tuple
// (a billing checkpoint is per session, not per (tenant, user) subject).
// F-11.2.1.
type billingSessionLister struct {
	sessions sessionstore.Store
	tenants  func(ctx context.Context) ([]string, error)
}

func (l billingSessionLister) ListActiveSessions(ctx context.Context) ([]billingcheckpoint.Session, error) {
	ids, err := l.tenants(ctx)
	if err != nil {
		return nil, err
	}
	var out []billingcheckpoint.Session
	for _, tenantID := range ids {
		rows, err := l.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
		if err != nil {
			return nil, err
		}
		for _, s := range rows {
			if session.IsTerminal(s.State) {
				continue
			}
			out = append(out, billingcheckpoint.Session{TenantID: tenantID, SessionID: s.ID, UserID: s.UserID})
		}
	}
	return out, nil
}
