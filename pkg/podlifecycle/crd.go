// SPDX-License-Identifier: MIT

package podlifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/podclaim/state"
)

// Compile-time binding of the §17.1 row 9 / §4.6.1 named default
// implementation (`kubernetes-sigs/agent-sandbox` CRDs) to the
// forward-compatibility interfaces. These assertions are the machine-
// checked statement that the agent-sandbox types are the v1 PoolReader,
// PodLifecycleManager, and PoolManager — a breaking upstream change or
// an alternative backend swaps the implementations behind these
// interfaces with every consumer untouched. spec:
// §4.6.1; §17.1 row 9. F-17.1.11.
var (
	_ PoolReader          = (*CRDPoolReader)(nil)
	_ PodLifecycleManager = (*CRDPodLifecycleManager)(nil)
	_ PoolManager         = (*CRDPoolManager)(nil)
)

// CRDPoolReader is the v1 default PoolReader: a thin
// translator over the lenny.dev/v1alpha1 PodSet /
// AgentPod CRDs. spec: §4.6.1.
type CRDPoolReader struct {
	// Client is the controller-runtime client every read uses.
	Client client.Client
	// Namespace scopes the pool reads to the agent namespace. Empty
	// means cluster-scoped (multi-tenant deployments may want to scope
	// to one namespace; v1 single-namespace deployments leave this as
	// the agent namespace).
	Namespace string
}

// ListPools implements PoolReader.
func (r *CRDPoolReader) ListPools(ctx context.Context) ([]PoolStatus, error) {
	var templates lennyv1.PodSetList
	if err := r.Client.List(ctx, &templates, client.InNamespace(r.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list PodSets: %w", err)
	}
	out := make([]PoolStatus, 0, len(templates.Items))
	for i := range templates.Items {
		status, err := r.statusFromTemplate(ctx, &templates.Items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, status)
	}
	return out, nil
}

// GetPoolStatus implements PoolReader.
func (r *CRDPoolReader) GetPoolStatus(ctx context.Context, poolName string) (PoolStatus, error) {
	var tmpl lennyv1.PodSet
	key := client.ObjectKey{Namespace: r.Namespace, Name: poolName}
	if err := r.Client.Get(ctx, key, &tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return PoolStatus{}, fmt.Errorf("%w: %s", ErrPoolNotFound, poolName)
		}
		return PoolStatus{}, fmt.Errorf("podlifecycle: get PodSet %q: %w", poolName, err)
	}
	return r.statusFromTemplate(ctx, &tmpl)
}

// statusFromTemplate materializes a PoolStatus from a PodSet
// plus its companion PodSet + AgentPod-list observations.
func (r *CRDPoolReader) statusFromTemplate(ctx context.Context, tmpl *lennyv1.PodSet) (PoolStatus, error) {
	status := PoolStatus{
		Name:             tmpl.Name,
		Namespace:        tmpl.Namespace,
		IsolationProfile: tmpl.Spec.IsolationProfile,
	}
	for _, c := range tmpl.Status.Conditions {
		status.Conditions = append(status.Conditions, PoolCondition{
			Type:    PoolConditionType(c.Type),
			Status:  ConditionStatus(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}
	// PodSet carries minWarm/maxWarm and the observed
	// warmCount; absence is not an error (a freshly-created pool may
	// not have a SWP yet).
	var swp lennyv1.PodSet
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: tmpl.Namespace, Name: tmpl.Name}, &swp); err == nil {
		status.MinWarm = swp.Spec.MinWarm
		status.MaxWarm = swp.Spec.MaxWarm
		status.WarmCount = swp.Status.WarmCount
	} else if !apierrors.IsNotFound(err) {
		return PoolStatus{}, fmt.Errorf("podlifecycle: get PodSet %q: %w", tmpl.Name, err)
	}
	// Count idle vs claimed AgentPods belonging to this pool. The
	// list is unindexed; v1 deployments are expected to scope reads
	// to one namespace and accept O(pods) per read. Production may
	// add a label-selector index when warranted.
	var agentPods lennyv1.AgentPodList
	if err := r.Client.List(ctx, &agentPods, client.InNamespace(tmpl.Namespace)); err != nil {
		return PoolStatus{}, fmt.Errorf("podlifecycle: list AgentPods for pool %q: %w", tmpl.Name, err)
	}
	for _, sb := range agentPods.Items {
		if sb.Spec.PodSetRef != tmpl.Name {
			continue
		}
		// spec: §6.2 — claimed and reserved are the occupied coarse phases.
		// A reserved pod is held for its pinned tenant and excluded from idle
		// inventory, so it counts as claimed for pool accounting; a pod
		// serving any number of concurrent sessions projects claimed.
		switch PodState(sb.Status.Phase) {
		case PodStateIdle:
			status.IdleCount++
		case PodStateClaimed, PodStateReserved:
			status.ClaimedCount++
		}
	}
	return status, nil
}

// CRDPodLifecycleManager is the v1 default
// PodLifecycleManager. Each method is a thin translator onto the
// PodClaim / AgentPod CRDs the §4.6.1 design pins as the lifecycle
// contract. The gateway's full claim path (the §4.7 binder, the
// fallback ClaimIdle, the SPIFFE attestation) lives in
// pkg/gateway/podsession; the spec mandates that consumers route
// through this interface, with the binder's logic moving behind a
// PodLifecycleManager implementation in a follow-on iteration. Until
// then this type exposes the spec surface so future replacements (a
// custom kubebuilder backend, a multi-cluster router) can be dropped
// in without changing every call site.
// spec: §4.6.1.
type CRDPodLifecycleManager struct {
	CRDPoolReader
}

// ClaimPod implements PodLifecycleManager under the §4.6.1 per-pod
// occupancy claim model. The v1 default selects an idle AgentPod from the
// pool and creates the deterministic per-pod PodClaim
// (`claim-<podName>`) that guards acquisition, then writes the claim's
// first `bound` binding state. The CREATE under the deterministic name is
// the authoritative single-claim guard: an AlreadyExists collision (or a
// Forbidden from the lenny-podclaim-guard webhook) means another writer
// already bound the pod, surfaced as ErrClaimConflict so the caller
// re-reads the idle inventory and retries with a different pod.
//
// The gateway does not write AgentPod.status: the WarmPoolController
// projects the pod's coarse occupancy phase (claimed) from the claim's
// binding state (§4.6.3 ownership decomposition, §4.6.1 occupancy
// projection). The session-to-pod binding lives on the Postgres session
// row's pod_assignment column rather than on the CRD, so the per-pod claim
// carries no session identifier; the returned PodHandle echoes sessionID
// for the caller's attribution alone.
//
// spec: §4.6.1; §4.6.3 (gateway is not an AgentPod.status writer).
func (m *CRDPodLifecycleManager) ClaimPod(ctx context.Context, poolName, sessionID string, opts ClaimOpts) (PodHandle, error) {
	pod, err := m.findIdleAgentPod(ctx, poolName)
	if err != nil {
		return PodHandle{}, err
	}
	// Per-pod claim CREATE: the deterministic claim-<podName> name is the
	// single-claim guard. AlreadyExists (the name collision) and Forbidden
	// (the lenny-podclaim-guard webhook flagging an existing claim for
	// the same AgentPod) both mean another writer bound this pod, so the
	// caller retries with a fresh idle pod via ErrClaimConflict.
	claim := &lennyv1.PodClaim{}
	claim.Namespace = pod.Namespace
	claim.Name = podclaim.ClaimName(pod.Name)
	claim.Spec.AgentPodRef = pod.Name
	// The claim's spec carries agentPodRef and tenantId (§4.6.1
	// claim-spec contract); stamp the tenant the pod is pinned to.
	claim.Spec.TenantID = opts.TenantID
	if err := m.Client.Create(ctx, claim); err != nil {
		if apierrors.IsAlreadyExists(err) || apierrors.IsForbidden(err) {
			return PodHandle{}, ErrClaimConflict
		}
		return PodHandle{}, fmt.Errorf("podlifecycle: create claim for AgentPod %q: %w", pod.Name, err)
	}
	// §4.6.1: the claim is created spec-only; write the first `bound`
	// binding state with a subsequent status patch (the status subresource
	// is not writable by Create), stamping the binding-state-transition time
	// the orphan GC keys its live-binding-state reclaim on, matching the
	// canonical pkg/gateway/podclaim writer's field set. A failure here
	// deletes the partial claim so a retry is not blocked by a claim with
	// empty status.
	statusOriginal := claim.DeepCopy()
	claim.Status.Phase = string(claimstate.Bound)
	claim.Status.BindingStateTransitionTime = &metav1.Time{Time: time.Now().UTC()}
	if err := m.Client.Status().Patch(ctx, claim, client.MergeFrom(statusOriginal)); err != nil {
		_ = m.Client.Delete(ctx, claim)
		return PodHandle{}, fmt.Errorf("podlifecycle: claim AgentPod %q bound status: %w", pod.Name, err)
	}
	return PodHandle{
		AgentPodName: pod.Name,
		Namespace:    pod.Namespace,
		SessionID:    sessionID,
		PoolName:     poolName,
		WarmMode:     warmModeFromAnnotations(pod.Annotations),
		// CertExpiresAt + AdapterEndpoint are filled in by GetPodStatus
		// once the AgentPod-to-Pod reconciler reports them on status.
	}, nil
}

// ReleasePod implements PodLifecycleManager under the §4.6.1 per-pod
// occupancy claim model: releasing a pod deletes its per-pod PodClaim
// (`claim-<podName>`), and the WarmPoolController projects the pod's
// occupancy phase (§4.6.1 occupancy projection). The gateway does not write AgentPod.status to roll the pod
// back (§4.6.3). A missing claim is treated as already-released — release
// is idempotent.
func (m *CRDPodLifecycleManager) ReleasePod(ctx context.Context, handle PodHandle) error {
	claim := &lennyv1.PodClaim{}
	claim.Namespace = handle.Namespace
	claim.Name = podclaim.ClaimName(handle.AgentPodName)
	if err := m.Client.Delete(ctx, claim); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("podlifecycle: delete claim for AgentPod %q: %w", handle.AgentPodName, err)
	}
	return nil
}

// DrainPod implements PodLifecycleManager. The v1 default sets the
// AgentPod phase to draining; the controller's drain reconciler runs
// the §7.1 seal-and-export sequence when checkpointFirst is true.
func (m *CRDPodLifecycleManager) DrainPod(ctx context.Context, handle PodHandle, checkpointFirst bool) (DrainResult, error) {
	var pod lennyv1.AgentPod
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.AgentPodName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return DrainResult{}, nil
		}
		return DrainResult{}, fmt.Errorf("podlifecycle: get AgentPod %q: %w", handle.AgentPodName, err)
	}
	original := pod.DeepCopy()
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if checkpointFirst {
		pod.Annotations["lenny.dev/drain-checkpoint-first"] = "true"
	}
	if err := m.Client.Patch(ctx, &pod, client.MergeFrom(original)); err != nil {
		return DrainResult{}, fmt.Errorf("podlifecycle: drain AgentPod %q: %w", handle.AgentPodName, err)
	}
	statusOriginal := pod.DeepCopy()
	pod.Status.Phase = string(PodStateDraining)
	if err := m.Client.Status().Patch(ctx, &pod, client.MergeFrom(statusOriginal)); err != nil {
		return DrainResult{}, fmt.Errorf("podlifecycle: drain AgentPod %q status: %w", handle.AgentPodName, err)
	}
	return DrainResult{TornDown: true}, nil
}

// GetPodStatus implements PodLifecycleManager.
func (m *CRDPodLifecycleManager) GetPodStatus(ctx context.Context, handle PodHandle) (PodStatus, error) {
	var pod lennyv1.AgentPod
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.AgentPodName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return PodStatus{}, ErrPodNotFound
		}
		return PodStatus{}, fmt.Errorf("podlifecycle: get AgentPod %q: %w", handle.AgentPodName, err)
	}
	return PodStatus{
		Phase:    PodState(pod.Status.Phase),
		PodIP:    pod.Status.PodIP,
		PodName:  pod.Status.PodName,
		NodeName: pod.Status.NodeName,
		TenantID: pod.Status.TenantID,
	}, nil
}

// findIdleAgentPod returns the first idle AgentPod in poolName, or
// ErrPodNotIdle when the pool has none. The selection is read-only;
// the claim itself is a status patch the caller commits.
func (m *CRDPodLifecycleManager) findIdleAgentPod(ctx context.Context, poolName string) (*lennyv1.AgentPod, error) {
	var agentPods lennyv1.AgentPodList
	if err := m.Client.List(ctx, &agentPods, client.InNamespace(m.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list AgentPods for pool %q: %w", poolName, err)
	}
	for i := range agentPods.Items {
		sb := &agentPods.Items[i]
		if sb.Spec.PodSetRef != poolName {
			continue
		}
		if PodState(sb.Status.Phase) == PodStateIdle {
			return sb, nil
		}
	}
	return nil, fmt.Errorf("%w: pool %s", ErrPodNotIdle, poolName)
}

// warmModeFromAnnotations reads the §6.1 warm mode the warming
// controller stamps on an AgentPod. An unrecognized or missing
// annotation defaults to pod_warm (the §6.1 default mode).
func warmModeFromAnnotations(annotations map[string]string) WarmMode {
	if annotations["lenny.dev/warm-mode"] == string(WarmModeSDKWarm) {
		return WarmModeSDKWarm
	}
	return WarmModePodWarm
}

// CRDPoolManager is the v1 default PoolManager. The §4.6
// implementation lives in pkg/controller/warmpool and pkg/controller/
// sandbox; this type is the spec's facade so the controller calls
// route through the §4.6.1 interface rather than the CRD types
// directly. The methods translate to CRD writes; orchestration that
// already runs in the WarmPoolController stays there for v1, and a
// follow-on iteration moves the orchestration behind these methods.
type CRDPoolManager struct {
	CRDPoolReader
}

// ReconcilePool implements PoolManager.
func (m *CRDPoolManager) ReconcilePool(ctx context.Context, cfg PoolConfig) error {
	if cfg.Name == "" {
		return errors.New("podlifecycle: ReconcilePool requires Name")
	}
	tmpl, err := m.getOrInitTemplate(ctx, cfg)
	if err != nil {
		return err
	}
	original := tmpl.DeepCopy()
	tmpl.Spec.RuntimeRef = cfg.RuntimeRef
	if cfg.IsolationProfile != "" {
		tmpl.Spec.IsolationProfile = cfg.IsolationProfile
	}
	// The warm targets sit on the same PodSet as the pool settings. A
	// config that carries neither leaves the live targets alone.
	if cfg.MinWarm > 0 || cfg.MaxWarm > 0 {
		tmpl.Spec.MinWarm = cfg.MinWarm
		tmpl.Spec.MaxWarm = cfg.MaxWarm
	}
	if tmpl.ResourceVersion == "" {
		// Fresh template — Create it.
		if err := m.Client.Create(ctx, tmpl); err != nil {
			return fmt.Errorf("podlifecycle: create PodSet %q: %w", cfg.Name, err)
		}
	} else if err := m.Client.Patch(ctx, tmpl, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("podlifecycle: patch PodSet %q: %w", cfg.Name, err)
	}
	return nil
}

// ApplyPoolDefinition implements PoolManager.
func (m *CRDPoolManager) ApplyPoolDefinition(ctx context.Context, def PoolDefinition) error {
	if def.Deleted {
		var tmpl lennyv1.PodSet
		key := client.ObjectKey{Namespace: def.Spec.Namespace, Name: def.Spec.Name}
		if err := m.Client.Get(ctx, key, &tmpl); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("podlifecycle: get PodSet %q: %w", def.Spec.Name, err)
		}
		if err := m.Client.Delete(ctx, &tmpl); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("podlifecycle: delete PodSet %q: %w", def.Spec.Name, err)
		}
		return nil
	}
	return m.ReconcilePool(ctx, def.Spec)
}

// ReplacePod implements PoolManager. The v1 default marks the pod
// for retirement (draining); the WarmPoolController's pod-reconciler
// observes the phase and provisions a replacement.
func (m *CRDPoolManager) ReplacePod(ctx context.Context, handle PodHandle, reason string) error {
	var pod lennyv1.AgentPod
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.AgentPodName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrPodNotFound
		}
		return fmt.Errorf("podlifecycle: get AgentPod %q: %w", handle.AgentPodName, err)
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if reason != "" {
		original := pod.DeepCopy()
		pod.Annotations["lenny.dev/replace-reason"] = reason
		if err := m.Client.Patch(ctx, &pod, client.MergeFrom(original)); err != nil {
			return fmt.Errorf("podlifecycle: annotate replace AgentPod %q: %w", handle.AgentPodName, err)
		}
	}
	statusOriginal := pod.DeepCopy()
	pod.Status.Phase = string(PodStateDraining)
	return m.Client.Status().Patch(ctx, &pod, client.MergeFrom(statusOriginal))
}

// TransitionPodState implements PoolManager. The v1 default validates
// the requested transition against the spec's allowed-transition set
// (a subset enforced at this layer) and writes the new phase.
func (m *CRDPoolManager) TransitionPodState(ctx context.Context, handle PodHandle, from, to PodState) error {
	if !allowedTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	var pod lennyv1.AgentPod
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.AgentPodName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrPodNotFound
		}
		return fmt.Errorf("podlifecycle: get AgentPod %q: %w", handle.AgentPodName, err)
	}
	if PodState(pod.Status.Phase) != from {
		return fmt.Errorf("%w: observed phase %s != from %s", ErrInvalidTransition, pod.Status.Phase, from)
	}
	original := pod.DeepCopy()
	pod.Status.Phase = string(to)
	return m.Client.Status().Patch(ctx, &pod, client.MergeFrom(original))
}

// GarbageCollect implements PoolManager. The v1 default sweep finds
// orphan AgentPods (no matching pool) and reports them; the
// WarmPoolController's GC reconciler owns the actual delete. spec:
// §4.6.1.
func (m *CRDPoolManager) GarbageCollect(ctx context.Context) ([]OrphanResult, error) {
	var templates lennyv1.PodSetList
	if err := m.Client.List(ctx, &templates, client.InNamespace(m.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list PodSets: %w", err)
	}
	pools := sets.New[string]()
	for _, t := range templates.Items {
		pools.Insert(t.Name)
	}

	var agentPods lennyv1.AgentPodList
	if err := m.Client.List(ctx, &agentPods, client.InNamespace(m.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list AgentPods: %w", err)
	}
	var orphans []OrphanResult
	for _, sb := range agentPods.Items {
		if sb.Spec.PodSetRef == "" || !pools.Has(sb.Spec.PodSetRef) {
			orphans = append(orphans, OrphanResult{
				Kind:      "AgentPod",
				Namespace: sb.Namespace,
				Name:      sb.Name,
				Reason:    fmt.Sprintf("PoolRef %q has no matching PodSet", sb.Spec.PodSetRef),
				Action:    OrphanActionRetained,
			})
		}
	}

	var claims lennyv1.PodClaimList
	if err := m.Client.List(ctx, &claims, client.InNamespace(m.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list PodClaims: %w", err)
	}
	agentPodNames := sets.New[string]()
	for _, sb := range agentPods.Items {
		agentPodNames.Insert(sb.Name)
	}
	for _, claim := range claims.Items {
		if claim.Spec.AgentPodRef == "" || !agentPodNames.Has(claim.Spec.AgentPodRef) {
			orphans = append(orphans, OrphanResult{
				Kind:      "PodClaim",
				Namespace: claim.Namespace,
				Name:      claim.Name,
				Reason:    fmt.Sprintf("AgentPodRef %q has no matching AgentPod", claim.Spec.AgentPodRef),
				Action:    OrphanActionRetained,
			})
		}
	}
	return orphans, nil
}

// ManageFinalizer implements PoolManager.
func (m *CRDPoolManager) ManageFinalizer(ctx context.Context, handle PodHandle, action FinalizerAction) error {
	var pod lennyv1.AgentPod
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.AgentPodName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrPodNotFound
		}
		return fmt.Errorf("podlifecycle: get AgentPod %q: %w", handle.AgentPodName, err)
	}
	original := pod.DeepCopy()
	finalizers := sets.New[string](pod.Finalizers...)
	switch action {
	case FinalizerAdd:
		finalizers.Insert(lennyv1.FinalizerSessionCleanup)
	case FinalizerRemove:
		finalizers.Delete(lennyv1.FinalizerSessionCleanup)
	default:
		return fmt.Errorf("podlifecycle: unknown FinalizerAction %q", action)
	}
	pod.Finalizers = sets.List(finalizers)
	return m.Client.Patch(ctx, &pod, client.MergeFrom(original))
}

// ManagePDB implements PoolManager.
func (m *CRDPoolManager) ManagePDB(ctx context.Context, poolName string, config PDBConfig) error {
	// The PDB CRUD already lives in pkg/controller/warmpool/pdb.go;
	// for v1 this method is a no-op surface so consumers can call it
	// behind the §4.6.1 interface. A follow-on iteration migrates the
	// PDB-management goroutine to invoke this method, removing the
	// direct controller-runtime call from the reconciler hot path.
	_ = poolName
	_ = config
	return nil
}

// DrainPool implements PoolManager. The v1 default lists every
// AgentPod in poolName and patches each to phase=draining.
func (m *CRDPoolManager) DrainPool(ctx context.Context, poolName string, checkpointFirst bool) error {
	var agentPods lennyv1.AgentPodList
	if err := m.Client.List(ctx, &agentPods, client.InNamespace(m.Namespace)); err != nil {
		return fmt.Errorf("podlifecycle: list AgentPods for drain: %w", err)
	}
	for i := range agentPods.Items {
		sb := &agentPods.Items[i]
		if sb.Spec.PodSetRef != poolName {
			continue
		}
		if checkpointFirst {
			if sb.Annotations == nil {
				sb.Annotations = map[string]string{}
			}
			original := sb.DeepCopy()
			sb.Annotations["lenny.dev/drain-checkpoint-first"] = "true"
			if err := m.Client.Patch(ctx, sb, client.MergeFrom(original)); err != nil {
				return fmt.Errorf("podlifecycle: annotate drain AgentPod %q: %w", sb.Name, err)
			}
		}
		statusOriginal := sb.DeepCopy()
		sb.Status.Phase = string(PodStateDraining)
		if err := m.Client.Status().Patch(ctx, sb, client.MergeFrom(statusOriginal)); err != nil {
			return fmt.Errorf("podlifecycle: drain AgentPod %q status: %w", sb.Name, err)
		}
	}
	return nil
}

// SetPoolCondition implements PoolManager.
func (m *CRDPoolManager) SetPoolCondition(ctx context.Context, poolName string, condition PoolCondition, reason string) error {
	var tmpl lennyv1.PodSet
	key := client.ObjectKey{Namespace: m.Namespace, Name: poolName}
	if err := m.Client.Get(ctx, key, &tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %s", ErrPoolNotFound, poolName)
		}
		return fmt.Errorf("podlifecycle: get PodSet %q: %w", poolName, err)
	}
	original := tmpl.DeepCopy()
	setCondition(&tmpl, condition, reason)
	return m.Client.Status().Patch(ctx, &tmpl, client.MergeFrom(original))
}

// getOrInitTemplate returns the existing PodSet or an
// initialized in-memory one ready for Create.
func (m *CRDPoolManager) getOrInitTemplate(ctx context.Context, cfg PoolConfig) (*lennyv1.PodSet, error) {
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = m.Namespace
	}
	var tmpl lennyv1.PodSet
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: cfg.Name}, &tmpl); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("podlifecycle: get PodSet %q: %w", cfg.Name, err)
		}
		tmpl = lennyv1.PodSet{}
		tmpl.Namespace = namespace
		tmpl.Name = cfg.Name
	}
	return &tmpl, nil
}

// setCondition writes condition onto tmpl.Status.Conditions, replacing
// the existing entry of the same Type if present.
func setCondition(tmpl *lennyv1.PodSet, condition PoolCondition, reason string) {
	replaced := false
	for i := range tmpl.Status.Conditions {
		if tmpl.Status.Conditions[i].Type == string(condition.Type) {
			tmpl.Status.Conditions[i].Status = metav1.ConditionStatus(condition.Status)
			tmpl.Status.Conditions[i].Reason = condition.Reason
			tmpl.Status.Conditions[i].Message = condition.Message
			if reason != "" {
				tmpl.Status.Conditions[i].Reason = reason
			}
			replaced = true
			break
		}
	}
	if !replaced {
		tmpl.Status.Conditions = append(tmpl.Status.Conditions, metav1.Condition{
			Type:               string(condition.Type),
			Status:             metav1.ConditionStatus(condition.Status),
			Reason:             firstNonEmpty(reason, condition.Reason),
			Message:            condition.Message,
			LastTransitionTime: metav1.Now(),
		})
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// allowedTransition reports whether the §6.2 coarse pod-occupancy state
// machine admits a from → to transition. This mirror stays in lockstep with
// pkg/sandbox/state.ValidTransitions(): the warm-fill paths, the
// claim-driven occupancy projection (including the claimed → claimed
// concurrent-occupancy self-edge), and the recycle edges (the occupancy-zero
// re-warm, the same-tenant rebind, and the hold-expiry return to idle). The
// fine session-lifecycle transitions live in the Postgres session model and
// are not CRD edges (spec: §6.2). spec: spec/06_warm-pod-model.md §6.2.
func allowedTransition(from, to PodState) bool {
	allowed := map[PodState]sets.Set[PodState]{
		PodStateWarming:       sets.New(PodStateIdle, PodStateSDKConnecting, PodStateFailed),
		PodStateSDKConnecting: sets.New(PodStateIdle, PodStateFailed, PodStateTerminated, PodStateReserved),
		PodStateIdle:          sets.New(PodStateClaimed, PodStateDraining),
		PodStateClaimed:       sets.New(PodStateClaimed, PodStateDraining, PodStateSDKConnecting, PodStateReserved),
		PodStateReserved:      sets.New(PodStateClaimed, PodStateIdle),
		PodStateFailed:        sets.New(PodStateDraining),
		PodStateDraining:      sets.New(PodStateTerminated),
	}
	if next, ok := allowed[from]; ok {
		return next.Has(to)
	}
	return false
}
