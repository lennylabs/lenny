// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"
	"os"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lennylabs/lenny/pkg/controller/ratelimit"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// buildManagerSetup constructs the §4.6.1 manager and its prerequisites: the
// controller-runtime logger, the §16.3 tracer, the §17.5 RuntimeClass-name
// override map, the §5.2/§6.4 resource-class registry, the §4.6.1 rate-limited
// REST config, the manager itself, and the §10 line 437 CRD schema-version
// self-check. It records the manager, the REST config, the resolved overrides,
// the resource-class registry, and the trace-shutdown closer on the accumulator
// for the build steps that follow.
//
// spec: §4.6.1 — the manager carries leader election, the metrics endpoint, and
// the health/readiness probes; §16.3 — the controller's spans reach the OTLP
// Collector; §5.2/§6.4 — the resource-class registry fails closed when a class
// memory limit does not clear the tmpfs reservation; §10 line 437 — the CRD
// schema-version self-check refuses to start against a stale CRD. F-15.5.12.
func (w *controllerWiring) buildManagerSetup() {
	f := w.f

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&f.zapOpts)))

	// spec: §5.3 line 677 — log the dev-mode isolation warning once at
	// startup so an accidental production dev-mode install is visible.
	if f.devMode {
		log.Printf("lenny-controller: %s", isolation.DevModeIsolationWarning)
	}

	// spec: §16.3 line 359 — install the process-wide TracerProvider and
	// W3C propagator so the controller's §16.3 spans (session.claim_pod)
	// reach the OTLP Collector instead of the no-op provider. F-16.3.2.
	traceShutdown, err := tracing.InitProvider(context.Background(), tracing.ProviderConfig{
		ServiceName:  "lenny-controller",
		OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		DevMode:      f.devMode,
	})
	if err != nil {
		log.Fatalf("lenny-controller: tracing init: %v", err)
	}
	w.traceShutdown = traceShutdown

	// spec: §17.5 line 3 — assemble the §5.3 isolation-profile to
	// RuntimeClass-name override map. An unset flag leaves the chart-
	// default literal (runc / gvisor / kata) in place.
	w.runtimeClassOverrides = map[isolation.Profile]string{}
	if f.runtimeClassStandard != "" {
		w.runtimeClassOverrides[isolation.ProfileStandard] = f.runtimeClassStandard
	}
	if f.runtimeClassSandboxed != "" {
		w.runtimeClassOverrides[isolation.ProfileSandboxed] = f.runtimeClassSandboxed
	}
	if f.runtimeClassMicrovm != "" {
		w.runtimeClassOverrides[isolation.ProfileMicrovm] = f.runtimeClassMicrovm
	}

	// spec: §5.2 / §6.4 line 413 — build the resource-class registry from
	// the built-in small/medium/large defaults plus any operator overrides,
	// failing closed if a class's memory limit does not clear the tmpfs
	// reservation.
	resourceClasses, err := buildResourceClasses(f.resourceClassOverrides)
	if err != nil {
		log.Fatalf("lenny-controller: resource classes: %v", err)
	}
	w.resourceClasses = resourceClasses

	// §4.6.1 API server rate limiting: route Create calls for Sandbox
	// pods and UpdateStatus calls for Sandbox/SandboxWarmPool through two
	// dedicated client-side token buckets so pod creation is never starved
	// by status-update traffic; all other requests share the default
	// limiter.
	w.restCfg = ratelimit.WrapConfig(ctrl.GetConfigOrDie(), ratelimit.Config{
		CreateQPS:   f.createQPS,
		CreateBurst: f.createBurst,
		StatusQPS:   f.statusQPS,
		StatusBurst: f.statusBurst,
	})

	ld, rd, rp := leaseDuration, renewDeadline, retryPeriod
	mgr, err := ctrl.NewManager(w.restCfg, ctrl.Options{
		Scheme:                        buildScheme(),
		Metrics:                       metricsserver.Options{BindAddress: f.metricsAddr},
		HealthProbeBindAddress:        f.probeAddr,
		LeaderElection:                f.leaderElect,
		LeaderElectionID:              "lenny-warm-pool-controller",
		LeaderElectionNamespace:       f.leaderElectNS,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &ld,
		RenewDeadline:                 &rd,
		RetryPeriod:                   &rp,
	})
	if err != nil {
		log.Fatalf("lenny-controller: create manager: %v", err)
	}
	w.mgr = mgr

	// spec: §10 line 437 / line 443 — assert every installed Lenny CRD
	// declares the expected `lenny.dev/schema-version` annotation before
	// the controller starts reconciling. A mismatch logs the
	// runbook-grep-anchored FATAL message and exits non-zero so a stale
	// CRD never silently strips fields added by a newer controller.
	// F-15.5.12.
	if err := assertCRDSchemaVersion(w.restCfg); err != nil {
		log.Fatalf("lenny-controller: %v. See docs/runbooks/crd-upgrade.md", err)
	}
}
