// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/storage/failopen"
)

// quotaFailOpenAuditEmitter adapts the gateway's §11.7 audit appender to
// the failopen.AuditEmitter seam. The quota_failopen_started event is
// replica-scoped (the outage affects every tenant on the replica), so it
// is written to the platform chain (empty tenant id) with the
// service_instance_id and timestamp the §16.7 catalogue entry requires.
//
// spec: §12.4 line 224; §16.7 (quota_failopen_started).
type quotaFailOpenAuditEmitter struct {
	appender policy.AuditAppender
}

func (e quotaFailOpenAuditEmitter) EmitQuotaFailOpenStarted(ctx context.Context, serviceInstanceID string, at time.Time) {
	if e.appender == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		// spec: §12.4 line 224 — the event carries tenant_id (empty for a
		// replica-wide outage), service_instance_id, and timestamp.
		"tenant_id":           "",
		"service_instance_id": serviceInstanceID,
		"timestamp":           at.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	// Best-effort: a fail-open audit append must never block the admission
	// hot path or fail the request. The controller already calls this in a
	// goroutine.
	_, _ = e.appender.Append(ctx, "", "quota_failopen_started", payload, at)
}

// gatewayEndpointsLister counts the ready endpoints backing the gateway
// Service via the controller-runtime client. It implements
// failopen.EndpointsLister so the §12.4 line 224 cached_replica_count is
// sourced from the Kubernetes Endpoints object for the gateway Service.
//
// spec: §12.4 line 224.
type gatewayEndpointsLister struct {
	client    client.Client
	namespace string
	service   string
}

func (l gatewayEndpointsLister) CountReady(ctx context.Context) (int, error) {
	var ep corev1.Endpoints
	key := client.ObjectKey{Namespace: l.namespace, Name: l.service}
	if err := l.client.Get(ctx, key, &ep); err != nil {
		return 0, err
	}
	ready := 0
	for _, subset := range ep.Subsets {
		ready += len(subset.Addresses)
	}
	return ready, nil
}

// buildFailOpenController assembles the §12.4 per-replica fail-open
// controller. It returns nil when the per-user fraction is the only signal
// and the caller wants the legacy allow-until-episode-cap behaviour; in
// practice the gateway always builds one so the cumulative timer and
// per-user backstop are active. The CumulativeTimer's OnChange mirrors the
// running value onto the lenny_quota_failopen_cumulative_seconds gauge.
//
// spec: §12.4 lines 220-224. F-12.4.9 / F-11.2.6.
func buildFailOpenController(cfg failOpenWiring) *failopen.Controller {
	timer := failopen.NewCumulativeTimer(failopen.CumulativeConfig{
		MaxSeconds: time.Duration(cfg.cumulativeMaxSeconds) * time.Second,
		StatePath:  cfg.statePath,
		OnChange:   cfg.metrics.SetQuotaFailopenCumulativeSeconds,
	})
	return failopen.NewController(failopen.ControllerConfig{
		Timer:             timer,
		Backstop:          failopen.NewBackstop(nil),
		Replicas:          cfg.replicas,
		UserFraction:      cfg.userFraction,
		PerReplicaHardCap: cfg.perReplicaHardCap,
		Audit:             quotaFailOpenAuditEmitter{appender: cfg.appender},
		InstanceID:        cfg.serviceInstanceID,
	})
}

// failOpenWiring carries the assembled inputs buildFailOpenController needs.
type failOpenWiring struct {
	metrics interface {
		SetQuotaFailopenCumulativeSeconds(float64)
	}
	replicas             *failopen.ReplicaCount
	appender             policy.AuditAppender
	statePath            string
	cumulativeMaxSeconds int
	perReplicaHardCap    int64
	userFraction         float64
	serviceInstanceID    string
}
