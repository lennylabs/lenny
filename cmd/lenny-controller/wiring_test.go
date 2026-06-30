// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	agentpodstatepg "github.com/lennylabs/lenny/pkg/agentpodstate/pgstore"
	"github.com/lennylabs/lenny/pkg/controller/cidrdrift"
	"github.com/lennylabs/lenny/pkg/controller/controllermetrics"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	runtimecontroller "github.com/lennylabs/lenny/pkg/controller/runtime"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	experimentstorepg "github.com/lennylabs/lenny/pkg/gateway/experimentstore/pgstore"
	poolstorepg "github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore/pgstore"
	runtimepg "github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore/pgstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// recordingManager wraps a real controller-runtime manager and records the
// concrete type of every runnable registered through Add. Every controller the
// register steps wire flows through Add: a controller built with
// ctrl.NewControllerManagedBy(mgr)...Complete() registers itself with the
// manager via mgr.Add (controller-runtime's controller.NewTyped ends in
// `mgr.Add(c)`), and each leader-elected runnable is wired with an explicit
// mgr.Add(runnable). Recording Add therefore captures the full registration
// outcome the composition root produces under a given flag combination.
//
// The wrapper embeds the real manager so it satisfies the entire ctrl.Manager
// interface (GetClient, GetScheme, GetCache, ...) that ctrl.NewControllerManagedBy
// reads while building each controller; only Add is overridden to record.
type recordingManager struct {
	manager.Manager
	addedControllers []string // ctrl.NewControllerManagedBy(...).Complete() registrations
	addedRunnables   []string // explicit mgr.Add(runnable) registrations
}

// Add records the runnable type, then delegates to the real manager so the
// controller name uniqueness and source wiring the builder performs still run.
// A controller registered through ctrl.NewControllerManagedBy(mgr)...Complete()
// is the controller-runtime internal controller type (its package path is the
// controller-runtime tree); a leader-elected runnable this binary wires is one
// of Lenny's own concrete types (MirrorReconciler, ClaimGarbageCollector,
// poolscaling.Runnable, cidrdrift.Detector, LeaseRenewalMonitor). The two sets
// are distinguished by package path so the discriminator does not depend on the
// exact generic type string controller-runtime emits.
func (m *recordingManager) Add(r manager.Runnable) error {
	t := reflect.TypeOf(r)
	pkgPath := t.String()
	if elem := t; elem.Kind() == reflect.Pointer {
		pkgPath = elem.Elem().PkgPath()
	}
	if strings.HasPrefix(pkgPath, "sigs.k8s.io/controller-runtime") {
		m.addedControllers = append(m.addedControllers, t.String())
	} else {
		m.addedRunnables = append(m.addedRunnables, t.String())
	}
	return m.Manager.Add(r)
}

// runnableTypeNames returns the set of leader-elected runnable type names the
// composition root registered, keyed by the concrete type so a dropped or
// flipped gate is detectable independent of registration order.
func (m *recordingManager) runnableTypeNames() map[string]bool {
	out := map[string]bool{}
	for _, n := range m.addedRunnables {
		out[n] = true
	}
	return out
}

// newRecordingManager boots a real manager against the envtest API server and
// wraps it for recording. Metrics and the health probe bind to "0" (a disabled
// listener) and leader election is set per scenario, so each scenario gets a
// fresh manager without a port clash. Building the manager does not start it, so
// no reconcile runs; the test asserts only the registration outcome.
func newRecordingManager(t *testing.T, cfg *rest.Config, leaderElect bool) *recordingManager {
	t.Helper()
	mgr, err := newScopedManager(cfg, leaderElect)
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	return &recordingManager{Manager: mgr}
}

// newScopedManager constructs a manager whose listeners are disabled so several
// can coexist within one test process against one envtest API server.
// SkipNameValidation lets each scenario register a controller of the same name
// (warmpool, ...) without colliding with the process-global controller-name
// registry a sibling scenario already populated; controller-name uniqueness is
// a controller-runtime concern, not the wiring outcome this test pins.
func newScopedManager(cfg *rest.Config, leaderElect bool) (manager.Manager, error) {
	mgr, err := manager.New(cfg, manager.Options{
		Scheme:                  buildScheme(),
		Metrics:                 metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:  "0",
		LeaderElection:          leaderElect,
		LeaderElectionID:        "lenny-warm-pool-controller",
		LeaderElectionNamespace: "lenny-system",
		Controller:              config.Controller{SkipNameValidation: ptr.To(true)},
		// A disabled cache reader is not needed; the builder only wires watch
		// sources against the cache and never reads it until Start.
		Cache: cache.Options{},
	})
	if err != nil {
		return nil, fmt.Errorf("new manager: %w", err)
	}
	return mgr, nil
}

// newWiringForTest builds a controllerWiring with the supplied recording
// manager and the §4.6.1 store/emitter/queue inputs the register steps read.
// The store fields are set by the per-scenario helper so each scenario exercises
// a distinct flag gate; the shared inputs (queue factory, ops emitter) are
// always present because buildStores constructs them unconditionally.
func newWiringForTest(f *controllerFlags, mgr manager.Manager) *controllerWiring {
	w := &controllerWiring{f: f}
	w.mgr = mgr
	w.queueFactory = controllermetrics.NewQueueFactory(f.workqueueMaxDepth)
	w.opsEmitter = eventbuffer.NewEmitter(eventbuffer.NewEventBuffer(0), "controller-test")
	w.runtimeClassOverrides = map[isolation.Profile]string{}
	return w
}

// registerAll runs the composition root's registration sequence (the build
// steps runController calls after buildManagerSetup/buildStores) against the
// recording manager, exactly as runController does.
func (w *controllerWiring) registerAll() {
	w.registerCoreControllers()
	w.registerOptionalControllers()
	w.registerLeaderRunnables()
	w.registerProbes()
}

// spec: 4.6.1 (controller composition-root wiring outcome), 4.1 (the
// composition root threads its inputs to each subsystem), 5.1
// (RuntimeReconciler is Postgres-gated), 10.7 (PoolScalingController is
// Postgres+namespace-gated), 13.2 (CIDR-drift detector is namespace/
// system-gated), 16.5 (leader-lease monitor is leader-election-gated)
//
// diagnosis: a failure here means the R8 decomposition (proposal 0020 §4 Part A
// R8) changed which controllers or leader-elected runnables the lenny-controller
// composition root registers, or flipped one of the spec-named conditional
// gates. The block move was supposed to be behavior-preserving: the same
// controllers register under the same flag combinations as the pre-R8 monolith.
// A failure points at registerCoreControllers/registerOptionalControllers/
// registerLeaderRunnables in controllers.go: a dropped SetupWithManager/mgr.Add
// call, a flipped gate (for example `w.mirror != nil` → `== nil`, or a dropped
// `len(w.agentNamespaces) > 0` term), or a changed leader-election gate.
//
// TestControllerWiringOutcomePinsRegistrationGates is the behavioral
// characterization the R8 step requires: it pins main's wiring outcome (which
// controllers and runnables register, and under which flag gates) rather than
// only the function's structure. It boots a real manager per scenario behind a
// recording Add seam and asserts the registered set for each spec-named gate
// combination. Against the pre-R8 monolith the same outcome holds, so the move
// is provably behavior-preserving; against a regression that drops a
// registration or flips a gate, the matching scenario fails.
func TestControllerWiringOutcomePinsRegistrationGates(t *testing.T) {
	env := envtest.Start(t)
	cfg := env.RESTConfig()

	// The four core controllers always register: the WarmPoolController, the
	// Sandbox reconciler, the per-pod reconciler, and the occupancy reconciler
	// (registerCoreControllers, ungated). Each flows through the builder, so the
	// recorder counts four *controller.Controller registrations from the core
	// step alone; the optional RuntimeReconciler adds a fifth when its gate
	// opens.
	const coreControllers = 4

	t.Run("no-postgres-no-namespaces-no-leader", func(t *testing.T) {
		f := &controllerFlags{leaderElectNS: "lenny-system"}
		mgr := newRecordingManager(t, cfg, false)
		w := newWiringForTest(f, mgr)
		// No Postgres: mirror/runtimeRegistry/poolScaling stores stay nil.
		// No namespaces: agentNamespaces is empty.
		w.registerAll()

		// Only the four core controllers register; the §5.1 RuntimeReconciler is
		// Postgres-gated and skipped.
		if got := len(mgr.addedControllers); got != coreControllers {
			t.Errorf("registered %d controllers, want %d (core only, RuntimeReconciler Postgres-gated)", got, coreControllers)
		}
		// The §13.2 CIDR-drift detector still registers because leaderElectNS is
		// set (the gate is `len(agentNamespaces) > 0 || leaderElectNS != ""`);
		// no other runnable registers without Postgres or leader election.
		runnables := mgr.runnableTypeNames()
		wantRunnable(t, runnables, &cidrdrift.Detector{})
		notRunnable(t, runnables, &warmpool.MirrorReconciler{})
		notRunnable(t, runnables, &warmpool.ClaimGarbageCollector{})
		notRunnable(t, runnables, &poolscaling.Runnable{})
		notRunnable(t, runnables, &controllermetrics.LeaseRenewalMonitor{})
		assertRuntimeReconcilerAbsent(t, mgr)
	})

	t.Run("postgres-only-no-namespaces", func(t *testing.T) {
		f := &controllerFlags{leaderElectNS: "lenny-system"}
		mgr := newRecordingManager(t, cfg, false)
		w := newWiringForTest(f, mgr)
		// Postgres present: the §5.1 RuntimeReconciler gate opens. The pool/
		// experiment registries are present, but the §10.7 PoolScalingController
		// and the §4.6.1 mirror/GC stay gated on agent namespaces, which are
		// empty here.
		setPostgresStores(w)
		w.registerAll()

		if got := len(mgr.addedControllers); got != coreControllers+1 {
			t.Errorf("registered %d controllers, want %d (core + §5.1 RuntimeReconciler)", got, coreControllers+1)
		}
		runnables := mgr.runnableTypeNames()
		// Mirror/GC and PoolScalingController need agent namespaces too, so they
		// stay absent under Postgres-only.
		notRunnable(t, runnables, &warmpool.MirrorReconciler{})
		notRunnable(t, runnables, &warmpool.ClaimGarbageCollector{})
		notRunnable(t, runnables, &poolscaling.Runnable{})
		wantRunnable(t, runnables, &cidrdrift.Detector{})
		notRunnable(t, runnables, &controllermetrics.LeaseRenewalMonitor{})
	})

	t.Run("postgres-and-namespaces", func(t *testing.T) {
		f := &controllerFlags{leaderElectNS: "lenny-system"}
		mgr := newRecordingManager(t, cfg, false)
		w := newWiringForTest(f, mgr)
		setPostgresStores(w)
		w.agentNamespaces = []string{"lenny-agents"}
		w.registerAll()

		runnables := mgr.runnableTypeNames()
		// §4.6.1 mirror reconciliation and orphan-claim GC open under
		// Postgres + namespaces.
		wantRunnable(t, runnables, &warmpool.MirrorReconciler{})
		wantRunnable(t, runnables, &warmpool.ClaimGarbageCollector{})
		// §10.7 PoolScalingController opens under Postgres + namespaces.
		wantRunnable(t, runnables, &poolscaling.Runnable{})
		// §13.2 CIDR-drift detector registers (namespaces set).
		wantRunnable(t, runnables, &cidrdrift.Detector{})
		// Still no leader election, so the §16.5 lease monitor stays absent.
		notRunnable(t, runnables, &controllermetrics.LeaseRenewalMonitor{})
	})

	t.Run("leader-elect-gates-lease-monitor", func(t *testing.T) {
		f := &controllerFlags{leaderElectNS: "lenny-system", leaderElect: true}
		mgr := newRecordingManager(t, cfg, true)
		w := newWiringForTest(f, mgr)
		w.registerAll()

		runnables := mgr.runnableTypeNames()
		// The §16.5 LeaseRenewalMonitor registers only under --leader-elect.
		wantRunnable(t, runnables, &controllermetrics.LeaseRenewalMonitor{})
	})
}

// setPostgresStores populates the wiring's Postgres-gated store fields with
// non-nil placeholders so the register steps observe the same "Postgres present"
// condition buildStores produces under --postgres-dsn. The stores are never
// dereferenced because the register steps are not started; only the nil checks
// that gate registration read them. A bare *pgxpool.Pool backs each so the
// concrete-store fields are non-nil interface values.
func setPostgresStores(w *controllerWiring) {
	// A zero *pgxpool.Pool backs each store: the register steps are never
	// started, so the pool is never used; only the non-nil gate is read. Each
	// store constructor is a trivial field assignment, so no connection opens.
	pool := &pgxpool.Pool{}
	w.mirror = agentpodstatepg.New(pool)
	w.runtimeRegistry = runtimepg.New(pool)
	w.poolScalingPools = poolstorepg.New(pool)
	w.poolScalingExperiments = experimentstorepg.New(pool)
	w.sessionLookup = &sessionActiveLookup{}
}

// assertRuntimeReconcilerAbsent confirms the §5.1 RuntimeReconciler did not
// register by reconstructing the expected controller count: without Postgres,
// registerOptionalControllers adds no controller, so the total stays at the
// four core controllers.
func assertRuntimeReconcilerAbsent(t *testing.T, mgr *recordingManager) {
	t.Helper()
	if got := len(mgr.addedControllers); got != 4 {
		t.Errorf("registered %d controllers without Postgres, want 4 (no §5.1 RuntimeReconciler)", got)
	}
	// Guard against the type being silently swapped: the runtime controller is
	// not a leader-elected runnable, so it must never appear in the runnable set.
	notRunnable(t, mgr.runnableTypeNames(), &runtimecontroller.Reconciler{})
}

func wantRunnable(t *testing.T, set map[string]bool, runnable any) {
	t.Helper()
	name := reflect.TypeOf(runnable).String()
	if !set[name] {
		t.Errorf("leader-elected runnable %s did not register but its flag gate is open (proposal 0020 R8 wiring outcome)", name)
	}
}

func notRunnable(t *testing.T, set map[string]bool, runnable any) {
	t.Helper()
	name := reflect.TypeOf(runnable).String()
	if set[name] {
		t.Errorf("runnable %s registered but its flag gate is closed: a gate was flipped or dropped (proposal 0020 R8 wiring outcome)", name)
	}
}

// spec: 4.6.1 (the composition root threads the manager and probes to every
// step)
//
// diagnosis: a failure here means the R8 decomposition (proposal 0020 §4 Part A
// R8) dropped one of the §4.6.1 liveness/readiness probe registrations when it
// extracted registerProbes from the monolithic main. The Deployment's probe
// endpoints depend on both checks being added, so a dropped probe would make the
// controller never report ready (readyz) or never report live (healthz).
//
// TestRegisterProbesWiresLivenessAndReadiness pins that the §4.6.1 probe
// registration step adds both the healthz and readyz checks to the manager, so
// the R8 move did not drop a probe. It runs registerProbes against a recording
// manager and asserts both checks are present. The probes are added via
// AddHealthzCheck/AddReadyzCheck rather than Add, so this asserts on a thin
// recording wrapper that captures those calls.
func TestRegisterProbesWiresLivenessAndReadiness(t *testing.T) {
	env := envtest.Start(t)
	mgr, err := newScopedManager(env.RESTConfig(), false)
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	pr := &probeRecorder{Manager: mgr}
	w := &controllerWiring{f: &controllerFlags{}}
	w.mgr = pr
	w.registerProbes()

	if !pr.healthz {
		t.Error("registerProbes did not add the §4.6.1 healthz check")
	}
	if !pr.readyz {
		t.Error("registerProbes did not add the §4.6.1 readyz check")
	}
}

// probeRecorder records the healthz/readyz check registrations registerProbes
// performs, delegating to the real manager so the check is actually installed.
type probeRecorder struct {
	manager.Manager
	healthz bool
	readyz  bool
}

func (p *probeRecorder) AddHealthzCheck(name string, check healthz.Checker) error {
	p.healthz = true
	return p.Manager.AddHealthzCheck(name, check)
}

func (p *probeRecorder) AddReadyzCheck(name string, check healthz.Checker) error {
	p.readyz = true
	return p.Manager.AddReadyzCheck(name, check)
}
