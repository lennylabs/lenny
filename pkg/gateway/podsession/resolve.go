// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"

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
// SandboxTemplate fields copied alongside it, so the start path can
// decide between session-claim and slot-claim without re-reading the
// template.
type PoolMatch struct {
	// Pool is the resolved SandboxWarmPool name.
	Pool string
	// ExecutionMode is the §5.2 pod-reuse mode declared on the
	// template. Empty is treated as `session`.
	ExecutionMode string
	// ConcurrencyStyle is the §5.2 concurrent-mode sub-variant, set
	// only when ExecutionMode is `concurrent`.
	ConcurrencyStyle string
	// MaxConcurrent is the per-pod slot count for concurrent mode.
	MaxConcurrent int32
	// IsolationProfile is the §5.3 profile the pool's pods run under,
	// copied from the SandboxTemplate so the §7.1 sessionIsolationLevel
	// can report the assigned pod's profile.
	IsolationProfile string
	// AllowCrossTenantReuse and MicrovmScrubMode are the §5.2 task-mode
	// TaskPolicy fields that select the §7.1 scrubPolicy variant for a
	// task-mode microvm pool. Both are zero on non-task pools.
	AllowCrossTenantReuse bool
	MicrovmScrubMode      string
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
// fields (executionMode, concurrencyStyle, maxConcurrent) that route
// the bind between session-claim and slot-claim. A pool whose
// templateRef dangles is skipped rather than treated as an error.
// ResolvePool returns ErrNoMatchingPool when none match and
// ErrAmbiguousPool when more than one does.
func ResolvePool(ctx context.Context, reader client.Reader, namespace, runtimeRef, isolationProfile string) (PoolMatch, error) {
	var pools lennyv1.SandboxWarmPoolList
	if err := reader.List(ctx, &pools, client.InNamespace(namespace)); err != nil {
		return PoolMatch{}, fmt.Errorf("podsession: list warm pools: %w", err)
	}

	var matches []PoolMatch
	for i := range pools.Items {
		pool := &pools.Items[i]
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
			ConcurrencyStyle: tmpl.Spec.ConcurrencyStyle,
			MaxConcurrent:    tmpl.Spec.MaxConcurrent,
			IsolationProfile: tmpl.Spec.IsolationProfile,
			PoolWarmingUp:    warmingUp,
			PodsWarming:      podsWarming,
		}
		if tp := tmpl.Spec.TaskPolicy; tp != nil {
			m.AllowCrossTenantReuse = tp.AllowCrossTenantReuse
			m.MicrovmScrubMode = tp.MicrovmScrubMode
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
		return PoolMatch{}, ErrNoMatchingPool
	case 1:
		return matches[0], nil
	default:
		return PoolMatch{}, ErrAmbiguousPool
	}
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
