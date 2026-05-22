// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// ErrNoMatchingPool reports that no warm pool serves the requested
// runtime and isolation profile.
var ErrNoMatchingPool = errors.New("podsession: no warm pool matches the runtime")

// ErrAmbiguousPool reports that more than one warm pool matches the
// runtime and isolation profile — an operator misconfiguration the
// gateway cannot resolve to a single pool.
var ErrAmbiguousPool = errors.New("podsession: more than one warm pool matches the runtime")

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
		matches = append(matches, PoolMatch{
			Pool:             pool.Name,
			ExecutionMode:    tmpl.Spec.ExecutionMode,
			ConcurrencyStyle: tmpl.Spec.ConcurrencyStyle,
			MaxConcurrent:    tmpl.Spec.MaxConcurrent,
		})
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
