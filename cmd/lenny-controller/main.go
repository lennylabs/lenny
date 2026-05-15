// SPDX-License-Identifier: MIT

// Command lenny-controller runs the Lenny control-plane controllers
// against a Kubernetes API server. It hosts the §4.6.1
// WarmPoolController, which reconciles each SandboxWarmPool toward its
// minWarm/maxWarm target by creating and draining Sandbox resources.
//
// The binary builds a controller-runtime manager: a shared client
// cache, leader election so only one replica reconciles at a time, a
// metrics endpoint, and health and readiness probes. Leader election
// is off by default and enabled with --leader-elect for a
// multi-replica Deployment; the §4.6.1 lease parameters (15s duration,
// 10s renew deadline, 2s retry period) give a 25s worst-case
// crash-failover window.
//
// Usage:
//
//	lenny-controller --leader-elect --leader-election-namespace lenny-system
//
// The cluster connection is resolved from the in-cluster service
// account when running as a pod, or from KUBECONFIG otherwise.
package main

import (
	"flag"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
)

// buildScheme assembles the runtime scheme the manager uses: the
// Kubernetes built-in types plus the lenny.dev/v1 CRDs the controllers
// reconcile.
func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(lennyv1.AddToScheme(s))
	return s
}

// §4.6.1 leader-election lease parameters. The worst-case crash
// failover window is leaseDuration + renewDeadline = 25s.
const (
	leaseDuration = 15 * time.Second
	renewDeadline = 10 * time.Second
	retryPeriod   = 2 * time.Second
)

func main() {
	var (
		metricsAddr   string
		probeAddr     string
		leaderElect   bool
		leaderElectNS string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"address the metrics endpoint binds to")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"address the health and readiness probes bind to")
	flag.BoolVar(&leaderElect, "leader-elect", false,
		"run leader election so only one replica reconciles at a time")
	flag.StringVar(&leaderElectNS, "leader-election-namespace", "lenny-system",
		"namespace that holds the leader-election Lease")
	zapOpts := zap.Options{Development: false}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	ld, rd, rp := leaseDuration, renewDeadline, retryPeriod
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                        buildScheme(),
		Metrics:                       metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                leaderElect,
		LeaderElectionID:              "lenny-warm-pool-controller",
		LeaderElectionNamespace:       leaderElectNS,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &ld,
		RenewDeadline:                 &rd,
		RetryPeriod:                   &rp,
	})
	if err != nil {
		log.Fatalf("lenny-controller: create manager: %v", err)
	}

	if err := (&warmpool.Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		log.Fatalf("lenny-controller: set up WarmPoolController: %v", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("lenny-controller: add healthz check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("lenny-controller: add readyz check: %v", err)
	}

	log.Printf("lenny-controller: starting manager (leader-election=%t)", leaderElect)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("lenny-controller: manager exited: %v", err)
	}
}
