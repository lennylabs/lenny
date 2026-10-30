// SPDX-License-Identifier: MIT

// Package ssaretry holds the single shared implementation of the §4.6.3
// Server-Side Apply conflict retry policy for the pod-lifecycle
// controllers. RetryOnConflictSSA re-runs an apply closure on HTTP 409
// conflicts with bounded jittered backoff, and on five consecutive
// no-progress conflicts against a CRD field owned by another field
// manager it emits the §4.6.3 stuck signal (one crd_ssa_conflict_stuck
// structured log event and one lenny_crd_ssa_conflict_total increment)
// before returning the conflict error so controller-runtime re-enqueues
// the reconcile.
package ssaretry

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// crdSSAConflictTotal is the §16.1 lenny_crd_ssa_conflict_total counter
// the §16.5 CRDSSAConflictStuck alert evaluates against. Per §4.6.3 it
// increments once per five-consecutive-409 stuck episode, labeled by crd
// and controller. Per-resource identity is on the crd_ssa_conflict_stuck
// log, not on this counter (§16.1.1 forbids per-resource metric labels).
// Registration is package-level so the controller-runtime metrics
// registry exposes it on each controller's /metrics endpoint.
var crdSSAConflictTotal = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_crd_ssa_conflict_total",
		Help: "SSA five-consecutive-409 stuck episodes on CRD fields, by crd and controller.",
	}, []string{"crd", "controller"})
	if err != nil {
		panic(fmt.Sprintf("ssaretry: build crd_ssa_conflict counter: %v", err))
	}
	ctrlmetrics.Registry.MustRegister(c)
	return c
}()

// ConflictID carries the identity of the resource an apply targets so
// the retry helper can label the §4.6.3 stuck log and the §16.1 counter.
// Controller is the field manager, CRD is the owning CRD kind (empty for
// a non-CRD apply such as a Pod label, which is outside the §16.1 counter
// scope), and Namespace and Name identify the object for the structured
// log's per-resource attribution.
type ConflictID struct {
	Controller string
	CRD        string
	Namespace  string
	Name       string
}

// maxAttempts is the §4.6.3 loop bound: after five consecutive
// no-progress 409s the helper stops retrying in-process and returns the
// conflict error so controller-runtime requeues the reconcile.
const maxAttempts = 5

// RetryOnConflictSSA implements the §4.6.3 SSA-conflict retry policy:
// always re-read before re-applying (the apply closure receives the
// attempt index and re-reads the live object itself), never
// force-conflicts, bounded retry with jittered backoff (100ms initial,
// 2s ceiling, five attempts). A non-409 error returns immediately
// because only a conflict indicates a stale cached resourceVersion worth
// re-reading. On five consecutive no-progress 409s against a CRD field
// (id.CRD != ""), the helper emits one crd_ssa_conflict_stuck log event
// and increments lenny_crd_ssa_conflict_total{crd, controller} by one,
// then returns the conflict error so controller-runtime re-enqueues the
// reconcile through its exponential rate-limiter (the §4.6.3
// "continues with exponential backoff").
//
// spec: §4.6.3 (SSA conflict retry policy), §16.1 (stuck counter)
func RetryOnConflictSSA(ctx context.Context, id ConflictID, apply func(attempt int) error) error {
	delay := 100 * time.Millisecond
	const maxDelay = 2 * time.Second
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := apply(attempt); err == nil {
			return nil
		} else if !apierrors.IsConflict(err) {
			return err
		} else {
			lastErr = err
		}
		jitter := time.Duration(rand.Int63n(int64(delay) / 4))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay + jitter):
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	// All five attempts were no-progress 409s. A CRD field-ownership
	// dispute emits the §4.6.3 stuck signal (one log + one counter
	// increment, correlated by identity). An apply that carries no CRD
	// kind (a Pod label under the sole owning field manager) emits
	// nothing, because a Pod label is not a CRD field owned by another
	// field manager (§16.1). Either way return the conflict error so
	// controller-runtime requeues with its exponential rate-limiter (the
	// §4.6.3 "continues with exponential backoff").
	if id.CRD != "" {
		logf.FromContext(ctx).Info(
			"crd_ssa_conflict_stuck",
			"controller", id.Controller,
			"resource", id.CRD,
			"name", id.Name,
			"namespace", id.Namespace,
		)
		crdSSAConflictTotal.WithLabelValues(id.CRD, id.Controller).Inc()
	}
	return lastErr
}
