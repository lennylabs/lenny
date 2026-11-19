// SPDX-License-Identifier: MIT

// Package poolstatus reads a pool's live status from its PodSet: the
// bootstrap condition, the idle-pod count, the cold-start signals, and the
// configuration generation the pool scaling controller stamps. The API
// front's admin pool handlers read it, so it is a domain package outside
// the gateway rather than a part of the session core.
package poolstatus

import (
	"context"
	"fmt"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// ConditionPoolWarmingUp is the PodSet condition type the warm pool
// controller sets True while a pool with a positive minWarm has no idle
// pods and at least one pod still warming.
const ConditionPoolWarmingUp = "PoolWarmingUp"

// Lookup reads the live §5.2 bootstrap status of a pool from
// its Kubernetes PodSet. The §15.1 admin pool GET handler consults it
// to surface `poolCondition` and `idlePodCount` (§5.2) without
// requiring operators to inspect the CR status directly. The pool's
// PodSet share the pool name (the
// PoolScalingController names both after the pool, §4.6.2).
type Lookup struct {
	// Reader addresses the cluster.
	Reader client.Reader
	// Namespace is the agent namespace the pool's CRDs live in.
	Namespace string
}

// PoolStatus returns the pool's §5.2 condition and idle-pod count. The
// condition is "PoolWarmingUp" while the pool's PodSet carries
// a true PoolWarmingUp condition, and the empty string otherwise.
// idlePodCount is the PodSet's ready-pod count. found is false
// when the pool has no PodSet yet (defined in Postgres but not
// reconciled into a CRD), so the caller omits the live-status fields
// rather than reporting a misleading zero.
// spec: §5.2.
func (l Lookup) PoolStatus(ctx context.Context, poolName string) (condition string, idlePodCount int, found bool, err error) {
	var pool lennyv1.PodSet
	if e := l.Reader.Get(ctx, client.ObjectKey{Namespace: l.Namespace, Name: poolName}, &pool); e != nil {
		if apierrors.IsNotFound(e) {
			return "", 0, false, nil
		}
		return "", 0, false, fmt.Errorf("poolstatus: get warm pool %s: %w", poolName, e)
	}
	idlePodCount = int(pool.Status.ReadyCount)
	if meta.IsStatusConditionTrue(pool.Status.Conditions, ConditionPoolWarmingUp) {
		condition = ConditionPoolWarmingUp
	}
	return condition, idlePodCount, true, nil
}

// PoolBootstrapStatus returns the pool's §17.8.2 cold-start signals read
// from its PodSet CRD status: the accumulated whole hours of
// traffic data and the scaling mode the PoolScalingController operates
// it in (`bootstrap` or `formula`). found is false when the pool has no
// PodSet yet, so the admin GET reports the override-only view.
// spec: §17.8.2 step 3.
func (l Lookup) PoolBootstrapStatus(ctx context.Context, poolName string) (hoursOfData float64, scalingMode string, found bool, err error) {
	var pool lennyv1.PodSet
	if e := l.Reader.Get(ctx, client.ObjectKey{Namespace: l.Namespace, Name: poolName}, &pool); e != nil {
		if apierrors.IsNotFound(e) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("poolstatus: get warm pool %s: %w", poolName, e)
	}
	return float64(pool.Status.BootstrapHoursOfData), pool.Status.ScalingMode, true, nil
}

// CRDGeneration returns the §4.6.2 pool_config_generation the
// PoolScalingController stamped on the pool's PodSet annotation,
// the line-560 last-reconciled instant, and ok = true when the
// PodSet exists. It satisfies the admin
// CRDGenerationReader so the §15.1 GET /v1/admin/pools/{name}/sync-status
// endpoint and the PUT response can report crdGeneration / lastReconciledAt
// / lagSeconds / inSync outside the Postgres-only dev posture. ok = false
// when no PodSet has been created for the pool yet (defined in
// Postgres but not reconciled into a CRD), so the handler reports the
// pending state. The PoolScalingController names both CRDs after the pool
// (§4.6.2), so the PodSet is looked up by the pool name.
// spec: §4.6.2.
func (l Lookup) CRDGeneration(ctx context.Context, poolName string) (generation int64, lastReconciledAt time.Time, ok bool, err error) {
	var tmpl lennyv1.PodSet
	if e := l.Reader.Get(ctx, client.ObjectKey{Namespace: l.Namespace, Name: poolName}, &tmpl); e != nil {
		if apierrors.IsNotFound(e) {
			return 0, time.Time{}, false, nil
		}
		return 0, time.Time{}, false, fmt.Errorf("poolstatus: get template %s: %w", poolName, e)
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
