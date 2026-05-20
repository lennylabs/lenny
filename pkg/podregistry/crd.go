// SPDX-License-Identifier: MIT

package podregistry

import (
	"context"
	"errors"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// CRDPodRegistry is the §12.6 v1 PodRegistry implementation: it
// reads and writes Sandbox CRD status via the Kubernetes API. The
// optimistic-locking CAS that the §4.6.1 lifecycle manager runs
// against UpdatePodState uses Sandbox.metadata.resourceVersion.
//
// Namespace is the agent namespace the Sandbox CRs live in. The
// production install renders this from the `agent` namespace value
// in the Helm chart; tests pass an envtest-scoped namespace.
type CRDPodRegistry struct {
	Client    client.Client
	Namespace string
}

// PoolLabel is the §4.6 label every Sandbox carries so the registry
// can filter by pool without parsing spec.poolRef on every read.
// The §4.6.1 controller writes it alongside the spec field at
// creation time.
const PoolLabel = "lenny.dev/pool"

var _ PodRegistry = (*CRDPodRegistry)(nil)

// New returns a CRDPodRegistry backed by client over namespace.
func New(c client.Client, namespace string) (*CRDPodRegistry, error) {
	if c == nil {
		return nil, errors.New("podregistry: nil client")
	}
	if namespace == "" {
		return nil, errors.New("podregistry: namespace is required")
	}
	return &CRDPodRegistry{Client: c, Namespace: namespace}, nil
}

func (r *CRDPodRegistry) GetPod(ctx context.Context, podID PodID) (*PodRecord, error) {
	var sb lennyv1.Sandbox
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: string(podID)}, &sb); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("podregistry: get sandbox %s: %w", podID, err)
	}
	rec := toPodRecord(&sb)
	return &rec, nil
}

// UpdatePodState writes a §6.2 state transition under optimistic
// CAS. A transition whose From does not match the current
// Sandbox.status.phase returns ErrInvalidTransition. A concurrent
// write that bumps resourceVersion returns ErrResourceConflict so
// the caller can refresh and retry.
func (r *CRDPodRegistry) UpdatePodState(ctx context.Context, podID PodID, transition StateTransition) error {
	var sb lennyv1.Sandbox
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: string(podID)}, &sb); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("podregistry: get sandbox %s: %w", podID, err)
	}
	if transition.From != "" && sb.Status.Phase != transition.From {
		return fmt.Errorf("%w: current phase %q, transition.From %q",
			ErrInvalidTransition, sb.Status.Phase, transition.From)
	}
	sb.Status.Phase = transition.To
	if err := r.Client.Status().Update(ctx, &sb); err != nil {
		if apierrors.IsConflict(err) {
			return ErrResourceConflict
		}
		return fmt.Errorf("podregistry: status update %s: %w", podID, err)
	}
	return nil
}

// ClaimPod runs the §4.6.1 SELECT ... FOR UPDATE SKIP LOCKED
// equivalent against the Kubernetes API: it lists idle pods in the
// pool, selects the first, transitions it to "claimed" under CAS,
// and returns the claimed record. ErrPoolExhausted reports no idle
// pod was found.
func (r *CRDPodRegistry) ClaimPod(ctx context.Context, opts ClaimOpts) (*PodRecord, error) {
	if opts.PoolID == "" {
		return nil, errors.New("podregistry: ClaimOpts.PoolID is required")
	}
	if opts.SessionID == "" {
		return nil, errors.New("podregistry: ClaimOpts.SessionID is required")
	}
	pods, err := r.listSandboxes(ctx, opts.PoolID, PodFilter{State: "idle"})
	if err != nil {
		return nil, err
	}
	for i := range pods {
		sb := &pods[i]
		sb.Status.Phase = "claimed"
		sb.Status.TenantID = opts.TenantID
		if err := r.Client.Status().Update(ctx, sb); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return nil, fmt.Errorf("podregistry: claim %s: %w", sb.Name, err)
		}
		rec := toPodRecord(sb)
		return &rec, nil
	}
	return nil, ErrPoolExhausted
}

// ReleasePod transitions a pod out of an active phase and clears
// its tenant binding. The reason is recorded as a condition on the
// status subresource so the §4.6.1 lifecycle manager and the
// operator-facing inspect path can see why the pod returned to its
// pool.
func (r *CRDPodRegistry) ReleasePod(ctx context.Context, podID PodID, reason ReleaseReason) error {
	var sb lennyv1.Sandbox
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: string(podID)}, &sb); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("podregistry: get %s: %w", podID, err)
	}
	switch reason {
	case ReleaseCompleted:
		sb.Status.Phase = "task_cleanup"
	case ReleaseFailed:
		sb.Status.Phase = "failed"
	case ReleaseCancelled:
		sb.Status.Phase = "cancelled"
	default:
		sb.Status.Phase = "task_cleanup"
	}
	sb.Status.TenantID = ""
	if err := r.Client.Status().Update(ctx, &sb); err != nil {
		if apierrors.IsConflict(err) {
			return ErrResourceConflict
		}
		return fmt.Errorf("podregistry: release %s: %w", podID, err)
	}
	return nil
}

// ListPodsByPool returns every Sandbox in the pool, optionally
// filtered by state. Results sort by name so a paginated read is
// deterministic.
func (r *CRDPodRegistry) ListPodsByPool(ctx context.Context, poolID PoolID, filter PodFilter) ([]PodRecord, error) {
	pods, err := r.listSandboxes(ctx, poolID, filter)
	if err != nil {
		return nil, err
	}
	out := make([]PodRecord, len(pods))
	for i := range pods {
		out[i] = toPodRecord(&pods[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PodID < out[j].PodID })
	return out, nil
}

// CountByState returns the §6.2 state histogram for a pool. The
// §4.6.2 PoolScalingController consumes it to derive scaling
// decisions.
func (r *CRDPodRegistry) CountByState(ctx context.Context, poolID PoolID) (StateCounts, error) {
	pods, err := r.listSandboxes(ctx, poolID, PodFilter{})
	if err != nil {
		return nil, err
	}
	counts := StateCounts{}
	for i := range pods {
		counts[pods[i].Status.Phase]++
	}
	return counts, nil
}

// CreatePod creates a new Sandbox in the pool with the supplied
// spec. The §4.6 controller-runtime sets up the Pod afterward; this
// path only writes the CR.
func (r *CRDPodRegistry) CreatePod(ctx context.Context, poolID PoolID, spec PodSpec) (*PodRecord, error) {
	if poolID == "" {
		return nil, errors.New("podregistry: poolID is required")
	}
	name := fmt.Sprintf("%s-%s", poolID, uuid.NewUUID()[:8])
	sb := &lennyv1.Sandbox{}
	sb.Namespace = r.Namespace
	sb.Name = name
	sb.Labels = map[string]string{PoolLabel: string(poolID)}
	sb.Spec.PoolRef = string(poolID)
	if err := r.Client.Create(ctx, sb); err != nil {
		return nil, fmt.Errorf("podregistry: create sandbox: %w", err)
	}
	// Initialize status. Status writes go through the subresource so
	// a fresh sandbox shows phase=warming for the lifecycle manager.
	sb.Status.Phase = "warming"
	if err := r.Client.Status().Update(ctx, sb); err != nil {
		return nil, fmt.Errorf("podregistry: init status %s: %w", name, err)
	}
	rec := toPodRecord(sb)
	return &rec, nil
}

// DeletePod removes the Sandbox.
func (r *CRDPodRegistry) DeletePod(ctx context.Context, podID PodID) error {
	sb := &lennyv1.Sandbox{}
	sb.Namespace = r.Namespace
	sb.Name = string(podID)
	if err := r.Client.Delete(ctx, sb); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("podregistry: delete %s: %w", podID, err)
	}
	return nil
}

// WatchPods returns a stream of PodEvent frames for the pool. The
// v1 implementation uses a per-call polling loop driven by
// ListPodsByPool; the channel closes when ctx is cancelled. A
// future Tier-4 implementation may switch to a controller-runtime
// informer for lower latency.
func (r *CRDPodRegistry) WatchPods(ctx context.Context, poolID PoolID) (<-chan PodEvent, error) {
	if poolID == "" {
		return nil, errors.New("podregistry: poolID is required")
	}
	out := make(chan PodEvent, 32)
	go r.watchLoop(ctx, poolID, out)
	return out, nil
}

// watchLoop polls ListPodsByPool and emits PodEvent frames for
// transitions (created, updated, deleted). The polling interval is
// short to keep event latency bounded; the goroutine exits when
// ctx is cancelled.
func (r *CRDPodRegistry) watchLoop(ctx context.Context, poolID PoolID, out chan<- PodEvent) {
	defer close(out)
	known := map[PodID]PodRecord{}
	ticker := newWatchTicker()
	defer ticker.Stop()
	for {
		// First-pass: seed the snapshot before sleeping.
		records, err := r.ListPodsByPool(ctx, poolID, PodFilter{})
		if err == nil {
			seen := map[PodID]bool{}
			for _, rec := range records {
				seen[rec.PodID] = true
				prev, ok := known[rec.PodID]
				switch {
				case !ok:
					emit(ctx, out, PodEvent{PodID: rec.PodID, EventType: EventCreated, PodRecord: rec})
				case prev.State != rec.State || prev.ResourceVersion != rec.ResourceVersion:
					emit(ctx, out, PodEvent{PodID: rec.PodID, EventType: EventUpdated, PodRecord: rec})
				}
				known[rec.PodID] = rec
			}
			for podID, rec := range known {
				if !seen[podID] {
					emit(ctx, out, PodEvent{PodID: podID, EventType: EventDeleted, PodRecord: rec})
					delete(known, podID)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
		}
	}
}

func emit(ctx context.Context, out chan<- PodEvent, event PodEvent) {
	select {
	case out <- event:
	case <-ctx.Done():
	}
}

// listSandboxes is the shared list path: it filters by the
// PoolLabel and applies a phase filter if one is set.
func (r *CRDPodRegistry) listSandboxes(ctx context.Context, poolID PoolID, filter PodFilter) ([]lennyv1.Sandbox, error) {
	selector := labels.SelectorFromSet(labels.Set{PoolLabel: string(poolID)})
	var list lennyv1.SandboxList
	if err := r.Client.List(ctx, &list,
		client.InNamespace(r.Namespace),
		client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("podregistry: list sandboxes for pool %s: %w", poolID, err)
	}
	if filter.State == "" {
		return list.Items, nil
	}
	out := make([]lennyv1.Sandbox, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].Status.Phase == filter.State {
			out = append(out, list.Items[i])
		}
	}
	return out, nil
}

// toPodRecord projects a Sandbox onto a PodRecord. ExecutionMode
// is not carried on the Sandbox CRD spec today (it lives on the
// SandboxTemplate the pool resolves through); the field is left
// empty here and a future tier-4 PostgresPodRegistry impl reads
// it from agent_pod_state.
func toPodRecord(sb *lennyv1.Sandbox) PodRecord {
	return PodRecord{
		PodID:            PodID(sb.Name),
		PoolID:           PoolID(sb.Spec.PoolRef),
		State:            sb.Status.Phase,
		TenantID:         sb.Status.TenantID,
		IsolationProfile: sb.Spec.IsolationProfile,
		ResourceVersion:  sb.ResourceVersion,
		NodeName:         sb.Status.NodeName,
		PodIP:            sb.Status.PodIP,
		PodName:          sb.Status.PodName,
		ActiveSlots:      sb.Status.ActiveSlots,
	}
}
