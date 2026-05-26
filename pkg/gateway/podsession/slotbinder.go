// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// SlotBindRequest describes a session to place on a concurrent-mode
// (§5.2) pod slot. It is the concurrent-mode counterpart of BindRequest:
// rather than claiming a pod exclusively, BindSlot reserves one of the
// pod's up-to-maxConcurrent slots.
type SlotBindRequest struct {
	// Pool is the concurrent-mode SandboxWarmPool to claim a slot from.
	Pool string
	// SessionID is the §15.1 session being started. It is also the
	// slot's SlotID.
	SessionID string
	// TenantID is the tenant that owns the session. §5.2 tenant pinning
	// binds a concurrent-mode pod to its first tenant.
	TenantID string
	// Runtime is the runtime name passed to the adapter's StartSession.
	Runtime string
	// Style is the §5.2 concurrency style of the pool: StyleWorkspace
	// (workspace-concurrent — a per-slot workspace is materialized) or
	// StyleStateless (stateless-concurrent — no workspace).
	Style podclaim.ConcurrencyStyle
	// MaxConcurrent is the §5.2 per-pod slot bound.
	MaxConcurrent int32
	// Plan is the per-slot workspace the adapter materializes under
	// /workspace/slots/{slotId}/ before start. It is honored only in
	// workspace-concurrent mode; stateless-concurrent ignores it because
	// §5.2 stateless mode materializes no workspace.
	Plan *adapterv1.WorkspacePlan
	// ExperimentContext and TracingContext are delivered to the runtime
	// in the adapter manifest. Nil when unset.
	ExperimentContext *adapterv1.ExperimentContext
	TracingContext    map[string]string
	// SetupPolicy bounds the §5.1 setup phase. Honored only in
	// workspace-concurrent mode. Nil when the runtime declares no cap.
	SetupPolicy *adapterv1.SetupPolicy
	// CredentialPools names the §4.9 credential pools to lease from,
	// keyed by provider. Per §6 a concurrent-mode slot holds an
	// independent per-slot credential lease. Empty when the session
	// needs no upstream LLM credentials.
	CredentialPools map[string]string
	// PodSpiffeURI is the issuing pod's SPIFFE identity recorded on each
	// minted lease. Empty disables proxy-mode SPIFFE binding.
	PodSpiffeURI string
	// AgentInterface is the runtime's §5.1 agentInterface descriptor as
	// JSON, written into the §15.4 manifest. Nil when undeclared.
	AgentInterface []byte
	// MinPlatformVersion is the runtime's §5.1 minPlatformVersion written
	// into the §15.4 manifest. Empty when none is specified.
	MinPlatformVersion string
}

// BindSlot places a session on a concurrent-mode (§5.2) pod slot.
//
// It reserves a slot via podclaim.SlotClaimer — landing on a pod that
// is already hosting slots when one has free capacity for the tenant,
// or opening a fresh idle pod otherwise — resolves the pod's adapter
// address, runs the §15.5 version handshake, and then runs the
// per-style assignment sequence:
//
//   - workspace-concurrent (StyleWorkspace): the slot gets its own
//     workspace. BindSlot runs PrepareWorkspace, FinalizeWorkspace,
//     RunSetup, AssignCredentials, and StartSession, exactly as
//     session-mode Bind does — the §6.4 per-slot directory tree under
//     /workspace/slots/{slotId}/ is the adapter's responsibility, and
//     the pod's /workspace/shared/ tree is shared read-only across the
//     pod's slots.
//   - stateless-concurrent (StyleStateless): §5.2 materializes no
//     workspace and tracks no per-slot lifecycle. BindSlot assigns
//     credentials (a per-slot lease per §6) and runs StartSession; it
//     does not stage, finalize, or run setup, because the slot has no
//     Lenny-managed workspace.
//
// On success the caller owns the returned live adapter connection and
// the BindResult carries the slot's SlotID. Any failure after the slot
// reservation is returned so the caller can retry on another slot.
func (b *Binder) BindSlot(ctx context.Context, req SlotBindRequest) (*BindResult, error) {
	sandboxName, slotID, podIP, cl, err := b.connectSlot(ctx, req)
	if err != nil {
		return nil, err
	}

	if req.Style == podclaim.StyleWorkspace {
		// Workspace-concurrent: the slot has its own per-slot workspace
		// (§6.4). Run the full §4.7 workspace-and-start sequence.
		if err := b.stageWorkspace(ctx, cl, req.SessionID, req.Plan); err != nil {
			cl.Close()
			b.recordSlotFailure(slotFailureWorkspacePrep, req.Pool, sandboxName)
			return nil, fmt.Errorf("podsession: stage slot workspace on pod %s: %w", sandboxName, err)
		}
		if err := cl.FinalizeWorkspace(ctx, req.SessionID, req.Plan); err != nil {
			cl.Close()
			b.recordSlotFailure(slotFailureWorkspacePrep, req.Pool, sandboxName)
			return nil, fmt.Errorf("podsession: finalize slot workspace on pod %s: %w", sandboxName, err)
		}
		if err := cl.RunSetup(ctx, req.SessionID, req.Plan.GetSetupCommands(), req.SetupPolicy); err != nil {
			cl.Close()
			b.recordSlotFailure(slotFailureSetup, req.Pool, sandboxName)
			return nil, fmt.Errorf("podsession: run slot setup on pod %s: %w", sandboxName, err)
		}
	}
	// Stateless-concurrent skips the workspace path entirely: §5.2
	// stateless mode materializes no workspace and runs no setup.

	if err := b.assignSlotCredentials(ctx, cl, req); err != nil {
		cl.Close()
		b.recordSlotFailure(slotFailureCredentialAssignment, req.Pool, sandboxName)
		return nil, fmt.Errorf("podsession: assign slot credentials on pod %s: %w", sandboxName, err)
	}
	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{
		SessionID:          req.SessionID,
		Runtime:            req.Runtime,
		ExperimentContext:  req.ExperimentContext,
		TracingContext:     req.TracingContext,
		AgentInterface:     req.AgentInterface,
		MinPlatformVersion: req.MinPlatformVersion,
	}); err != nil {
		cl.Close()
		b.recordSlotFailure(slotFailureSessionStart, req.Pool, sandboxName)
		return nil, fmt.Errorf("podsession: start slot session on pod %s: %w", sandboxName, err)
	}
	return &BindResult{
		SessionID:   req.SessionID,
		TenantID:    req.TenantID,
		SandboxName: sandboxName,
		PodIP:       podIP,
		SlotID:      slotID,
		Adapter:     cl,
	}, nil
}

// recordSlotFailure emits the §5.2 line 12 lenny_slot_failure_total
// counter for a slot bind stage that failed after the slot was reserved.
// It is a no-op when the binder has no SlotFailure hook.
//
// spec: §5.2 line 12.
func (b *Binder) recordSlotFailure(errorType, pool, podName string) {
	if b.SlotFailure != nil {
		b.SlotFailure(errorType, pool, podName)
	}
}

// assignSlotCredentials mints the slot's §4.9 credential leases and
// pushes them to the pod via AssignCredentials. Per §6 each
// concurrent-mode slot holds an independent per-slot lease, so a
// rotation on one slot does not disrupt sibling slots. It is a no-op
// when the binder has no credential service or the request names no
// pools.
func (b *Binder) assignSlotCredentials(ctx context.Context, cl *adapterclient.Client, req SlotBindRequest) error {
	if b.Credentials == nil || len(req.CredentialPools) == 0 {
		return nil
	}
	leases := make(map[string]*adapterv1.CredentialLease, len(req.CredentialPools))
	for provider, pool := range req.CredentialPools {
		lease, err := b.Credentials.AssignProto(pool, req.SessionID, req.PodSpiffeURI)
		if err != nil {
			// §4.9 line 1220 pre-claim race: surface a typed error so the
			// caller can release the slot and emit the mismatch metric.
			return &CredentialAssignmentError{Provider: provider, Pool: pool, Err: err}
		}
		lease.Provider = provider
		leases[provider] = lease
	}
	return cl.AssignCredentials(ctx, req.SessionID, leases)
}

// connectSlot reserves a concurrent-mode slot from the pool, resolves
// the slot's pod adapter address, dials it, and runs the §15.5 version
// handshake. On success the caller owns cl and must close it once the
// session ends or on any later failure.
//
// A podclaim.ErrNoConcurrentSlot or podclaim.ErrTenantMismatch from the
// SlotClaimer is returned unwrapped so the gateway's session-creation
// handler can map it to WARM_POOL_EXHAUSTED with the §5.2
// "concurrent_slots_exhausted" reason.
func (b *Binder) connectSlot(ctx context.Context, req SlotBindRequest) (sandboxName, slotID, podIP string, cl *adapterclient.Client, err error) {
	claimer := &podclaim.SlotClaimer{
		Client:         b.Client,
		Namespace:      b.Namespace,
		Counter:        b.SlotCounter,
		OnSlotConflict: b.SlotConflict,
		OnRehydrate:    b.Rehydration,
	}
	res, err := claimer.ClaimSlot(ctx, podclaim.SlotRequest{
		Pool:          req.Pool,
		SessionID:     req.SessionID,
		TenantID:      req.TenantID,
		Style:         req.Style,
		MaxConcurrent: req.MaxConcurrent,
	})
	if err != nil {
		// The §5.2 line 519 exhaustion sentinels are returned unwrapped
		// for the caller's errors.Is check, which maps them to
		// WARM_POOL_EXHAUSTED with the right details.reason: ErrNoIdlePod
		// → "no_idle_pods" (pool holds no pods), ErrNoConcurrentSlot and
		// ErrTenantMismatch → "concurrent_slots_exhausted".
		if errors.Is(err, podclaim.ErrNoConcurrentSlot) ||
			errors.Is(err, podclaim.ErrTenantMismatch) ||
			errors.Is(err, podclaim.ErrNoIdlePod) {
			return "", "", "", nil, err
		}
		return "", "", "", nil, fmt.Errorf("podsession: claim concurrent slot: %w", err)
	}
	sandboxName = res.SandboxName
	slotID = res.SlotID

	sb, err := b.resolveSandbox(ctx, sandboxName)
	if err != nil {
		return "", "", "", nil, err
	}
	podIP = sb.Status.PodIP

	addr := net.JoinHostPort(podIP, strconv.Itoa(b.AdapterPort))
	cl, err = b.DialAdapter(addr)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("podsession: dial slot adapter at %s: %w", addr, err)
	}

	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return "", "", "", nil, fmt.Errorf("podsession: negotiate version with %s: %w", sandboxName, err)
	}
	if resp.GetIncompatible() {
		cl.Close()
		return "", "", "", nil, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName,
		)
	}
	return sandboxName, slotID, podIP, cl, nil
}

// ReleaseSlot tears down a concurrent-mode slot when its session ends.
// It shuts the slot's runtime down through the adapter, closes the
// adapter connection, deletes the slot's SandboxClaim, and decrements
// the pod's status.activeSlots.
//
// Unlike session-mode Release, ReleaseSlot does not drain the pod: a
// concurrent-mode pod hosts sibling slots, and per §6.2 the pod returns
// to the idle phase only when its last slot drains (activeSlots reaches
// 0). The slot-count decrement and the idle-on-last transition are
// handled by podclaim.SlotClaimer.ReleaseSlot. The adapter Shutdown is
// best-effort.
func (b *Binder) ReleaseSlot(ctx context.Context, result *BindResult) error {
	if result.Adapter != nil {
		_, _ = result.Adapter.Shutdown(ctx, result.SessionID)
		result.Adapter.Close()
	}
	claimer := &podclaim.SlotClaimer{Client: b.Client, Namespace: b.Namespace, Counter: b.SlotCounter}
	return claimer.ReleaseSlot(ctx, result.SandboxName, result.SessionID)
}

// drainSandbox transitions a Sandbox to the draining phase. It is the
// shared teardown used when a concurrent-mode pod must be retired as a
// whole (for example after the §5.2 unhealthy-slot threshold) rather
// than releasing a single slot.
func (b *Binder) drainSandbox(ctx context.Context, sandboxName string) error {
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: sandboxName}, &sb); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("podsession: get sandbox %s: %w", sandboxName, err)
	}
	sb.Status.Phase = string(state.Draining)
	if err := b.Client.Status().Update(ctx, &sb); err != nil {
		return fmt.Errorf("podsession: drain sandbox %s: %w", sandboxName, err)
	}
	return nil
}
