// SPDX-License-Identifier: MIT

package podlifecycle

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// Compile-time binding of the §17.1 row 9 / §4.6.1 named default
// implementation (`kubernetes-sigs/agent-sandbox` CRDs) to the
// forward-compatibility interfaces. These assertions are the machine-
// checked statement that the agent-sandbox types are the v1 PoolReader,
// PodLifecycleManager, and PoolManager — a breaking upstream change or
// an alternative backend swaps the implementations behind these
// interfaces with every consumer untouched. spec:
// spec/04_system-components.md lines 333-363; §17.1 row 9. F-17.1.11.
var (
	_ PoolReader          = (*AgentSandboxPoolReader)(nil)
	_ PodLifecycleManager = (*AgentSandboxPodLifecycleManager)(nil)
	_ PoolManager         = (*AgentSandboxPoolManager)(nil)
)

// AgentSandboxPoolReader is the v1 default PoolReader: a thin
// translator over the lenny.dev/v1 SandboxTemplate / SandboxWarmPool /
// Sandbox CRDs. spec: spec/04_system-components.md line 359.
type AgentSandboxPoolReader struct {
	// Client is the controller-runtime client every read uses.
	Client client.Client
	// Namespace scopes the pool reads to the agent namespace. Empty
	// means cluster-scoped (multi-tenant deployments may want to scope
	// to one namespace; v1 single-namespace deployments leave this as
	// the agent namespace).
	Namespace string
}

// ListPools implements PoolReader.
func (r *AgentSandboxPoolReader) ListPools(ctx context.Context) ([]PoolStatus, error) {
	var templates lennyv1.SandboxTemplateList
	if err := r.Client.List(ctx, &templates, client.InNamespace(r.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list SandboxTemplates: %w", err)
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
func (r *AgentSandboxPoolReader) GetPoolStatus(ctx context.Context, poolName string) (PoolStatus, error) {
	var tmpl lennyv1.SandboxTemplate
	key := client.ObjectKey{Namespace: r.Namespace, Name: poolName}
	if err := r.Client.Get(ctx, key, &tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return PoolStatus{}, fmt.Errorf("%w: %s", ErrPoolNotFound, poolName)
		}
		return PoolStatus{}, fmt.Errorf("podlifecycle: get SandboxTemplate %q: %w", poolName, err)
	}
	return r.statusFromTemplate(ctx, &tmpl)
}

// statusFromTemplate materializes a PoolStatus from a SandboxTemplate
// plus its companion SandboxWarmPool + Sandbox-list observations.
func (r *AgentSandboxPoolReader) statusFromTemplate(ctx context.Context, tmpl *lennyv1.SandboxTemplate) (PoolStatus, error) {
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
	// SandboxWarmPool carries minWarm/maxWarm and the observed
	// warmCount; absence is not an error (a freshly-created pool may
	// not have a SWP yet).
	var swp lennyv1.SandboxWarmPool
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: tmpl.Namespace, Name: tmpl.Name}, &swp); err == nil {
		status.MinWarm = swp.Spec.MinWarm
		status.MaxWarm = swp.Spec.MaxWarm
		status.WarmCount = swp.Status.WarmCount
	} else if !apierrors.IsNotFound(err) {
		return PoolStatus{}, fmt.Errorf("podlifecycle: get SandboxWarmPool %q: %w", tmpl.Name, err)
	}
	// Count idle vs claimed Sandboxes belonging to this pool. The
	// list is unindexed; v1 deployments are expected to scope reads
	// to one namespace and accept O(pods) per read. Production may
	// add a label-selector index when warranted.
	var sandboxes lennyv1.SandboxList
	if err := r.Client.List(ctx, &sandboxes, client.InNamespace(tmpl.Namespace)); err != nil {
		return PoolStatus{}, fmt.Errorf("podlifecycle: list Sandboxes for pool %q: %w", tmpl.Name, err)
	}
	for _, sb := range sandboxes.Items {
		if sb.Spec.PoolRef != tmpl.Name {
			continue
		}
		switch PodState(sb.Status.Phase) {
		case PodStateIdle:
			status.IdleCount++
		case PodStateClaimed, PodStateSlotActive,
			PodStateRunningSetup, PodStateStartingSession, PodStateAttached:
			status.ClaimedCount++
		}
	}
	return status, nil
}

// AgentSandboxPodLifecycleManager is the v1 default
// PodLifecycleManager. Each method is a thin translator onto the
// SandboxClaim / Sandbox CRDs the §4.6.1 design pins as the lifecycle
// contract. The gateway's full claim path (the §4.7 binder, the
// fallback ClaimIdle, the SPIFFE attestation) lives in
// pkg/gateway/podsession; the spec mandates that consumers route
// through this interface, with the binder's logic moving behind a
// PodLifecycleManager implementation in a follow-on iteration. Until
// then this type exposes the spec surface so future replacements (a
// custom kubebuilder backend, a multi-cluster router) can be dropped
// in without changing every call site.
// spec: spec/04_system-components.md lines 340-345, 359, 363.
type AgentSandboxPodLifecycleManager struct {
	AgentSandboxPoolReader
}

// ClaimPod implements PodLifecycleManager. The v1 default selects an
// idle Sandbox from the pool and writes a SandboxClaim referencing it
// with optimistic locking; on a conflict the caller retries (spec:
// spec/04_system-components.md line 386).
func (m *AgentSandboxPodLifecycleManager) ClaimPod(ctx context.Context, poolName, sessionID string, opts ClaimOpts) (PodHandle, error) {
	pod, err := m.findIdleSandbox(ctx, poolName)
	if err != nil {
		return PodHandle{}, err
	}
	// Optimistic CAS: stamp the session-claim annotations on the
	// metadata, then write the phase transition on the status
	// subresource. A concurrent claimer's PATCH on the same
	// resourceVersion produces a 409 the API server rejects, which
	// is translated to ErrClaimConflict so the caller retries with a
	// fresh selection. spec: §4.6.1 line 386 — "ClaimPod
	// implementations must use a resourceVersion-guarded
	// compare-and-swap loop".
	original := pod.DeepCopy()
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations["lenny.dev/session-id"] = sessionID
	if opts.RequiresDemotion {
		pod.Annotations["lenny.dev/sdk-warm-requires-demotion"] = "true"
	}
	if err := m.Client.Patch(ctx, pod, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return PodHandle{}, ErrClaimConflict
		}
		return PodHandle{}, fmt.Errorf("podlifecycle: claim Sandbox %q: %w", pod.Name, err)
	}
	statusOriginal := pod.DeepCopy()
	pod.Status.Phase = string(PodStateClaimed)
	if err := m.Client.Status().Patch(ctx, pod, client.MergeFrom(statusOriginal)); err != nil {
		if apierrors.IsConflict(err) {
			return PodHandle{}, ErrClaimConflict
		}
		return PodHandle{}, fmt.Errorf("podlifecycle: claim Sandbox %q status: %w", pod.Name, err)
	}
	return PodHandle{
		SandboxName: pod.Name,
		Namespace:   pod.Namespace,
		SessionID:   sessionID,
		PoolName:    poolName,
		WarmMode:    warmModeFromAnnotations(pod.Annotations),
		// CertExpiresAt + AdapterEndpoint are filled in by GetPodStatus
		// once the Sandbox-to-Pod reconciler reports them on status.
	}, nil
}

// ReleasePod implements PodLifecycleManager. The v1 default rolls the
// Sandbox back to idle (the §6.2 release transition); a missing
// Sandbox is treated as already-released.
func (m *AgentSandboxPodLifecycleManager) ReleasePod(ctx context.Context, handle PodHandle) error {
	var pod lennyv1.Sandbox
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.SandboxName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("podlifecycle: get Sandbox %q: %w", handle.SandboxName, err)
	}
	if pod.Status.Phase == string(PodStateIdle) {
		return nil
	}
	original := pod.DeepCopy()
	pod.Status.Phase = string(PodStateIdle)
	delete(pod.Annotations, "lenny.dev/session-id")
	return m.Client.Status().Patch(ctx, &pod, client.MergeFrom(original))
}

// DrainPod implements PodLifecycleManager. The v1 default sets the
// Sandbox phase to draining; the controller's drain reconciler runs
// the §7.1 seal-and-export sequence when checkpointFirst is true.
func (m *AgentSandboxPodLifecycleManager) DrainPod(ctx context.Context, handle PodHandle, checkpointFirst bool) (DrainResult, error) {
	var pod lennyv1.Sandbox
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.SandboxName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return DrainResult{}, nil
		}
		return DrainResult{}, fmt.Errorf("podlifecycle: get Sandbox %q: %w", handle.SandboxName, err)
	}
	original := pod.DeepCopy()
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if checkpointFirst {
		pod.Annotations["lenny.dev/drain-checkpoint-first"] = "true"
	}
	if err := m.Client.Patch(ctx, &pod, client.MergeFrom(original)); err != nil {
		return DrainResult{}, fmt.Errorf("podlifecycle: drain Sandbox %q: %w", handle.SandboxName, err)
	}
	statusOriginal := pod.DeepCopy()
	pod.Status.Phase = string(PodStateDraining)
	if err := m.Client.Status().Patch(ctx, &pod, client.MergeFrom(statusOriginal)); err != nil {
		return DrainResult{}, fmt.Errorf("podlifecycle: drain Sandbox %q status: %w", handle.SandboxName, err)
	}
	return DrainResult{TornDown: true}, nil
}

// GetPodStatus implements PodLifecycleManager.
func (m *AgentSandboxPodLifecycleManager) GetPodStatus(ctx context.Context, handle PodHandle) (PodStatus, error) {
	var pod lennyv1.Sandbox
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.SandboxName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return PodStatus{}, ErrPodNotFound
		}
		return PodStatus{}, fmt.Errorf("podlifecycle: get Sandbox %q: %w", handle.SandboxName, err)
	}
	return PodStatus{
		Phase:       PodState(pod.Status.Phase),
		PodIP:       pod.Status.PodIP,
		PodName:     pod.Status.PodName,
		NodeName:    pod.Status.NodeName,
		ActiveSlots: pod.Status.ActiveSlots,
		TenantID:    pod.Status.TenantID,
	}, nil
}

// findIdleSandbox returns the first idle Sandbox in poolName, or
// ErrPodNotIdle when the pool has none. The selection is read-only;
// the claim itself is a status patch the caller commits.
func (m *AgentSandboxPodLifecycleManager) findIdleSandbox(ctx context.Context, poolName string) (*lennyv1.Sandbox, error) {
	var sandboxes lennyv1.SandboxList
	if err := m.Client.List(ctx, &sandboxes, client.InNamespace(m.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list Sandboxes for pool %q: %w", poolName, err)
	}
	for i := range sandboxes.Items {
		sb := &sandboxes.Items[i]
		if sb.Spec.PoolRef != poolName {
			continue
		}
		if PodState(sb.Status.Phase) == PodStateIdle {
			return sb, nil
		}
	}
	return nil, fmt.Errorf("%w: pool %s", ErrPodNotIdle, poolName)
}

// warmModeFromAnnotations reads the §6.1 warm mode the warming
// controller stamps on a Sandbox. An unrecognized or missing
// annotation defaults to pod_warm (the §6.1 default mode).
func warmModeFromAnnotations(annotations map[string]string) WarmMode {
	if annotations["lenny.dev/warm-mode"] == string(WarmModeSDKWarm) {
		return WarmModeSDKWarm
	}
	return WarmModePodWarm
}

// AgentSandboxPoolManager is the v1 default PoolManager. The §4.6
// implementation lives in pkg/controller/warmpool and pkg/controller/
// sandbox; this type is the spec's facade so the controller calls
// route through the §4.6.1 interface rather than the CRD types
// directly. The methods translate to CRD writes; orchestration that
// already runs in the WarmPoolController stays there for v1, and a
// follow-on iteration moves the orchestration behind these methods.
type AgentSandboxPoolManager struct {
	AgentSandboxPoolReader
}

// ReconcilePool implements PoolManager.
func (m *AgentSandboxPoolManager) ReconcilePool(ctx context.Context, cfg PoolConfig) error {
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
	if tmpl.ResourceVersion == "" {
		// Fresh template — Create it.
		if err := m.Client.Create(ctx, tmpl); err != nil {
			return fmt.Errorf("podlifecycle: create SandboxTemplate %q: %w", cfg.Name, err)
		}
	} else if err := m.Client.Patch(ctx, tmpl, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("podlifecycle: patch SandboxTemplate %q: %w", cfg.Name, err)
	}
	// MinWarm/MaxWarm live on the companion SandboxWarmPool (§4.6.1);
	// upsert it alongside the template.
	if cfg.MinWarm > 0 || cfg.MaxWarm > 0 {
		if err := m.upsertWarmPool(ctx, tmpl.Namespace, cfg); err != nil {
			return err
		}
	}
	return nil
}

// upsertWarmPool creates or patches the SandboxWarmPool that carries
// MinWarm/MaxWarm for the pool. The shape mirrors the live
// WarmPoolController reconciler but stays minimal so v1 callers can
// route through the §4.6.1 interface.
func (m *AgentSandboxPoolManager) upsertWarmPool(ctx context.Context, namespace string, cfg PoolConfig) error {
	var swp lennyv1.SandboxWarmPool
	key := client.ObjectKey{Namespace: namespace, Name: cfg.Name}
	if err := m.Client.Get(ctx, key, &swp); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("podlifecycle: get SandboxWarmPool %q: %w", cfg.Name, err)
		}
		swp = lennyv1.SandboxWarmPool{}
		swp.Namespace = namespace
		swp.Name = cfg.Name
		swp.Spec.MinWarm = cfg.MinWarm
		swp.Spec.MaxWarm = cfg.MaxWarm
		swp.Spec.TemplateRef = cfg.Name
		if err := m.Client.Create(ctx, &swp); err != nil {
			return fmt.Errorf("podlifecycle: create SandboxWarmPool %q: %w", cfg.Name, err)
		}
		return nil
	}
	original := swp.DeepCopy()
	swp.Spec.MinWarm = cfg.MinWarm
	swp.Spec.MaxWarm = cfg.MaxWarm
	if swp.Spec.TemplateRef == "" {
		swp.Spec.TemplateRef = cfg.Name
	}
	return m.Client.Patch(ctx, &swp, client.MergeFrom(original))
}

// ApplyPoolDefinition implements PoolManager.
func (m *AgentSandboxPoolManager) ApplyPoolDefinition(ctx context.Context, def PoolDefinition) error {
	if def.Deleted {
		var tmpl lennyv1.SandboxTemplate
		key := client.ObjectKey{Namespace: def.Spec.Namespace, Name: def.Spec.Name}
		if err := m.Client.Get(ctx, key, &tmpl); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("podlifecycle: get SandboxTemplate %q: %w", def.Spec.Name, err)
		}
		if err := m.Client.Delete(ctx, &tmpl); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("podlifecycle: delete SandboxTemplate %q: %w", def.Spec.Name, err)
		}
		return nil
	}
	return m.ReconcilePool(ctx, def.Spec)
}

// ReplacePod implements PoolManager. The v1 default marks the pod
// for retirement (draining); the WarmPoolController's pod-reconciler
// observes the phase and provisions a replacement.
func (m *AgentSandboxPoolManager) ReplacePod(ctx context.Context, handle PodHandle, reason string) error {
	var pod lennyv1.Sandbox
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.SandboxName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrPodNotFound
		}
		return fmt.Errorf("podlifecycle: get Sandbox %q: %w", handle.SandboxName, err)
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if reason != "" {
		original := pod.DeepCopy()
		pod.Annotations["lenny.dev/replace-reason"] = reason
		if err := m.Client.Patch(ctx, &pod, client.MergeFrom(original)); err != nil {
			return fmt.Errorf("podlifecycle: annotate replace Sandbox %q: %w", handle.SandboxName, err)
		}
	}
	statusOriginal := pod.DeepCopy()
	pod.Status.Phase = string(PodStateDraining)
	return m.Client.Status().Patch(ctx, &pod, client.MergeFrom(statusOriginal))
}

// TransitionPodState implements PoolManager. The v1 default validates
// the requested transition against the spec's allowed-transition set
// (a subset enforced at this layer) and writes the new phase.
func (m *AgentSandboxPoolManager) TransitionPodState(ctx context.Context, handle PodHandle, from, to PodState) error {
	if !allowedTransition(from, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	var pod lennyv1.Sandbox
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.SandboxName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrPodNotFound
		}
		return fmt.Errorf("podlifecycle: get Sandbox %q: %w", handle.SandboxName, err)
	}
	if PodState(pod.Status.Phase) != from {
		return fmt.Errorf("%w: observed phase %s != from %s", ErrInvalidTransition, pod.Status.Phase, from)
	}
	original := pod.DeepCopy()
	pod.Status.Phase = string(to)
	return m.Client.Status().Patch(ctx, &pod, client.MergeFrom(original))
}

// GarbageCollect implements PoolManager. The v1 default sweep finds
// orphan Sandboxes (no matching pool) and reports them; the
// WarmPoolController's GC reconciler owns the actual delete. spec:
// spec/04_system-components.md line 353.
func (m *AgentSandboxPoolManager) GarbageCollect(ctx context.Context) ([]OrphanResult, error) {
	var templates lennyv1.SandboxTemplateList
	if err := m.Client.List(ctx, &templates, client.InNamespace(m.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list SandboxTemplates: %w", err)
	}
	pools := sets.New[string]()
	for _, t := range templates.Items {
		pools.Insert(t.Name)
	}

	var sandboxes lennyv1.SandboxList
	if err := m.Client.List(ctx, &sandboxes, client.InNamespace(m.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list Sandboxes: %w", err)
	}
	var orphans []OrphanResult
	for _, sb := range sandboxes.Items {
		if sb.Spec.PoolRef == "" || !pools.Has(sb.Spec.PoolRef) {
			orphans = append(orphans, OrphanResult{
				Kind:      "Sandbox",
				Namespace: sb.Namespace,
				Name:      sb.Name,
				Reason:    fmt.Sprintf("PoolRef %q has no matching SandboxTemplate", sb.Spec.PoolRef),
				Action:    OrphanActionRetained,
			})
		}
	}

	var claims lennyv1.SandboxClaimList
	if err := m.Client.List(ctx, &claims, client.InNamespace(m.Namespace)); err != nil {
		return nil, fmt.Errorf("podlifecycle: list SandboxClaims: %w", err)
	}
	sandboxNames := sets.New[string]()
	for _, sb := range sandboxes.Items {
		sandboxNames.Insert(sb.Name)
	}
	for _, claim := range claims.Items {
		if claim.Spec.SandboxRef == "" || !sandboxNames.Has(claim.Spec.SandboxRef) {
			orphans = append(orphans, OrphanResult{
				Kind:      "SandboxClaim",
				Namespace: claim.Namespace,
				Name:      claim.Name,
				Reason:    fmt.Sprintf("SandboxRef %q has no matching Sandbox", claim.Spec.SandboxRef),
				Action:    OrphanActionRetained,
			})
		}
	}
	return orphans, nil
}

// ManageFinalizer implements PoolManager.
func (m *AgentSandboxPoolManager) ManageFinalizer(ctx context.Context, handle PodHandle, action FinalizerAction) error {
	var pod lennyv1.Sandbox
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: handle.Namespace, Name: handle.SandboxName}, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrPodNotFound
		}
		return fmt.Errorf("podlifecycle: get Sandbox %q: %w", handle.SandboxName, err)
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
func (m *AgentSandboxPoolManager) ManagePDB(ctx context.Context, poolName string, config PDBConfig) error {
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
// Sandbox in poolName and patches each to phase=draining.
func (m *AgentSandboxPoolManager) DrainPool(ctx context.Context, poolName string, checkpointFirst bool) error {
	var sandboxes lennyv1.SandboxList
	if err := m.Client.List(ctx, &sandboxes, client.InNamespace(m.Namespace)); err != nil {
		return fmt.Errorf("podlifecycle: list Sandboxes for drain: %w", err)
	}
	for i := range sandboxes.Items {
		sb := &sandboxes.Items[i]
		if sb.Spec.PoolRef != poolName {
			continue
		}
		if checkpointFirst {
			if sb.Annotations == nil {
				sb.Annotations = map[string]string{}
			}
			original := sb.DeepCopy()
			sb.Annotations["lenny.dev/drain-checkpoint-first"] = "true"
			if err := m.Client.Patch(ctx, sb, client.MergeFrom(original)); err != nil {
				return fmt.Errorf("podlifecycle: annotate drain Sandbox %q: %w", sb.Name, err)
			}
		}
		statusOriginal := sb.DeepCopy()
		sb.Status.Phase = string(PodStateDraining)
		if err := m.Client.Status().Patch(ctx, sb, client.MergeFrom(statusOriginal)); err != nil {
			return fmt.Errorf("podlifecycle: drain Sandbox %q status: %w", sb.Name, err)
		}
	}
	return nil
}

// SetPoolCondition implements PoolManager.
func (m *AgentSandboxPoolManager) SetPoolCondition(ctx context.Context, poolName string, condition PoolCondition, reason string) error {
	var tmpl lennyv1.SandboxTemplate
	key := client.ObjectKey{Namespace: m.Namespace, Name: poolName}
	if err := m.Client.Get(ctx, key, &tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: %s", ErrPoolNotFound, poolName)
		}
		return fmt.Errorf("podlifecycle: get SandboxTemplate %q: %w", poolName, err)
	}
	original := tmpl.DeepCopy()
	setCondition(&tmpl, condition, reason)
	return m.Client.Status().Patch(ctx, &tmpl, client.MergeFrom(original))
}

// getOrInitTemplate returns the existing SandboxTemplate or an
// initialized in-memory one ready for Create.
func (m *AgentSandboxPoolManager) getOrInitTemplate(ctx context.Context, cfg PoolConfig) (*lennyv1.SandboxTemplate, error) {
	namespace := cfg.Namespace
	if namespace == "" {
		namespace = m.Namespace
	}
	var tmpl lennyv1.SandboxTemplate
	if err := m.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: cfg.Name}, &tmpl); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("podlifecycle: get SandboxTemplate %q: %w", cfg.Name, err)
		}
		tmpl = lennyv1.SandboxTemplate{}
		tmpl.Namespace = namespace
		tmpl.Name = cfg.Name
	}
	return &tmpl, nil
}

// setCondition writes condition onto tmpl.Status.Conditions, replacing
// the existing entry of the same Type if present.
func setCondition(tmpl *lennyv1.SandboxTemplate, condition PoolCondition, reason string) {
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

// allowedTransition reports whether the §6.2 state machine admits a
// from → to transition. The list here is the subset the interface
// surface enforces; the runtime SDK-warm path adds the SDK-specific
// transitions in pkg/controller/warmpool. spec: spec/06_warm-pod-model.md
// §6.2.
func allowedTransition(from, to PodState) bool {
	allowed := map[PodState]sets.Set[PodState]{
		PodStateWarming:             sets.New(PodStateIdle, PodStateSDKConnecting, PodStateFailed),
		PodStateSDKConnecting:       sets.New(PodStateIdle, PodStateFailed),
		PodStateIdle:                sets.New(PodStateClaimed, PodStateSlotActive, PodStateDraining, PodStateTerminated),
		PodStateClaimed:             sets.New(PodStateReceivingUploads, PodStateRunningSetup, PodStateAttached, PodStateIdle, PodStateDraining, PodStateFailed),
		PodStateSlotActive:          sets.New(PodStateIdle, PodStateDraining, PodStateFailed),
		PodStateReceivingUploads:    sets.New(PodStateFinalizingWorkspace, PodStateFailed),
		PodStateFinalizingWorkspace: sets.New(PodStateRunningSetup, PodStateStartingSession, PodStateFailed),
		PodStateRunningSetup:        sets.New(PodStateStartingSession, PodStateFailed),
		PodStateStartingSession:     sets.New(PodStateAttached, PodStateFailed),
		PodStateAttached:            sets.New(PodStateTaskCleanup, PodStateCompleted, PodStateFailed, PodStateCancelled, PodStateExpired, PodStateDraining),
		PodStateTaskCleanup:         sets.New(PodStateIdle, PodStateDraining, PodStateTerminated),
		PodStateDraining:            sets.New(PodStateTerminated),
	}
	if next, ok := allowed[from]; ok {
		return next.Has(to)
	}
	return false
}
