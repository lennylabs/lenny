// SPDX-License-Identifier: MIT

// Command lenny-controller runs the Lenny control-plane controllers
// against a Kubernetes API server. It hosts the §4.6.1
// WarmPoolController, which reconciles each SandboxWarmPool toward its
// minWarm/maxWarm target by creating and draining Sandbox resources,
// and the Sandbox-to-Pod reconciler, which materializes each Sandbox
// into a backing Pod and drives the §6.2 warm-path lifecycle.
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
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/resourceclass"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/preflight"
)

// buildScheme assembles the runtime scheme the manager uses: the
// Kubernetes built-in types plus the lenny.dev/v1alpha1 CRDs the controllers
// reconcile.
//
// apiextensions.k8s.io/v1 is also registered so the §10 line 437
// startup self-check can fetch each installed CRD and read its
// `lenny.dev/schema-version` annotation. F-15.5.12.
func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(lennyv1.AddToScheme(s))
	utilruntime.Must(apiextensionsv1.AddToScheme(s))
	return s
}

// splitNamespaces parses a comma-separated namespace list, trimming
// whitespace and dropping empty entries. An empty or whitespace-only
// input yields a nil slice.
func splitNamespaces(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if ns := strings.TrimSpace(part); ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// envInt64 returns the int64 value of environment variable key, or def
// when the variable is unset, empty, or not a valid integer. It backs
// the §13.1 non-root UID flag defaults so the chart can wire them
// through env without a separate parsing step. F-13.1.16.
func envInt64(key string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// repeatableFlag accumulates the value of a flag passed multiple times. It
// backs --resource-class so an operator can retune several §5.2 classes on
// one command line. spec: §6.4 line 413.
type repeatableFlag []string

func (f *repeatableFlag) String() string     { return strings.Join(*f, ",") }
func (f *repeatableFlag) Set(v string) error { *f = append(*f, v); return nil }

// buildResourceClasses starts from the built-in §5.2 small/medium/large
// defaults and applies the operator's --resource-class overrides, then
// validates that every class's memory limit clears the §6.4 tmpfs
// reservation. spec: §5.2 line 369, §6.4 line 413.
func buildResourceClasses(overrides []string) (resourceclass.Registry, error) {
	reg := resourceclass.DefaultRegistry()
	for _, raw := range overrides {
		name, req, err := resourceclass.ParseOverride(raw)
		if err != nil {
			return nil, err
		}
		reg.Set(name, req)
	}
	if err := reg.Validate(); err != nil {
		return nil, err
	}
	return reg, nil
}

// §4.6.1 leader-election lease parameters. The worst-case crash
// failover window is leaseDuration + renewDeadline = 25s.
const (
	leaseDuration = 15 * time.Second
	renewDeadline = 10 * time.Second
	retryPeriod   = 2 * time.Second
)

// main is the §4.6.1 lenny-controller entry point. It installs the §16.4 JSON
// logger, then parses the command-line flags once into the controllerFlags
// value and hands off to runController, which wires and starts every controller
// and blocks on the §4.6.1 manager run loop. No subsystem is constructed here;
// the composition root in wiring.go runs the ordered per-subsystem build
// sequence (proposal 0020 §4 Part A R8).
//
// spec: §16.4 lines 370-372 — structured JSON logs from the pool controller;
// routes the stdlib log package through the §16.4 handler (component=controller)
// before any subsystem logs. F-16.4.1. §4.1 — the composition root parses its
// inputs once and threads them to each subsystem builder; §4.6.1 — the
// lenny-controller service body.
func main() {
	// spec: §16.4 lines 370-372 — structured JSON logs from the pool
	// controller; routes the stdlib log package through the §16.4 handler
	// (component=controller). F-16.4.1. This runs before flag parsing so the
	// flag-parse and wiring log lines also surface in the §16.4 format.
	logging.Setup(os.Stderr, "controller")
	runController(parseFlags())
}

// assertCRDSchemaVersion is the §10 line 437 startup self-check: it
// builds a read-only controller-runtime client and runs the shared
// preflight CRD schema-version comparison. On mismatch (or a missing
// CRD, or a missing annotation) it returns an error whose text matches
// the spec line 437 runbook anchor so operators can grep the controller
// logs for the exact message. F-15.5.12.
func assertCRDSchemaVersion(restCfg *rest.Config) error {
	c, err := ctrlclient.New(restCfg, ctrlclient.Options{Scheme: buildScheme()})
	if err != nil {
		return fmt.Errorf("CRD schema-version check: build read-only client: %w", err)
	}
	decision := preflight.CRDSchemaVersionCheck{
		Expected: preflight.CurrentCRDSchemaVersion,
	}.Decide(context.Background(), c)
	if !decision.Passed {
		return fmt.Errorf("CRD schema version mismatch: %s", decision.Reason)
	}
	return nil
}

// sessionActiveLookup adapts the session store to the §4.6.1 orphan-claim
// GC's warmpool.SessionLookup contract: a claim is reclaimable when no
// non-terminal session backs it. The per-pod claim (§4.6.3) carries no
// session identifier, so the check keys on the pod through the Postgres
// `pod_assignment` binding: GetActiveSlotsByPod counts the live
// (non-terminal) sessions bound to the Sandbox, and a pod with none has
// no live session. A pod whose sessions all reached a terminal state (or
// whose gateway crashed before persisting any session) reports inactive.
type sessionActiveLookup struct {
	store sessionstore.Store
}

func (l *sessionActiveLookup) PodHasActiveSession(ctx context.Context, sandboxRef string) (bool, error) {
	n, err := l.store.GetActiveSlotsByPod(ctx, sandboxRef)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
