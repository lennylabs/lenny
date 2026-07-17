// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// ErrNoMatchingPool reports that no warm pool serves the requested
// runtime and isolation profile.
var ErrNoMatchingPool = errors.New("podsession: no warm pool matches the runtime")

// ErrAmbiguousPool reports that more than one warm pool matches the
// runtime and isolation profile — an operator misconfiguration the
// gateway cannot resolve to a single pool.
var ErrAmbiguousPool = errors.New("podsession: more than one warm pool matches the runtime")

// ErrPoolNotSatisfiable reports that a client pinned a specific pool (the
// §14.1 CreateSessionRequest.pool selector) that does not exist, is not
// backed by the requested runtime, or whose §5.3 isolation profile does
// not satisfy the requested isolationProfile. It is a deterministic
// client fault: the named pin is unsatisfiable for this (runtime,
// isolationProfile) pair, so the gateway fails closed with
// 400 VALIDATION_ERROR rather than silently falling back to a different
// pool. The §4 pool_tenant_access authorization is enforced separately at
// the gateway layer (which holds the tenant id and the grant store), so a
// pool the client may not pin is a 403 rather than this 400.
// spec: §7.1 (pool selector), §14.1 (CreateSessionRequest.pool).
var ErrPoolNotSatisfiable = errors.New("podsession: the requested pool is not backed by the runtime or is isolation-inconsistent")

// conditionPoolWarmingUp is the §5.2 SandboxTemplate condition type the
// WarmPoolController sets True while a pool with a positive minWarm has
// no idle pods and at least one pod still warming. The gateway reads it
// to answer session creation with a 503 during the bootstrap window.
// spec: §5.2 line 594 ("PoolWarmingUp condition").
const conditionPoolWarmingUp = "PoolWarmingUp"

// PoolWarmingError reports that the resolved pool is in the §5.2
// PoolWarmingUp state: it has no idle pods yet because it is still
// bootstrapping. The session-creation handler maps it to the §5.2
// lines 602-625 `503 RUNTIME_UNAVAILABLE` "Pool Not Ready" response.
// spec: §5.2 lines 602-625.
type PoolWarmingError struct {
	// Pool is the warming pool's name (its SandboxWarmPool name).
	Pool string
	// PodsWarming is the number of pods currently warming toward idle.
	PodsWarming int32
}

func (e *PoolWarmingError) Error() string {
	return fmt.Sprintf("podsession: pool %q is warming up (%d pods warming)", e.Pool, e.PodsWarming)
}

// PoolMatch is a resolved SandboxWarmPool with the dispatch-relevant
// fields copied alongside it, so the start path can decide between the
// session-claim and slot-claim paths without re-reading the template.
//
// The CRD pair carries the execution mode, the §5.3 isolation profile,
// the service-mode per-pod request capacity, and the recycle scrub
// profile. The remaining §5.2 sessionPolicy dispatch fields
// (maxConcurrentSessions, recycle.allowCrossTenantReuse, and the
// concurrent-workspace pod-uptime cap) are gateway-enforced and live on
// the poolstore sessionPolicy mirror; ResolvePool folds them in from the
// PoolPolicyReader when one is wired, keyed by the resolved pool name.
type PoolMatch struct {
	// Pool is the resolved SandboxWarmPool name.
	Pool string
	// ExecutionMode is the §5.2 mode declared on the template: `session`
	// or `service`. Empty is treated as `session`.
	ExecutionMode string
	// MaxConcurrent is the §5.2 service-mode per-pod request capacity. For
	// a service-mode pool it is sourced from the poolstore mirror (the
	// gateway-enforced routing capacity); zero on session-mode pools.
	MaxConcurrent int32
	// MaxConcurrentSessions is the §5.2 sessionPolicy.maxConcurrentSessions
	// bound: the per-pod simultaneous-session count for session mode. A
	// value above 1 routes the bind through the slot-claim path. It is
	// sourced from the poolstore sessionPolicy mirror; the CRD pair does
	// not carry it. Zero (or 1) keeps the session-claim path.
	MaxConcurrentSessions int32
	// IsolationProfile is the §5.3 profile the pool's pods run under,
	// copied from the SandboxTemplate so the §7.1 sessionIsolationLevel
	// can report the assigned pod's profile.
	IsolationProfile string
	// SpiffeBinding is the §4.9 proxy-mode lease-token SPIFFE binding the
	// pool's pods run under, copied from the SandboxTemplate. The
	// session-start credential-delivery gate pairs it with each resolved
	// CredentialPool's effective deliveryMode so the same
	// direct_mode_isolation.Decide the registration and admission layers run
	// also evaluates the delivery mode leasing actually uses. Empty is the
	// SandboxTemplate default. spec: §4.9.
	SpiffeBinding string
	// Recycle reports whether the pool's §5.2 sessionPolicy.recycle block
	// is present, so the §7.1 scrubPolicy derivation knows the pod is
	// reused across sessions. It is set from the SandboxTemplate's
	// sessionPolicy.recycle presence.
	Recycle bool
	// AllowCrossTenantReuse and MicrovmScrubMode select the §7.1 scrubPolicy
	// variant for a cross-tenant-reuse microvm pool. MicrovmScrubMode is set
	// from sessionPolicy.recycle.scrubProfile on the CRD pair;
	// AllowCrossTenantReuse is gateway-enforced and folded in from the
	// poolstore sessionPolicy mirror.
	AllowCrossTenantReuse bool
	MicrovmScrubMode      string
	// CleanupCommands and CleanupTimeoutSeconds are the §5.2 deployer
	// cleanup commands and their aggregate cap, run by the adapter's
	// whole-pod scrub after the credential purge and before the standard
	// scrub steps. They are gateway-enforced and live on the poolstore
	// sessionPolicy mirror (not the CRD pair), so foldPoolPolicy copies
	// them in from the PoolPolicyMirror the way AllowCrossTenantReuse is
	// folded. The recycle-path Shutdown carries them to the adapter so the
	// scrub runs against the pool configuration as it stood at session start.
	// The scrub profile is not carried on the wire; the gateway routes the
	// §5.2 step-7 vm-restart retire on the recycle policy in its own runtime
	// store (C4). Both are zero on a non-recycling pool or a pool with no
	// mirror row. spec: §5.2 (whole-pod scrub, sessionPolicy gateway-enforced
	// subset); §4.6.3.
	CleanupCommands       []string
	CleanupTimeoutSeconds int
	// PoolWarmingUp reflects the §5.2 PoolWarmingUp condition on the
	// pool's SandboxTemplate: true while the pool is bootstrapping with
	// no idle pods. The start path returns a 503 RUNTIME_UNAVAILABLE
	// before attempting a claim when this is set.
	PoolWarmingUp bool
	// PodsWarming is the number of pods currently warming toward idle
	// (warm count minus ready count on the SandboxWarmPool status). It
	// populates the 503 response's details.podsWarming.
	PodsWarming int32
	// WorkspaceSizeLimitBytes is the §4.4 / §10.1 per-pod hard workspace
	// size limit declared on the SandboxTemplate. Zero leaves the cap
	// unset (the kubelet emptyDir guard remains the backstop). The
	// resume path forwards it to the adapter for the §7.3 line 397
	// pre-extraction symmetric size check. F-7.3.26.
	WorkspaceSizeLimitBytes int64
	// MaxPodUptimeSeconds is the §6.2 lines 166-167 concurrent-workspace
	// pod-uptime retirement cap copied from the SandboxTemplate's
	// sessionPolicy.recycle.maxPodUptimeSeconds. Zero leaves uptime retirement off. The
	// slot-claim path drains an over-uptime pod before its next slot
	// assignment. Zero on non-concurrent pools.
	MaxPodUptimeSeconds int64
	// OnPoolExhausted is the §5.2 sessionPolicy.onPoolExhausted disposition
	// folded in from the poolstore mirror: "reject" (or empty, the default)
	// returns WARM_POOL_EXHAUSTED once the claim-path timeout and the
	// Postgres fallback are exhausted; "queue" holds the request in the
	// §4.6.1 per-pool claim queue for up to MaxQueueWaitSeconds after both
	// paths are exhausted. spec: §5.2, §4.6.1 (Pool exhaustion behavior).
	OnPoolExhausted string
	// MaxQueueWaitSeconds is the §5.2 sessionPolicy.maxQueueWaitSeconds
	// queue-wait bound applied when OnPoolExhausted is "queue". Zero falls
	// back to the platform default. spec: §5.2, §4.6.1.
	MaxQueueWaitSeconds int
}

// PoolPolicyMirror is the gateway-enforced subset of a pool's §5.2
// sessionPolicy that does not live on the CRD pair: the per-pod
// simultaneous-session bound, the cross-tenant-reuse gate, the
// service-mode per-pod request capacity, and the concurrent-workspace
// pod-uptime retirement cap. ResolvePool folds it into the PoolMatch so
// the start path's dispatch and the §7.1 scrubPolicy derivation read the
// gateway-side values rather than always-zero CRD fields.
type PoolPolicyMirror struct {
	// MaxConcurrentSessions is sessionPolicy.maxConcurrentSessions; > 1
	// routes the bind through the slot-claim path.
	MaxConcurrentSessions int32
	// MaxConcurrent is the service-mode per-pod request capacity.
	MaxConcurrent int32
	// Recycle is sessionPolicy.recycle.enabled: the §5.2 sequential
	// pod-reuse flag for every scrub profile (standard, vm-restart,
	// in-place). The CRD pair carries the flag only implicitly, for the
	// microvm cross-tenant scrub variants where a non-empty scrubProfile
	// is itself an admission-enforced signal (§4.6.3 ownership); a
	// standard-profile recycling pool sets no CRD field at all, so this
	// gateway-enforced mirror is the only source foldPoolPolicy has for
	// the common case. spec: §5.2 (Recycle lifecycle).
	Recycle bool
	// AllowCrossTenantReuse is sessionPolicy.recycle.allowCrossTenantReuse.
	AllowCrossTenantReuse bool
	// CleanupCommands and CleanupTimeoutSeconds are the §5.2
	// sessionPolicy.cleanupCommands and sessionPolicy.cleanupTimeoutSeconds
	// the adapter's whole-pod scrub runs at the recycle boundary. They are
	// gateway-enforced and live on the poolstore sessionPolicy mirror, not
	// the CRD pair. spec: §5.2 (whole-pod scrub trigger).
	CleanupCommands       []string
	CleanupTimeoutSeconds int
	// MaxPodUptimeSeconds is sessionPolicy.recycle.maxPodUptimeSeconds, the
	// §6.2 concurrent-workspace pod-uptime retirement cap.
	MaxPodUptimeSeconds int64
	// OnPoolExhausted is sessionPolicy.onPoolExhausted ("reject" | "queue");
	// empty defaults to "reject". spec: §5.2 (Pool exhaustion behavior).
	OnPoolExhausted string
	// MaxQueueWaitSeconds is sessionPolicy.maxQueueWaitSeconds, the §5.2
	// queue-wait bound when OnPoolExhausted is "queue". spec: §5.2.
	MaxQueueWaitSeconds int
}

// PoolPolicyReader reads a pool's gateway-enforced §5.2 sessionPolicy
// mirror by pool name. It is defined at the consumer (ResolvePool) so the
// poolstore is injected rather than imported here, keeping the CRD
// resolver free of a storage dependency. found is false when the pool has
// no mirror row (the Postgres-only posture or a CRD-only pool), in which
// case the dispatch fields keep their CRD-derived defaults.
type PoolPolicyReader interface {
	PoolPolicy(ctx context.Context, name string) (mirror PoolPolicyMirror, found bool, err error)
}

// poolWarming derives the §5.2 PoolWarmingUp signal and the warming-pod
// count for a resolved (template, pool) pair. PodsWarming is the warm
// count minus the ready count, clamped at zero.
func poolWarming(tmpl *lennyv1.SandboxTemplate, pool *lennyv1.SandboxWarmPool) (warmingUp bool, podsWarming int32) {
	warmingUp = meta.IsStatusConditionTrue(tmpl.Status.Conditions, conditionPoolWarmingUp)
	podsWarming = pool.Status.WarmCount - pool.Status.ReadyCount
	if podsWarming < 0 {
		podsWarming = 0
	}
	return warmingUp, podsWarming
}

// ResolvePool finds the SandboxWarmPool whose SandboxTemplate serves
// the given runtime — and, when isolationProfile is non-empty, the
// given §5.3 isolation profile. The gateway calls it to choose the
// pool a session's pod is claimed from, and to read the dispatch
// fields (executionMode, maxConcurrentSessions, maxConcurrent) that
// route the bind between the session-claim and slot-claim paths. A
// pool whose templateRef dangles is skipped rather than treated as an
// error. ResolvePool returns ErrNoMatchingPool when none match and
// ErrAmbiguousPool when more than one does.
//
// When pool is non-empty the client pinned a specific pool (the §14.1
// CreateSessionRequest.pool selector): resolution is constrained to that
// named pool, which still must be backed by runtimeRef and satisfy
// isolationProfile (mirroring the §15.1 replay targetPool rule "Must be a
// pool backed by targetRuntime"). A pinned pool that is absent, not
// backed by the runtime, or isolation-inconsistent returns
// ErrPoolNotSatisfiable so the gateway fails closed with 400 rather than
// scheduling on a different pool. The §4 pool_tenant_access authorization
// for a pinned pool is enforced at the gateway layer (sessionserver),
// which holds the tenant id and the grant store. spec: §7.1 (pool
// selector), §14.1 (CreateSessionRequest.pool).
//
// policy may be nil. When wired, ResolvePool folds the matched pool's
// gateway-enforced §5.2 sessionPolicy mirror (maxConcurrentSessions,
// the service-mode maxConcurrent, allowCrossTenantReuse, and the
// concurrent-workspace pod-uptime cap) into the PoolMatch, keyed by the
// resolved pool name. spec: §5.2 (sessionPolicy block, gateway-enforced
// subset).
func ResolvePool(ctx context.Context, reader client.Reader, policy PoolPolicyReader, namespace, runtimeRef, isolationProfile, pinnedPool string) (PoolMatch, error) {
	var pools lennyv1.SandboxWarmPoolList
	if err := reader.List(ctx, &pools, client.InNamespace(namespace)); err != nil {
		return PoolMatch{}, fmt.Errorf("podsession: list warm pools: %w", err)
	}

	var matches []PoolMatch
	for i := range pools.Items {
		pool := &pools.Items[i]
		// spec: §7.1 / §14.1 — a client-pinned pool constrains resolution to
		// that one named pool. A non-matching name is skipped; the resolved
		// set is empty unless the named pool also passes the runtime and
		// isolation requirements below, which surface as ErrPoolNotSatisfiable.
		if pinnedPool != "" && pool.Name != pinnedPool {
			continue
		}
		var tmpl lennyv1.SandboxTemplate
		err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: pool.Spec.TemplateRef}, &tmpl)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return PoolMatch{}, fmt.Errorf("podsession: get template %s: %w", pool.Spec.TemplateRef, err)
		}
		if tmpl.Spec.RuntimeRef != runtimeRef {
			continue
		}
		if isolationProfile != "" && tmpl.Spec.IsolationProfile != isolationProfile {
			continue
		}
		warmingUp, podsWarming := poolWarming(&tmpl, pool)
		m := PoolMatch{
			Pool:             pool.Name,
			ExecutionMode:    tmpl.Spec.ExecutionMode,
			MaxConcurrent:    tmpl.Spec.MaxConcurrent,
			IsolationProfile: tmpl.Spec.IsolationProfile,
			SpiffeBinding:    tmpl.Spec.SpiffeBinding,
			PoolWarmingUp:    warmingUp,
			PodsWarming:      podsWarming,
		}
		// The CRD carries the cross-tenant microvm scrub control on the
		// sessionPolicy.recycle block (§4.6.3 ownership); the scrub profile
		// selects the §7.1 scrubPolicy variant. The remaining gateway-enforced
		// dispatch fields (maxConcurrentSessions, allowCrossTenantReuse, the
		// concurrent-workspace pod-uptime cap) live on the poolstore
		// sessionPolicy mirror and are folded in from policy below.
		if sp := tmpl.Spec.SessionPolicy; sp != nil && sp.Recycle != nil {
			m.Recycle = true
			m.MicrovmScrubMode = sp.Recycle.ScrubProfile
		}
		// spec: §4.4 line 254 / §10.1 line 122 — copy the per-pod hard
		// workspace size cap so the resume path can pass it to the adapter
		// for the §7.3 line 397 symmetric pre-extraction check. F-7.3.26.
		if tmpl.Spec.WorkspaceSizeLimitBytes != nil {
			m.WorkspaceSizeLimitBytes = *tmpl.Spec.WorkspaceSizeLimitBytes
		}
		matches = append(matches, m)
	}

	switch len(matches) {
	case 0:
		// spec: §7.1 / §14.1 — a client pinned a pool that does not exist,
		// is not backed by the runtime, or is isolation-inconsistent. That is
		// a deterministic client fault (400) distinct from the operator-side
		// "no pool serves this runtime" condition (ErrNoMatchingPool), so the
		// pinned-pool case returns ErrPoolNotSatisfiable.
		if pinnedPool != "" {
			return PoolMatch{}, ErrPoolNotSatisfiable
		}
		return PoolMatch{}, ErrNoMatchingPool
	case 1:
		m := matches[0]
		if err := foldPoolPolicy(ctx, policy, &m); err != nil {
			return PoolMatch{}, err
		}
		return m, nil
	default:
		return PoolMatch{}, ErrAmbiguousPool
	}
}

// foldPoolPolicy folds the resolved pool's gateway-enforced §5.2
// sessionPolicy mirror into m. It is a no-op when no reader is wired or
// the pool has no mirror row, leaving the CRD-derived dispatch fields
// unchanged. spec: §5.2 (sessionPolicy block, gateway-enforced subset).
func foldPoolPolicy(ctx context.Context, policy PoolPolicyReader, m *PoolMatch) error {
	if policy == nil {
		return nil
	}
	mirror, found, err := policy.PoolPolicy(ctx, m.Pool)
	if err != nil {
		return fmt.Errorf("podsession: read pool policy mirror for %s: %w", m.Pool, err)
	}
	if !found {
		return nil
	}
	m.MaxConcurrentSessions = mirror.MaxConcurrentSessions
	// spec: §5.2 (Recycle lifecycle) — the CRD only sets SessionPolicy.
	// Recycle for the microvm cross-tenant scrub variants (a non-empty
	// scrubProfile); a standard-profile recycling pool carries no CRD
	// signal at all. OR in the mirror's Recycle rather than overwrite, so
	// a CRD-derived true (the microvm case) is never downgraded by a
	// mirror row that happens to omit the flag.
	m.Recycle = m.Recycle || mirror.Recycle
	m.AllowCrossTenantReuse = mirror.AllowCrossTenantReuse
	// §5.2 whole-pod scrub trigger: the deployer cleanup commands and their
	// aggregate cap are gateway-enforced and live on the mirror, so fold them
	// onto PoolMatch beside AllowCrossTenantReuse. The recycle-path Shutdown
	// carries them to the adapter (C-A3); a pool with no mirror row leaves
	// them empty.
	m.CleanupCommands = mirror.CleanupCommands
	m.CleanupTimeoutSeconds = mirror.CleanupTimeoutSeconds
	m.MaxPodUptimeSeconds = mirror.MaxPodUptimeSeconds
	// §5.2 / §4.6.1 pool-exhaustion disposition: the queue-vs-reject choice
	// and its wait bound are gateway-enforced and live on the mirror, not the
	// CRD pair. A pool with no mirror row keeps the "reject" default.
	m.OnPoolExhausted = mirror.OnPoolExhausted
	m.MaxQueueWaitSeconds = mirror.MaxQueueWaitSeconds
	// The service-mode per-pod request capacity is gateway-enforced; the
	// mirror is authoritative. A session-mode pool leaves it zero.
	if mirror.MaxConcurrent > 0 {
		m.MaxConcurrent = mirror.MaxConcurrent
	}
	return nil
}

// PoolStatusLookup reads the live §5.2 bootstrap status of a pool from
// its Kubernetes CRD pair. The §15.1 admin pool GET handler consults it
// to surface `poolCondition` and `idlePodCount` (§5.2 line 629) without
// requiring operators to inspect the CR status directly. The pool's
// SandboxWarmPool and SandboxTemplate share the pool name (the
// PoolScalingController names both after the pool, §4.6.2).
type PoolStatusLookup struct {
	// Reader addresses the cluster.
	Reader client.Reader
	// Namespace is the agent namespace the pool's CRDs live in.
	Namespace string
}

// PoolStatus returns the pool's §5.2 condition and idle-pod count. The
// condition is "PoolWarmingUp" while the pool's SandboxTemplate carries
// a true PoolWarmingUp condition, and the empty string otherwise.
// idlePodCount is the SandboxWarmPool's ready-pod count. found is false
// when the pool has no SandboxWarmPool yet (defined in Postgres but not
// reconciled into a CRD), so the caller omits the live-status fields
// rather than reporting a misleading zero.
// spec: §5.2 line 629 ("Operator visibility").
func (l PoolStatusLookup) PoolStatus(ctx context.Context, poolName string) (condition string, idlePodCount int, found bool, err error) {
	var pool lennyv1.SandboxWarmPool
	if e := l.Reader.Get(ctx, client.ObjectKey{Namespace: l.Namespace, Name: poolName}, &pool); e != nil {
		if apierrors.IsNotFound(e) {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("podsession: get warm pool %s: %w", poolName, e)
	}
	idlePodCount = int(pool.Status.ReadyCount)

	var tmpl lennyv1.SandboxTemplate
	templateRef := pool.Spec.TemplateRef
	if templateRef == "" {
		templateRef = poolName
	}
	if e := l.Reader.Get(ctx, client.ObjectKey{Namespace: l.Namespace, Name: templateRef}, &tmpl); e != nil {
		if apierrors.IsNotFound(e) {
			// Pool exists but its template does not resolve; report the
			// idle count without a condition rather than failing the GET.
			return "", idlePodCount, true, nil
		}
		return "", 0, false, fmt.Errorf("podsession: get template %s: %w", templateRef, e)
	}
	if meta.IsStatusConditionTrue(tmpl.Status.Conditions, conditionPoolWarmingUp) {
		condition = conditionPoolWarmingUp
	}
	return condition, idlePodCount, true, nil
}

// PoolBootstrapStatus returns the pool's §17.8.2 cold-start signals read
// from its SandboxWarmPool CRD status: the accumulated whole hours of
// traffic data and the scaling mode the PoolScalingController operates
// it in (`bootstrap` or `formula`). found is false when the pool has no
// SandboxWarmPool yet, so the admin GET reports the override-only view.
// spec: §17.8.2 step 3.
func (l PoolStatusLookup) PoolBootstrapStatus(ctx context.Context, poolName string) (hoursOfData float64, scalingMode string, found bool, err error) {
	var pool lennyv1.SandboxWarmPool
	if e := l.Reader.Get(ctx, client.ObjectKey{Namespace: l.Namespace, Name: poolName}, &pool); e != nil {
		if apierrors.IsNotFound(e) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("podsession: get warm pool %s: %w", poolName, e)
	}
	return float64(pool.Status.BootstrapHoursOfData), pool.Status.ScalingMode, true, nil
}

// CRDGeneration returns the §4.6.2 line 558 pool_config_generation the
// PoolScalingController stamped on the pool's SandboxTemplate annotation,
// the line-560 last-reconciled instant, and ok = true when the
// SandboxTemplate exists. It satisfies the admin
// CRDGenerationReader so the §15.1 GET /v1/admin/pools/{name}/sync-status
// endpoint and the PUT response can report crdGeneration / lastReconciledAt
// / lagSeconds / inSync outside the Postgres-only dev posture. ok = false
// when no SandboxTemplate has been created for the pool yet (defined in
// Postgres but not reconciled into a CRD), so the handler reports the
// pending state. The PoolScalingController names both CRDs after the pool
// (§4.6.2), so the SandboxTemplate is looked up by the pool name.
// spec: spec/04_system-components.md lines 558-560.
func (l PoolStatusLookup) CRDGeneration(ctx context.Context, poolName string) (generation int64, lastReconciledAt time.Time, ok bool, err error) {
	var tmpl lennyv1.SandboxTemplate
	if e := l.Reader.Get(ctx, client.ObjectKey{Namespace: l.Namespace, Name: poolName}, &tmpl); e != nil {
		if apierrors.IsNotFound(e) {
			return 0, time.Time{}, false, nil
		}
		return 0, time.Time{}, false, fmt.Errorf("podsession: get template %s: %w", poolName, e)
	}
	// An annotation the controller never stamped (a CRD applied by hand,
	// or a generation it has not yet observed) leaves generation 0; the
	// handler then reports inSync=false against the Postgres counter,
	// which is the correct "not reconciled to the latest" signal.
	if raw := tmpl.Annotations[lennyv1.AnnotationConfigGeneration]; raw != "" {
		if g, perr := strconv.ParseInt(raw, 10, 64); perr == nil {
			generation = g
		}
	}
	if raw := tmpl.Annotations[lennyv1.AnnotationLastReconciledAt]; raw != "" {
		if t, perr := time.Parse(time.RFC3339Nano, raw); perr == nil {
			lastReconciledAt = t
		}
	}
	return generation, lastReconciledAt, true, nil
}
