// SPDX-License-Identifier: MIT

// Package pdbwatcher hosts the §10.4 PodDisruptionBudget status
// poller. The poller looks up a named PDB in the gateway namespace
// periodically and inspects Status.DisruptionsAllowed: when zero, the
// PDB is currently rejecting voluntary evictions. Each cycle that
// observes the blocking condition advances the §16
// lenny_pdb_blocked_evictions_total counter so the §16.5
// PDBBlockedEvictions alert (`increase(...) > 5 over 10m`) has a
// usable signal.
//
// The metric semantics here are "polling sample observed blocking",
// not the canonical "API server denied an eviction request". The
// canonical signal lives in the kube-apiserver audit log, which v1
// does not ingest. The poller's count tracks the time the PDB spent
// at DisruptionsAllowed=0, which is the operational state the
// PDBBlockedEvictions alert is designed to surface: voluntary
// evictions arriving while the PDB is in this state would be rejected.
//
// spec: §16.1 (`lenny_pdb_blocked_evictions_total`), §10.4 line 385,
// §16.5 PDBBlockedEvictions. F-10.4.4.
package pdbwatcher

import (
	"context"
	"log"
	"time"

	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Sink is the metric surface the watcher writes to. pkg/gateway/
// gatewaymetrics.Metrics satisfies it via IncPDBBlockedEvictions.
type Sink interface {
	IncPDBBlockedEvictions(pdb, controller string)
}

// Config configures a Watcher.
type Config struct {
	// Client is the cluster client. Required.
	Client client.Client
	// Namespace is the namespace holding the PDB. Required.
	Namespace string
	// PDBName is the PDB object name. Defaults to `lenny-gateway`.
	PDBName string
	// Interval is the polling cadence. Defaults to 30s.
	Interval time.Duration
	// Sink receives observations.
	Sink Sink
	// Logger is the structured-log sink. Defaults to log.Default.
	Logger Logger
	// ControllerLabel is the value used for the `controller` metric
	// label. The §16 catalog specifies `hpa | cluster_autoscaler |
	// node_drain | other` for the canonical audit-log emitter; the
	// polling-based v1 emitter uses `poller` to make the source
	// distinguishable in dashboards. Defaults to "poller".
	ControllerLabel string
}

// Logger is the minimal surface the watcher uses for diagnostic
// emissions. log.Default satisfies it.
type Logger interface {
	Printf(format string, args ...any)
}

// Watcher polls the PDB and forwards observations to the Sink.
type Watcher struct {
	cfg Config
}

// New returns a Watcher. Required fields (Client, Namespace, Sink)
// must be set; defaults apply to the rest.
func New(cfg Config) *Watcher {
	if cfg.PDBName == "" {
		cfg.PDBName = "lenny-gateway"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.ControllerLabel == "" {
		cfg.ControllerLabel = "poller"
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &Watcher{cfg: cfg}
}

// Run blocks polling until ctx is cancelled. Each polling cycle that
// observes Status.DisruptionsAllowed == 0 increments the counter on
// the configured Sink. Lookup errors are logged but never stall the
// loop. A NotFound on the PDB downgrades to a debug-level log so an
// install that opts out of the chart's PDB rendering does not produce
// alert noise.
func (w *Watcher) Run(ctx context.Context) {
	w.tick(ctx)
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Watcher) tick(ctx context.Context) {
	if w.cfg.Client == nil || w.cfg.Sink == nil {
		return
	}
	key := client.ObjectKey{Namespace: w.cfg.Namespace, Name: w.cfg.PDBName}
	var pdb policyv1.PodDisruptionBudget
	if err := w.cfg.Client.Get(ctx, key, &pdb); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		w.cfg.Logger.Printf("lenny-gateway: §10.4 PDB poll %s/%s failed: %v",
			w.cfg.Namespace, w.cfg.PDBName, err)
		return
	}
	if pdb.Status.DisruptionsAllowed == 0 {
		// spec: §10.4 PDB-bound scale-down floor / §16.5
		// PDBBlockedEvictions. Each cycle where the PDB has zero
		// disruption budget contributes one increment so the alert's
		// 10-minute window captures sustained blocking.
		w.cfg.Sink.IncPDBBlockedEvictions(w.cfg.PDBName, w.cfg.ControllerLabel)
	}
}
