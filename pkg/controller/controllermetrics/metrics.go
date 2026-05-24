// SPDX-License-Identifier: MIT

// Package controllermetrics emits the §4.6.1 controller-runtime
// operability metrics that the §16.5 ControllerLeaderElectionFailed and
// ControllerWorkQueueDepthHigh alerts read: the leader-lease renewal age
// gauge, the per-controller work-queue depth gauge, the work-queue
// overflow counter, and the configured work-queue max-depth gauge.
//
// All series register against the controller-runtime metrics registry so
// they appear on the controller's existing /metrics endpoint alongside
// the framework's own metrics.
package controllermetrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

var (
	// leaseRenewalAge is the §4.6.1 lenny_controller_leader_lease_renewal_age_seconds
	// gauge: seconds since the controller's leader last renewed its Lease,
	// one series per controller. The §16.5 ControllerLeaderElectionFailed
	// alert fires when the age exceeds leaseDuration (15s).
	leaseRenewalAge = mustGauge(prometheus.GaugeOpts{
		Name: "lenny_controller_leader_lease_renewal_age_seconds",
		Help: "Seconds since the controller leader last renewed its Lease.",
	}, []string{"controller"})

	// workqueueDepth is the §4.6.1 lenny_controller_workqueue_depth gauge:
	// the instantaneous depth of a controller reconciliation work queue,
	// labeled by controller and queue. The §16.5 ControllerWorkQueueDepthHigh
	// alert fires when depth exceeds 50% of the configured max depth.
	workqueueDepth = mustGauge(prometheus.GaugeOpts{
		Name: "lenny_controller_workqueue_depth",
		Help: "Instantaneous controller reconciliation work-queue depth.",
	}, []string{"controller", "queue"})

	// queueOverflow is the §4.6.1 lenny_controller_queue_overflow_total
	// counter: it increments each time a new reconciliation event is dropped
	// because the work queue is already at its configured max depth.
	queueOverflow = mustCounter(prometheus.CounterOpts{
		Name: "lenny_controller_queue_overflow_total",
		Help: "Reconciliation events dropped on work-queue overflow.",
	}, []string{"controller"})

	// workqueueMaxDepth is the configured work-queue max depth, exposed as
	// an unlabeled gauge so the ControllerWorkQueueDepthHigh alert's
	// scalar(lenny_controller_workqueue_max_depth) threshold resolves to a
	// single series. It is set from --workqueue-max-depth at queue
	// construction.
	workqueueMaxDepth = mustGauge(prometheus.GaugeOpts{
		Name: "lenny_controller_workqueue_max_depth",
		Help: "Configured controller work-queue max depth.",
	}, nil)
)

func mustGauge(opts prometheus.GaugeOpts, labels []string) *prometheus.GaugeVec {
	g, err := metrics.NewGauge(opts, labels)
	if err != nil {
		panic(fmt.Sprintf("controllermetrics: build gauge %s: %v", opts.Name, err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}

func mustCounter(opts prometheus.CounterOpts, labels []string) *prometheus.CounterVec {
	c, err := metrics.NewCounter(opts, labels)
	if err != nil {
		panic(fmt.Sprintf("controllermetrics: build counter %s: %v", opts.Name, err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, c)
	return c
}
