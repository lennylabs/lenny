// SPDX-License-Identifier: MIT

package main

import (
	"log"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// registerProbes registers the §4.6.1 liveness and readiness probes on the
// manager so the Deployment's probe endpoints (the --health-probe-bind-address
// served by the manager) report the process as up and ready.
//
// spec: §4.6.1 — the manager serves health and readiness probes alongside the
// metrics endpoint and leader election.
func (w *controllerWiring) registerProbes() {
	if err := w.mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("lenny-controller: add healthz check: %v", err)
	}
	if err := w.mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("lenny-controller: add readyz check: %v", err)
	}
}
