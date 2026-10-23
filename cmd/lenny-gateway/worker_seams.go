// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingcheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/podlifecycle"
)

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

// defaultTenantID is the built-in tenant every deployment serves. The
// in-memory tenant store has no row for it, so the listers below add it when
// the store holds no `default` row.
const defaultTenantID = "default"

// tenantsLister adapts a tenantstore.Store into the TenantLister the
// background workers read. It returns every listed tenant (not tombstoned),
// and adds `default` only when the store has no `default` row: a tombstoned
// `default` row therefore drops out, so the workers stop visiting a deleted
// `default`. The row is read with Get only when the listing lacks `default`,
// because the Postgres Get decrypts the salt of an active row; a Get error
// other than tenantstore.ErrNotFound is returned.
//
// spec: §12.8 (Compliance Interfaces: Tenant deletion lifecycle), §10.2.
type tenantsLister struct {
	store tenantstore.Store
}

func (t tenantsLister) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	ids := tenantIDs(rows)
	if slices.Contains(ids, defaultTenantID) {
		return ids, nil
	}
	if _, err := t.store.Get(ctx, defaultTenantID); err == nil {
		// The `default` row exists but is tombstoned.
		return ids, nil
	} else if !errors.Is(err, tenantstore.ErrNotFound) {
		return nil, fmt.Errorf("read the default tenant row: %w", err)
	}
	return append([]string{defaultTenantID}, ids...), nil
}

// allTenantsLister lists every tenant row, tombstones included, adding
// `default` only when the store has no `default` row. It serves the workers
// that must keep reaching a deleted tenant's retained rows: the billing
// retention pruner (an exempt tenant's billing events age out after its
// tombstone), the issued-token GC, and the revocation-cache rehydration (a
// replica restarted after the tombstone still loads the kept revoked rows).
//
// spec: §13.3 (Credential Flow: Authoritative durability for revocation),
// §12.8 (Compliance Interfaces: Tenant deletion lifecycle), §12.2.
type allTenantsLister struct {
	store tenantstore.Store
}

func (t allTenantsLister) ListTenants(ctx context.Context) ([]string, error) {
	rows, err := t.store.List(ctx, tenantstore.ListFilter{IncludeDeleted: true})
	if err != nil {
		return nil, err
	}
	ids := tenantIDs(rows)
	if slices.Contains(ids, defaultTenantID) {
		return ids, nil
	}
	return append([]string{defaultTenantID}, ids...), nil
}

// tenantIDs returns the IDs of rows in order.
func tenantIDs(rows []tenantstore.Tenant) []string {
	out := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out
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
