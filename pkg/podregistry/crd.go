// SPDX-License-Identifier: MIT

package podregistry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
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

	// watchBufferSize bounds each WatchPods event channel. The §12.6
	// line 480 contract permits coalescing or dropping events under
	// backpressure; a full channel is the "fallen behind" condition
	// that triggers the line 482 resync signal. Zero falls back to
	// defaultWatchBufferSize.
	watchBufferSize int
	// pollInterval is the WatchPods polling cadence. Zero falls back to
	// watchTickerInterval.
	pollInterval time.Duration

	// metrics records the §12.6 line 478 per-operation duration and error
	// metrics. nil disables emission (tests and the in-memory dev path).
	metrics *Metrics
}

// defaultWatchBufferSize is the §12.6 WatchPods channel depth. It is
// large enough to absorb a normal reconcile burst without signalling
// resync, yet bounded so a stalled consumer is detected.
const defaultWatchBufferSize = 32

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
	return &CRDPodRegistry{
		Client:          c,
		Namespace:       namespace,
		watchBufferSize: defaultWatchBufferSize,
		pollInterval:    watchTickerInterval,
	}, nil
}

// SetMetrics attaches the §12.6 line 478 observability metrics. The
// gateway wires this after the Prometheus registerer is built; a nil
// Metrics (the default) disables emission.
func (r *CRDPodRegistry) SetMetrics(m *Metrics) { r.metrics = m }

// recordOp emits the §12.6 line 478 operation-duration sample and, on a
// non-nil err, the operation-error count, both labeled by operation and
// pool. A registry with no metrics attached is a no-op.
func (r *CRDPodRegistry) recordOp(operation string, pool PoolID, start time.Time, err error) {
	if r.metrics == nil {
		return
	}
	r.metrics.observe(operation, string(pool), time.Since(start).Seconds())
	if err != nil {
		r.metrics.incError(operation, string(pool))
	}
}

func (r *CRDPodRegistry) GetPod(ctx context.Context, podID PodID) (rec *PodRecord, err error) {
	start := time.Now()
	var pool PoolID
	defer func() { r.recordOp(opGet, pool, start, err) }()
	var sb lennyv1.Sandbox
	if err = r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: string(podID)}, &sb); err != nil {
		if apierrors.IsNotFound(err) {
			err = ErrNotFound
			return nil, err
		}
		err = fmt.Errorf("podregistry: get sandbox %s: %w", podID, err)
		return nil, err
	}
	pool = PoolID(sb.Spec.PoolRef)
	out := toPodRecord(&sb)
	return &out, nil
}

// UpdatePodState writes a §6.2 state transition under optimistic
// CAS. A transition whose From does not match the current
// Sandbox.status.phase returns ErrInvalidTransition. A concurrent
// write that bumps resourceVersion returns ErrResourceConflict so
// the caller can refresh and retry.
func (r *CRDPodRegistry) UpdatePodState(ctx context.Context, podID PodID, transition StateTransition) (err error) {
	start := time.Now()
	var pool PoolID
	defer func() { r.recordOp(opUpdateState, pool, start, err) }()
	var sb lennyv1.Sandbox
	if err = r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: string(podID)}, &sb); err != nil {
		if apierrors.IsNotFound(err) {
			err = ErrNotFound
			return err
		}
		err = fmt.Errorf("podregistry: get sandbox %s: %w", podID, err)
		return err
	}
	pool = PoolID(sb.Spec.PoolRef)
	if transition.From != "" && sb.Status.Phase != transition.From {
		err = fmt.Errorf("%w: current phase %q, transition.From %q",
			ErrInvalidTransition, sb.Status.Phase, transition.From)
		return err
	}
	sb.Status.Phase = transition.To
	if err = r.Client.Status().Update(ctx, &sb); err != nil {
		if apierrors.IsConflict(err) {
			err = ErrResourceConflict
			return err
		}
		err = fmt.Errorf("podregistry: status update %s: %w", podID, err)
		return err
	}
	return nil
}

// ClaimPod acquires an idle pod from the pool under the §4.6.1 per-pod
// occupancy claim model: it lists idle pods and, for each, creates the
// deterministic per-pod SandboxClaim (`claim-<podName>`, spec carries
// sandboxRef and tenantId) and writes its first `bound` binding state.
// The CREATE under the deterministic name is the authoritative
// single-claim guard: an AlreadyExists collision (or a Forbidden from the
// lenny-sandboxclaim-guard webhook) means another writer already bound the
// pod, so the loop moves to the next idle pod. ErrPoolExhausted reports no
// idle pod could be claimed.
//
// The gateway does not write Sandbox.status: the WarmPoolController
// projects the pod's coarse occupancy phase (claimed) from the claim's
// binding state (§4.6.3 ownership decomposition, §4.6.1 occupancy
// projection). The session-to-pod binding lives on the Postgres session
// row's pod_assignment column rather than on the CRD, so the per-pod claim
// carries no session identifier; the returned PodRecord echoes the
// claim's tenant and the requesting session for the caller's attribution
// without persisting either on the Sandbox.
//
// spec: §4.6.1 (per-pod occupancy claim), §4.6.3 (gateway is not a
// Sandbox.status writer), §3.3 (occupancy projection).
func (r *CRDPodRegistry) ClaimPod(ctx context.Context, opts ClaimOpts) (rec *PodRecord, err error) {
	start := time.Now()
	defer func() { r.recordOp(opClaim, opts.PoolID, start, err) }()
	if opts.PoolID == "" {
		err = errors.New("podregistry: ClaimOpts.PoolID is required")
		return nil, err
	}
	if opts.SessionID == "" {
		err = errors.New("podregistry: ClaimOpts.SessionID is required")
		return nil, err
	}
	var pods []lennyv1.Sandbox
	pods, err = r.listSandboxes(ctx, opts.PoolID, PodFilter{State: "idle"})
	if err != nil {
		return nil, err
	}
	for i := range pods {
		sb := &pods[i]
		if cerr := r.claimViaSandboxClaim(ctx, sb, opts.TenantID); cerr != nil {
			if apierrors.IsAlreadyExists(cerr) || apierrors.IsForbidden(cerr) {
				// Another writer already bound this pod; try the next idle pod.
				continue
			}
			err = cerr
			return nil, err
		}
		// Project the claimed occupancy phase onto the returned record. The
		// WarmPoolController writes the actual Sandbox.status.phase from the
		// claim's `bound` binding state (§4.6.1 occupancy projection); the
		// record echoes that projection so the caller sees the post-claim
		// state without a re-read.
		out := toPodRecord(sb)
		out.State = "claimed"
		out.TenantID = opts.TenantID
		out.SessionID = opts.SessionID
		return &out, nil
	}
	err = ErrPoolExhausted
	return nil, err
}

// claimViaSandboxClaim creates the per-pod SandboxClaim for sb and writes
// its first `bound` binding state, stamping the binding-state-transition time
// the §4.6.1 orphan GC keys its live-binding-state reclaim on, matching the
// canonical pkg/gateway/podclaim writer's field set. An AlreadyExists/Forbidden
// CREATE collision is returned as the raw API error so ClaimPod can branch on
// it and advance to the next idle pod. A failure after the CREATE deletes the
// partial claim so a retry is not blocked by a claim with empty status; the
// §4.6.1 orphan GC reclaims any claim left behind by a crash via its
// CREATE-before-status predicate.
func (r *CRDPodRegistry) claimViaSandboxClaim(ctx context.Context, sb *lennyv1.Sandbox, tenantID string) error {
	claim := &lennyv1.SandboxClaim{}
	claim.Namespace = r.Namespace
	claim.Name = podclaim.ClaimName(sb.Name)
	claim.Spec.SandboxRef = sb.Name
	claim.Spec.TenantID = tenantID
	if err := r.Client.Create(ctx, claim); err != nil {
		return err
	}
	claim.Status.Phase = string(claimstate.Bound)
	// spec: §4.6.1 — every binding-state write stamps the transition time so
	// the orphan GC keys its live-binding-state reclaim on the binding
	// transition rather than on the claim's creation time (the start of the
	// whole occupancy episode).
	claim.Status.BindingStateTransitionTime = &metav1.Time{Time: time.Now().UTC()}
	if err := r.Client.Status().Update(ctx, claim); err != nil {
		_ = r.Client.Delete(ctx, claim)
		return fmt.Errorf("podregistry: write bound status for claim %s: %w", claim.Name, err)
	}
	return nil
}

// ReleasePod releases a pod by deleting its deterministic per-pod
// SandboxClaim (`claim-<podName>`); the WarmPoolController returns the pod to
// idle as a level-triggered projection of the claim's absence (§4.6.1
// occupancy projection). The gateway does not write Sandbox.status to roll the
// pod back: the `sandboxes/status` grant is removed under the §4.6.3 ownership
// decomposition, so a Sandbox.status write here would be RBAC-denied. The
// release reason is no longer recorded on Sandbox.status; a terminal
// retirement disposition is written to the claim's binding state on the live
// recycle-coordinator path rather than mapped to a Sandbox phase here. The
// delete is idempotent: a missing claim is a no-op so a double release or a
// claim the orphan GC already reclaimed does not error.
//
// spec: §4.6.1 (occupancy projection on claim DELETE), §4.6.3 (gateway is not
// a Sandbox.status writer; releases via the claim lifecycle).
func (r *CRDPodRegistry) ReleasePod(ctx context.Context, podID PodID, reason ReleaseReason) (err error) {
	start := time.Now()
	defer func() { r.recordOp(opRelease, "", start, err) }()
	_ = reason
	if err = podclaim.DeleteClaim(ctx, r.Client, r.Namespace, string(podID)); err != nil {
		err = fmt.Errorf("podregistry: release %s: %w", podID, err)
		return err
	}
	return nil
}

// ListPodsByPool returns every Sandbox in the pool, optionally
// filtered by state. Results sort by name so a paginated read is
// deterministic.
func (r *CRDPodRegistry) ListPodsByPool(ctx context.Context, poolID PoolID, filter PodFilter) (_ []PodRecord, err error) {
	start := time.Now()
	defer func() { r.recordOp(opList, poolID, start, err) }()
	var pods []lennyv1.Sandbox
	pods, err = r.listSandboxes(ctx, poolID, filter)
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
func (r *CRDPodRegistry) CountByState(ctx context.Context, poolID PoolID) (_ StateCounts, err error) {
	start := time.Now()
	defer func() { r.recordOp(opCount, poolID, start, err) }()
	var pods []lennyv1.Sandbox
	pods, err = r.listSandboxes(ctx, poolID, PodFilter{})
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
func (r *CRDPodRegistry) CreatePod(ctx context.Context, poolID PoolID, spec PodSpec) (_ *PodRecord, err error) {
	start := time.Now()
	defer func() { r.recordOp(opCreate, poolID, start, err) }()
	if poolID == "" {
		err = errors.New("podregistry: poolID is required")
		return nil, err
	}
	name := fmt.Sprintf("%s-%s", poolID, uuid.NewUUID()[:8])
	sb := &lennyv1.Sandbox{}
	sb.Namespace = r.Namespace
	sb.Name = name
	sb.Labels = map[string]string{PoolLabel: string(poolID)}
	sb.Spec.PoolRef = string(poolID)
	// spec: §12.6 line 422 — CreatePod stamps the per-pod PodSpec fields
	// onto the Sandbox so they round-trip through toPodRecord rather than
	// silently falling back to pool-template defaults. RuntimeRef is a
	// required CRD field; stamping it from the PodSpec is what lets the
	// gateway-created-pod path (Tier 4+) express a runtime other than the
	// pool template's.
	sb.Spec.RuntimeRef = spec.RuntimeDefinitionRef
	sb.Spec.IsolationProfile = spec.IsolationProfile
	sb.Spec.ExecutionMode = spec.ExecutionMode
	sb.Spec.ResourceClass = spec.ResourceClass
	if len(spec.WorkspacePlan) > 0 {
		sb.Spec.WorkspacePlan = &apiextensionsv1.JSON{Raw: append([]byte(nil), spec.WorkspacePlan...)}
	}
	if err = r.Client.Create(ctx, sb); err != nil {
		err = fmt.Errorf("podregistry: create sandbox: %w", err)
		return nil, err
	}
	// Initialize status. Status writes go through the subresource so
	// a fresh sandbox shows phase=warming for the lifecycle manager.
	sb.Status.Phase = "warming"
	if err = r.Client.Status().Update(ctx, sb); err != nil {
		err = fmt.Errorf("podregistry: init status %s: %w", name, err)
		return nil, err
	}
	rec := toPodRecord(sb)
	return &rec, nil
}

// DeletePod removes the Sandbox.
func (r *CRDPodRegistry) DeletePod(ctx context.Context, podID PodID) (err error) {
	start := time.Now()
	defer func() { r.recordOp(opDelete, "", start, err) }()
	sb := &lennyv1.Sandbox{}
	sb.Namespace = r.Namespace
	sb.Name = string(podID)
	if err = r.Client.Delete(ctx, sb); err != nil {
		if apierrors.IsNotFound(err) {
			err = ErrNotFound
			return err
		}
		err = fmt.Errorf("podregistry: delete %s: %w", podID, err)
		return err
	}
	return nil
}

// WatchPods returns a stream of PodEvent frames for the pool. The
// v1 implementation uses a per-call polling loop driven by
// ListPodsByPool; the channel closes when ctx is cancelled. A
// future Tier-4 implementation may switch to a controller-runtime
// informer for lower latency.
func (r *CRDPodRegistry) WatchPods(ctx context.Context, poolID PoolID) (_ <-chan PodEvent, err error) {
	start := time.Now()
	defer func() { r.recordOp(opWatch, poolID, start, err) }()
	if poolID == "" {
		err = errors.New("podregistry: poolID is required")
		return nil, err
	}
	bufSize := r.watchBufferSize
	if bufSize <= 0 {
		bufSize = defaultWatchBufferSize
	}
	out := make(chan PodEvent, bufSize)
	go r.watchLoop(ctx, poolID, out)
	return out, nil
}

// watchLoop polls ListPodsByPool and emits PodEvent frames for
// transitions (created, updated, deleted). The goroutine exits when
// ctx is cancelled.
//
// spec: §12.6 line 482 — the channel MUST NOT emit an initial state
// snapshot on creation (consumers read initial state via
// ListPodsByPool), and when the channel falls behind the loop emits a
// synthetic resync frame carrying no PodRecord instead of blocking.
func (r *CRDPodRegistry) watchLoop(ctx context.Context, poolID PoolID, out chan<- PodEvent) {
	defer close(out)
	// Seed the snapshot WITHOUT emitting: the first ListPodsByPool result
	// is the consumer's responsibility, not a stream of created events.
	known := map[PodID]PodRecord{}
	if records, err := r.ListPodsByPool(ctx, poolID, PodFilter{}); err == nil {
		for i := range records {
			known[records[i].PodID] = records[i]
		}
	}
	interval := r.pollInterval
	if interval <= 0 {
		interval = watchTickerInterval
	}
	ticker := newWatchTicker(interval)
	defer ticker.Stop()
	// pendingResync records that the channel has fallen behind: a delta
	// could not be delivered without blocking. While it is set the loop
	// keeps the snapshot current but suppresses per-pod deltas until it
	// can hand the consumer a single resync frame, after which normal
	// delta emission resumes against current truth.
	pendingResync := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
		}
		// cycleStart bounds the §12.6 line 484 watch lag for this poll:
		// the CRD records carry no updated_at, so the lag reported is the
		// time from the poll observing the change to its delivery, which
		// captures list latency and channel backpressure.
		cycleStart := time.Now()
		records, err := r.ListPodsByPool(ctx, poolID, PodFilter{})
		if err != nil {
			continue
		}
		if pendingResync {
			syncKnown(known, records)
			if trySend(out, PodEvent{EventType: EventResync}) {
				pendingResync = false
			}
			continue
		}
		seen := make(map[PodID]bool, len(records))
		for i := range records {
			rec := records[i]
			seen[rec.PodID] = true
			prev, ok := known[rec.PodID]
			known[rec.PodID] = rec
			var ev PodEvent
			switch {
			case !ok:
				ev = PodEvent{PodID: rec.PodID, EventType: EventCreated, PodRecord: rec}
			case prev.State != rec.State || prev.ResourceVersion != rec.ResourceVersion:
				ev = PodEvent{PodID: rec.PodID, EventType: EventUpdated, PodRecord: rec}
			default:
				continue
			}
			if !trySend(out, ev) {
				pendingResync = true
				continue
			}
			if r.metrics != nil {
				r.metrics.observeWatchLag(string(poolID), implCRD, time.Since(cycleStart).Seconds())
			}
		}
		for podID, rec := range known {
			if !seen[podID] {
				delete(known, podID)
				if !trySend(out, PodEvent{PodID: podID, EventType: EventDeleted, PodRecord: rec}) {
					pendingResync = true
				}
			}
		}
	}
}

// syncKnown replaces the known snapshot with the observed record set
// without emitting deltas. The watch loop calls it while a resync is
// pending so that, once the consumer re-reads via ListPodsByPool, the
// next deltas are computed against current truth rather than replaying
// the coalesced changes.
func syncKnown(known map[PodID]PodRecord, records []PodRecord) {
	for k := range known {
		delete(known, k)
	}
	for i := range records {
		known[records[i].PodID] = records[i]
	}
}

// trySend delivers event without blocking. It returns false when the
// channel buffer is full — the §12.6 line 480 backpressure condition
// under which the loop coalesces and signals a resync rather than
// blocking the polling goroutine on a slow consumer.
func trySend(out chan<- PodEvent, event PodEvent) bool {
	select {
	case out <- event:
		return true
	default:
		return false
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

// toPodRecord projects a Sandbox onto a PodRecord. spec: §12.6
// line 421 — every key field, including SessionID (set on claim,
// from status) and ExecutionMode (denormalized onto the Sandbox spec
// from the pool's SandboxTemplate), is carried so a consumer can rely
// on the record for claim attribution and task-vs-session mode.
func toPodRecord(sb *lennyv1.Sandbox) PodRecord {
	return PodRecord{
		PodID:            PodID(sb.Name),
		PoolID:           PoolID(sb.Spec.PoolRef),
		State:            sb.Status.Phase,
		TenantID:         sb.Status.TenantID,
		SessionID:        sb.Status.SessionID,
		IsolationProfile: sb.Spec.IsolationProfile,
		ExecutionMode:    sb.Spec.ExecutionMode,
		ResourceVersion:  sb.ResourceVersion,
		NodeName:         sb.Status.NodeName,
		PodIP:            sb.Status.PodIP,
		PodName:          sb.Status.PodName,
	}
}
