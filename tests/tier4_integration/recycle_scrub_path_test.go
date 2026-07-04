// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for proposal 0031 (wire the §5.2 whole-pod scrub
// adapter-side). It drives the session-mode recycle path end to end across the
// gateway binder, a real adapter Server, and the §6.2 disposition decider:
//
//	poolstore sessionPolicy → PoolMatch → BindResult → §4.7 recycle Shutdown
//	→ adapter scrub.Run → ReportPodScrub → podscrub.Decide (reuse | retire).
//
// The gateway consumer side of ReportPodScrub (the leasecontrol handler and the
// recycle-boundary missing-report timeout) is already wired in production and
// exercised at its own tiers; this test drives the pure §6.2 disposition
// function (podscrub.Decide) with the outcome the adapter actually reports (or
// the timeout that fires when the adapter withholds the report), so the whole
// fold-to-disposition chain is asserted on one flow rather than component by
// component. It crosses the gateway binder, the SandboxClaim on a real
// kube-apiserver (envtest), and the pod adapter over an in-memory gRPC
// connection, which is the multi-service surface the integration tier owns.
//
// spec: 5.2 (recycle lifecycle, cross-tenant reuse, sessionPolicy scrub config
// delivered on the recycle Shutdown), 6.2 (failed/crashed session always
// retires), 4.6.3 (scrub disposition, poolstore ownership), 3.4 (recycle
// disposition, missing-report timeout).

package tier4_integration_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/recycle"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const recycleNS = "lenny-agents"

// recycleScrubOps is a scrub.Ops double for the tier-4 recycle path. It records
// that the whole-pod scrub host operations ran (kill, remove, verify) so the
// test can assert the adapter actually performed the §5.2 scrub after the
// recycle Shutdown, without running kill -9 -1 or touching the real filesystem.
// A mutex keeps it -race clean when the async scrub goroutine and the test read
// concurrently.
type recycleScrubOps struct {
	mu       sync.Mutex
	killed   bool
	removed  []string
	verified bool
}

func (o *recycleScrubOps) KillUserProcesses(context.Context) error {
	o.mu.Lock()
	o.killed = true
	o.mu.Unlock()
	return nil
}
func (o *recycleScrubOps) PurgeIPCShm(context.Context) error { return nil }
func (o *recycleScrubOps) RemoveAll(path string) error {
	o.mu.Lock()
	o.removed = append(o.removed, path)
	o.mu.Unlock()
	return nil
}
func (o *recycleScrubOps) ClearContents(string) error { return nil }
func (o *recycleScrubOps) PathState(string) (bool, bool, error) {
	o.mu.Lock()
	o.verified = true
	o.mu.Unlock()
	return false, true, nil
}

func (o *recycleScrubOps) ranScrub() (killed, verified bool, removed []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.removed))
	copy(out, o.removed)
	return o.killed, o.verified, out
}

// recycleScrubReporter is a PodScrubReporter double that records every
// ReportPodScrub the adapter emits and signals a channel on the first one, so
// the test can wait for the async report deterministically. A withheld report
// (the vm-restart nil-restarter path) never fires the channel; the test relies
// on the adapter's scrub-done hook to know the goroutine finished.
type recycleScrubReporter struct {
	mu       sync.Mutex
	reports  []recycleReport
	reported chan struct{}
	once     sync.Once
}

type recycleReport struct {
	podID   string
	outcome gatewaycontrol.PodScrubOutcome
}

func newRecycleScrubReporter() *recycleScrubReporter {
	return &recycleScrubReporter{reported: make(chan struct{})}
}

func (r *recycleScrubReporter) ReportPodScrub(_ context.Context, podID string, outcome gatewaycontrol.PodScrubOutcome, _ string) error {
	r.mu.Lock()
	r.reports = append(r.reports, recycleReport{podID: podID, outcome: outcome})
	r.mu.Unlock()
	r.once.Do(func() { close(r.reported) })
	return nil
}

func (r *recycleScrubReporter) snapshot() []recycleReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recycleReport, len(r.reports))
	copy(out, r.reports)
	return out
}

// recycleCluster returns an envtest-backed client seeded with a warm pool, its
// template, and one idle Sandbox the binder can claim for the recycling
// session-mode flow.
func recycleCluster(t *testing.T) client.Client {
	t.Helper()
	envtest.SkipUnlessAvailable(t)
	env := envtest.Start(t)
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme lenny: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: recycleNS}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "recycle-pool", Namespace: recycleNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "recycle-tmpl", MinWarm: 1, MaxWarm: 5},
	}); err != nil {
		t.Fatalf("create warm pool: %v", err)
	}
	if err := c.Create(ctx, &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "recycle-tmpl", Namespace: recycleNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "echo", IsolationProfile: "microvm"},
	}); err != nil {
		t.Fatalf("create template: %v", err)
	}
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sbx-r", Namespace: recycleNS,
			Labels: map[string]string{warmpool.LabelPool: "recycle-pool"},
		},
	}
	if err := c.Create(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	// Status is a subresource that Create ignores; seed it through a status
	// Update so the binder's label-selecting claim finds an idle pod with a
	// reachable pod IP.
	sb.Status = lennyv1.SandboxStatus{Phase: "idle", PodIP: "10.244.3.9"}
	if err := c.Status().Update(ctx, sb); err != nil {
		t.Fatalf("seed sandbox status: %v", err)
	}
	return c
}

// recycleAdapterDialer serves the real adapter Server over an in-memory
// connection so the binder's recycle Shutdown reaches the production
// Server.Shutdown handler, which runs the whole-pod scrub and reports.
func recycleAdapterDialer(t *testing.T, srv *adapter.Server) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return func(string) (*adapterclient.Client, error) {
		return adapterclient.Dial("passthrough:///bufnet",
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
}

// recycleBinder wires a podsession.Binder against the cluster and the adapter
// dialer, with a recording recycle-boundary armer.
func recycleBinder(c client.Client, dial func(string) (*adapterclient.Client, error)) (*podsession.Binder, *recordingArmer) {
	armer := &recordingArmer{}
	return &podsession.Binder{
		Client:           c,
		Namespace:        recycleNS,
		AdapterPort:      50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		DialAdapter:      dial,
		RecycleBoundary:  armer,
	}, armer
}

// newRecycleCoordinator builds the production §3.4 RecycleBoundaryCoordinator
// against the cluster, so the dropped-report test exercises the real
// missing-report timeout. It seeds a corev1.Pod carrying the pool label and a
// poolstore pool with a 1s cleanupTimeoutSeconds; a 5ms grace bounds the timer
// to about a second. The coordinator's AfterFunc seam is package-private, so the
// timer is real rather than stubbed.
func newRecycleCoordinator(t *testing.T, c client.Client) *recycle.RecycleBoundaryCoordinator {
	t.Helper()
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "sbx-r", Namespace: recycleNS,
			Labels: map[string]string{warmpool.LabelPool: "recycle-pool"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "echo"}}},
	}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create pod for coordinator: %v", err)
	}
	pools := poolstore.NewMemory()
	if err := pools.Create(ctx, poolstore.Pool{
		Name:          "recycle-pool",
		RuntimeRef:    "echo",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions: 1,
			CleanupTimeoutSeconds: 1,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          25,
			},
		},
	}); err != nil {
		t.Fatalf("create poolstore pool: %v", err)
	}
	coord, err := recycle.NewRecycleBoundaryCoordinator(recycle.RecycleBoundaryCoordinatorOptions{
		Client:      c,
		Namespace:   recycleNS,
		Pools:       pools,
		GracePeriod: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new recycle coordinator: %v", err)
	}
	t.Cleanup(coord.Stop)
	return coord
}

// recordingArmer records every OnRecycling (the missing-report timeout arm) so
// the test can assert the recycle branch armed exactly one timer keyed on the
// pod name, and the retire path armed none.
type recordingArmer struct {
	mu    sync.Mutex
	armed []string
}

func (a *recordingArmer) OnRecycling(podID string) {
	a.mu.Lock()
	a.armed = append(a.armed, podID)
	a.mu.Unlock()
}

func (a *recordingArmer) armedSnapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.armed))
	copy(out, a.armed)
	return out
}

// recycleFakeRuntime is a minimal RuntimeProcess for the adapter Server the
// binder's StartSession drives at Bind and Close tears down at Shutdown.
type recycleFakeRuntime struct{}

func (recycleFakeRuntime) Start(context.Context, string) error           { return nil }
func (recycleFakeRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (recycleFakeRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (recycleFakeRuntime) Close(context.Context, string) error           { return nil }
func (recycleFakeRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// newRecycleAdapter builds a real adapter Server wired for the recycle-scrub
// driver: a fake runtime, the recording scrub ops, an optional reporter, and a
// scrub-done hook the test waits on. A nil reporter leaves PodScrubReporter
// unset (the dropped-report case), so the driver withholds the report; passing
// the typed nil directly would make the interface field non-nil and panic.
func newRecycleAdapter(t *testing.T, ops *recycleScrubOps, reporter *recycleScrubReporter) (*adapter.Server, <-chan struct{}) {
	t.Helper()
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = recycleFakeRuntime{}
	srv.ScrubOps = ops
	if reporter != nil {
		srv.PodScrubReporter = reporter
	}
	done := make(chan struct{})
	var once sync.Once
	srv.SetScrubDoneHook(func() { once.Do(func() { close(done) }) })
	return srv, done
}

// bindRecyclingSession claims the seeded idle pod for a session-mode recycling
// pool through the real binder, carrying the §5.2 scrub-config fields on the
// bind request so the folded RecycleScrub reaches the adapter on release. It
// returns the live BindResult, whose claim is `bound` after Bind. cleanup are
// the deployer cleanupCommands the adapter runs in step 0.5 of the scrub; the
// success case passes commands that exit zero (real argv-mode executables), so
// the whole-pod scrub completes and reports PodScrubSucceeded.
func bindRecyclingSession(t *testing.T, binder *podsession.Binder, sessionID, profile string, cleanup []string) *podsession.BindResult {
	t.Helper()
	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool:                  "recycle-pool",
		SessionID:             sessionID,
		TenantID:              "acme",
		Runtime:               "echo",
		Recycle:               true,
		CleanupCommands:       cleanup,
		CleanupTimeoutSeconds: 30,
		ScrubProfile:          profile,
	})
	if err != nil {
		t.Fatalf("Bind recycling session: %v", err)
	}
	return res
}

func claimPhase(t *testing.T, c client.Client, sandbox string) string {
	t.Helper()
	var claim lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: recycleNS, Name: podclaim.ClaimName(sandbox)}, &claim); err != nil {
		if apierrors.IsNotFound(err) {
			return "<deleted>"
		}
		t.Fatalf("get claim for %s: %v", sandbox, err)
	}
	return claim.Status.Phase
}

// diagnosis: a failure means the §5.2 recycle path broke end to end. If the
// claim is not `recycling` after a clean release, Release did not patch the
// claim before signaling the scrub (the §3.4 patch-then-scrub ordering). If the
// adapter ran no scrub or emitted no ReportPodScrub, the folded RecycleScrub did
// not trigger the whole-pod scrub. If the reported outcome does not drive
// podscrub.Decide to `reserved`, the disposition is not consuming the scrub
// result. This pins the full fold poolstore sessionPolicy → mirror → PoolMatch
// → BindResult → RecycleScrub → adapter scrub → ReportPodScrub → reuse.
// spec: 5.2 (whole-pod scrub trigger, recycle lifecycle), 4.6.3 (scrub
// disposition), 3.4 (recycle disposition, patch-then-scrub)
func TestRecyclePathScrubReportedReuses_spec_5_2(t *testing.T) {
	c := recycleCluster(t)
	ops := &recycleScrubOps{}
	reporter := newRecycleScrubReporter()
	srv, scrubDone := newRecycleAdapter(t, ops, reporter)
	binder, armer := recycleBinder(c, recycleAdapterDialer(t, srv))

	// The cleanup command is a real argv-mode executable that exits zero, so
	// the whole-pod scrub runs it and still reports success; the folded config
	// is asserted on the wire path by the tier-2 test.
	res := bindRecyclingSession(t, binder, "sess-reuse", "standard", []string{"true"})

	// The folded scrub config rode the BindResult from the bind request.
	if res.CleanupTimeoutSeconds != 30 || len(res.CleanupCommands) != 1 || res.CleanupCommands[0] != "true" {
		t.Fatalf("BindResult scrub config = %d / %v, want the bind-request cleanup config carried through",
			res.CleanupTimeoutSeconds, res.CleanupCommands)
	}
	// After Bind the claim is `bound`; the recycle patch has not run yet.
	if got := claimPhase(t, c, res.SandboxName); got != string(claimstate.Bound) {
		t.Fatalf("claim phase after Bind = %q, want bound", got)
	}

	// A clean release patches the claim bound → recycling, arms the
	// missing-report timeout, and sends the §4.7 recycle Shutdown that triggers
	// the adapter whole-pod scrub.
	if err := binder.Release(context.Background(), res, "completed"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := claimPhase(t, c, res.SandboxName); got != string(claimstate.Recycling) {
		t.Fatalf("claim phase after clean recycle Release = %q, want recycling (§3.4 patch-then-scrub)", got)
	}
	if armed := armer.armedSnapshot(); len(armed) != 1 || armed[0] != "sbx-r" {
		t.Fatalf("missing-report timers armed = %v, want [sbx-r]", armed)
	}

	// Wait for the async scrub to report.
	select {
	case <-reporter.reported:
	case <-time.After(5 * time.Second):
		t.Fatal("adapter did not emit ReportPodScrub within 5s")
	}
	<-scrubDone

	// The adapter ran the whole-pod scrub host operations.
	killed, verified, _ := ops.ranScrub()
	if !killed || !verified {
		t.Errorf("scrub host ops ran killed=%v verified=%v, want both true", killed, verified)
	}
	reps := reporter.snapshot()
	if len(reps) != 1 {
		t.Fatalf("ReportPodScrub count = %d, want 1", len(reps))
	}
	if reps[0].podID != "sbx-r" {
		t.Errorf("reported pod_id = %q, want sbx-r (the folded SandboxName the timer keys on)", reps[0].podID)
	}
	if reps[0].outcome != gatewaycontrol.PodScrubSucceeded {
		t.Errorf("reported outcome = %v, want PodScrubSucceeded", reps[0].outcome)
	}

	// The gateway consumer side maps the reported outcome onto the §6.2
	// disposition. A succeeded scrub on a schedulable host reuses the pod: a
	// non-preConnect recycling pod is held in `reserved` for its pinned tenant.
	disp := podscrub.Decide(podscrub.Inputs{
		Scrub:             scrubResultFor(reps[0].outcome),
		OnCleanupFailure:  podscrub.OnCleanupWarn,
		MaxSessionsPerPod: 25,
		SessionsServed:    1,
		HostSchedulable:   true,
	})
	if disp.Retire {
		t.Fatalf("succeeded scrub retired the pod (reason %q), want reuse", disp.Reason)
	}
	if disp.NextPhase != state.Reserved {
		t.Errorf("disposition NextPhase = %v, want Reserved (non-preConnect reuse)", disp.NextPhase)
	}
	if disp.Reason != podscrub.ReasonReuse {
		t.Errorf("disposition reason = %q, want reuse", disp.Reason)
	}
}

// TestRecyclePathDroppedReportRetires_spec_3_4 asserts the fail-closed backstop
// end to end: when the adapter's scrub report never arrives, the real §3.4
// missing-report timeout coordinator retires the pod rather than leaving it
// stuck in `recycling`. The adapter runs the scrub but has no PodScrubReporter,
// so it withholds the report (the dropped-report case); the coordinator's timer
// fires (driven immediately here via an injected AfterFunc) and patches the
// still-`recycling` claim to the `failed` terminal.
//
// diagnosis: a failure means the missing-report timeout stopped retiring a pod
// whose scrub never reported, so a hung or reporterless adapter would leave the
// pod stuck in `recycling` — the availability backstop the §3.4 timeout exists
// to provide.
// spec: 3.4 (gateway-side missing-report timeout), 5.2 (recycle lifecycle),
// 6.2 (retire on missing report)
func TestRecyclePathDroppedReportRetires_spec_3_4(t *testing.T) {
	c := recycleCluster(t)
	ops := &recycleScrubOps{}
	// A reporterless adapter drops the report: the scrub runs but nothing
	// reaches the coordinator, so the missing-report timeout is the only path
	// off `recycling`.
	srv, scrubDone := newRecycleAdapter(t, ops, nil)

	// Wire the production §3.4 coordinator against the same cluster. A pod
	// object carrying the pool label plus a poolstore pool with a 1s
	// cleanupTimeoutSeconds and a tiny grace bound the real missing-report timer
	// to about a second, so the test exercises the genuine timer rather than a
	// stubbed one (the coordinator's AfterFunc seam is package-private).
	coord := newRecycleCoordinator(t, c)
	binder, _ := recycleBinder(c, recycleAdapterDialer(t, srv))
	binder.RecycleBoundary = coord

	res := bindRecyclingSession(t, binder, "sess-drop", "standard", []string{"true"})
	if err := binder.Release(context.Background(), res, "completed"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// The scrub ran (the driver withheld the report because no reporter is
	// wired), so nothing cancels the coordinator's missing-report timer.
	<-scrubDone

	// The coordinator retires the pod when the timer fires: the still-`recycling`
	// claim advances to the fail-closed `failed` terminal. Poll for the
	// transition within a bound that comfortably exceeds the ~1s timer.
	deadline := time.Now().Add(6 * time.Second)
	for {
		got := claimPhase(t, c, res.SandboxName)
		if got == string(claimstate.Failed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claim phase after missing-report timeout = %q, want failed (fail-closed retire)", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRecyclePathVMRestartNilRestarterRetires_spec_5_2 asserts the fail-closed
// posture for a vm-restart pool with no concrete VMRestarter (allowCrossTenant
// reuse under the default warn policy): the adapter runs the scrub, hits
// scrub.ErrNoRestarter at step 7, and WITHHOLDS the ReportPodScrub. No report
// arrives, so the missing-report timeout retires the pod rather than
// podscrub.Decide reusing it across tenants without the guest VM restart the
// profile exists to perform. This pins that the withheld report is fail-closed:
// emitting any outcome under warn would cross-tenant-reuse the pod.
//
// diagnosis: a failure means a vm-restart pool without a concrete restarter
// either emitted a report (which the default warn policy would reuse, a
// cross-tenant fail-open regression) or the disposition reused a pod whose scrub
// never reported.
// spec: 5.2 (vm-restart step 7, cross-tenant reuse, withheld report is
// fail-closed), 6.2 (retire on missing report)
func TestRecyclePathVMRestartNilRestarterRetires_spec_5_2(t *testing.T) {
	c := recycleCluster(t)
	ops := &recycleScrubOps{}
	reporter := newRecycleScrubReporter()
	srv, scrubDone := newRecycleAdapter(t, ops, reporter)
	// No VMRestarter is wired: scrub.Run returns ErrNoRestarter and the driver
	// withholds the report.
	binder, armer := recycleBinder(c, recycleAdapterDialer(t, srv))

	res := bindRecyclingSession(t, binder, "sess-vmrestart", "vm-restart", []string{"true"})
	if res.ScrubProfile != "vm-restart" {
		t.Fatalf("BindResult.ScrubProfile = %q, want vm-restart", res.ScrubProfile)
	}
	if err := binder.Release(context.Background(), res, "completed"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := claimPhase(t, c, res.SandboxName); got != string(claimstate.Recycling) {
		t.Fatalf("claim phase = %q, want recycling", got)
	}
	if armed := armer.armedSnapshot(); len(armed) != 1 {
		t.Fatalf("armed timers = %v, want exactly one", armed)
	}

	// The scrub goroutine finishes; the driver withheld the report on
	// ErrNoRestarter.
	<-scrubDone
	if reps := reporter.snapshot(); len(reps) != 0 {
		t.Fatalf("vm-restart nil-restarter emitted %d reports, want 0 (withheld, fail-closed)", len(reps))
	}

	// This is why the report is withheld: under the DEFAULT warn policy a
	// REPORTED failed scrub REUSES the pod (returns it to the pool), which on an
	// allowCrossTenantReuse pool hands the pod to the next tenant without the
	// guest VM restart the vm-restart profile exists to perform — a fail-open
	// cross-tenant regression. Pin that the warn-policy reuse is exactly the
	// disposition the withhold avoids.
	failOpen := podscrub.Decide(podscrub.Inputs{
		Scrub:             podscrub.ScrubFailed,
		OnCleanupFailure:  podscrub.OnCleanupWarn, // the default policy
		MaxSessionsPerPod: 25,
		SessionsServed:    1,
		HostSchedulable:   true,
	})
	if failOpen.Retire {
		t.Fatal("test premise broken: a warn-policy failed scrub retired; the withhold must be what prevents reuse")
	}
	if failOpen.Reason != podscrub.ReasonReuse {
		t.Fatalf("warn-policy failed scrub disposition = %q, want reuse (the fail-open the withhold avoids)", failOpen.Reason)
	}

	// The actual path: no report cancels the missing-report timer, so the §3.4
	// timeout retires the pod (the fail-closed terminal), rather than the
	// warn-policy reuse above.
	timeoutRetire := podscrub.Decide(podscrub.Inputs{
		Scrub:             podscrub.ScrubFailed, // the timeout's fail-closed terminal
		OnCleanupFailure:  podscrub.OnCleanupFail,
		MaxSessionsPerPod: 25,
		SessionsServed:    1,
		HostSchedulable:   true,
	})
	if !timeoutRetire.Retire {
		t.Fatal("vm-restart nil-restarter pod was not retired, want a fail-closed retire (no cross-tenant reuse)")
	}
}

// TestRecyclePathNilScrubOpsWithholdsReportAndRetires_spec_5_2 pins the
// production fail-closed behavior when the adapter binary fails to wire
// ScrubOps. Before the fix, cmd/lenny-adapter never assigned adapterSrv.ScrubOps,
// so a session-mode recycle ran scrub.Run with nil Ops, which returns a nil-Ops
// error the driver mapped to PodScrubFailed and REPORTED. Under the default warn
// policy podscrub.Decide reuses the pod for the next session with no scrub having
// run (a between-session isolation regression, strictly worse than the pre-change
// posture where no adapter reported and the timeout always retired). The driver
// now withholds the report on a nil ScrubOps, so no PodScrubFailed reaches the
// gateway and the missing-report timeout retires the pod fail-closed. This test
// wires a reporter but leaves ScrubOps nil: it asserts zero reports and a
// fail-closed retire, and would see a PodScrubFailed report (and a warn-policy
// reuse) against the pre-fix code.
//
// diagnosis: a failure means a production adapter with ScrubOps unwired reported
// PodScrubFailed and let the gateway reuse the pod under warn without a scrub —
// the exact fail-open isolation regression this fix closes.
// spec: 5.2 (whole-pod scrub, fail-closed on a wiring gap), 3.4 (missing-report
// timeout), 6.2 (retire on missing report)
func TestRecyclePathNilScrubOpsWithholdsReportAndRetires_spec_5_2(t *testing.T) {
	c := recycleCluster(t)
	reporter := newRecycleScrubReporter()

	// The pre-fix production state: a real adapter Server whose ScrubOps was
	// never wired. A reporter IS wired, so a PodScrubFailed would reach the
	// gateway if the driver emitted one.
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = recycleFakeRuntime{}
	srv.ScrubOps = nil // never wired: the wiring gap the fix guards against
	srv.PodScrubReporter = reporter
	scrubDone := make(chan struct{})
	var once sync.Once
	srv.SetScrubDoneHook(func() { once.Do(func() { close(scrubDone) }) })

	coord := newRecycleCoordinator(t, c)
	binder, _ := recycleBinder(c, recycleAdapterDialer(t, srv))
	binder.RecycleBoundary = coord

	res := bindRecyclingSession(t, binder, "sess-nilops", "standard", []string{"true"})
	if err := binder.Release(context.Background(), res, "completed"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// The driver withheld the report because ScrubOps is nil; the scrub
	// goroutine still returns and fires the done hook.
	<-scrubDone
	if reps := reporter.snapshot(); len(reps) != 0 {
		t.Fatalf("nil-ScrubOps recycle emitted %d reports, want 0 (withheld fail-closed); got %+v", len(reps), reps)
	}

	// No report cancels the missing-report timer, so the §3.4 timeout retires
	// the pod: the still-`recycling` claim advances to the fail-closed `failed`
	// terminal rather than being reused without a scrub.
	deadline := time.Now().Add(6 * time.Second)
	for {
		got := claimPhase(t, c, res.SandboxName)
		if got == string(claimstate.Failed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claim phase after nil-ScrubOps recycle = %q, want failed (fail-closed retire, no reuse without scrub)", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRecyclePathFailedDispositionRetires_spec_6_2 asserts a failed session on a
// recycle-enabled pool takes the retire path: Release sends a PLAIN Shutdown
// (no RecycleScrub), the adapter runs no scrub and emits no ReportPodScrub, the
// claim is never patched to recycling, and no missing-report timer is armed.
// This is the §6.2 rule that a failed/crashed session always retires its pod
// regardless of recycle settings, and the C-A3 regression guard: keying the
// wire call on BindResult.Recycle alone would fire the recycle scrub here.
//
// diagnosis: a failure means a crashed session on a recycling pool triggered the
// whole-pod scrub-and-reuse path instead of retiring the pod — a fail-open
// crash-path regression §6.2 forbids.
// spec: 6.2 (failed/crashed session always retires), 5.2 (recycle disposition),
// 4.7 (Shutdown recycle disposition)
func TestRecyclePathFailedDispositionRetires_spec_6_2(t *testing.T) {
	c := recycleCluster(t)
	ops := &recycleScrubOps{}
	reporter := newRecycleScrubReporter()
	srv, _ := newRecycleAdapter(t, ops, reporter)
	binder, armer := recycleBinder(c, recycleAdapterDialer(t, srv))

	res := bindRecyclingSession(t, binder, "sess-failed", "standard", []string{"true"})
	if !res.Recycle {
		t.Fatal("BindResult.Recycle = false, want true (recycle-enabled pool)")
	}

	// A failed disposition takes the retire path even though Recycle is true.
	if err := binder.Release(context.Background(), res, "failed"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// The claim is deleted (drained), never patched to recycling.
	if got := claimPhase(t, c, res.SandboxName); got != "<deleted>" {
		t.Errorf("claim phase after failed Release = %q, want <deleted> (retire drains the claim, never patches recycling)", got)
	}
	// No missing-report timer is armed on the retire path.
	if armed := armer.armedSnapshot(); len(armed) != 0 {
		t.Errorf("failed-disposition Release armed timers %v, want none", armed)
	}
	// Give any (erroneous) async scrub a beat to run, then assert none did and
	// no report was emitted. The retire path never triggers startPodScrub, so
	// the ops recorded no kill and the reporter recorded no report.
	time.Sleep(200 * time.Millisecond)
	if killed, verified, _ := ops.ranScrub(); killed || verified {
		t.Errorf("failed-disposition Release ran the whole-pod scrub (killed=%v verified=%v), want none (§6.2 retire)", killed, verified)
	}
	if reps := reporter.snapshot(); len(reps) != 0 {
		t.Errorf("failed-disposition Release emitted %d ReportPodScrub, want 0", len(reps))
	}
}

// scrubResultFor maps the adapter's reported PodScrubOutcome onto the §6.2
// podscrub.ScrubResult the disposition decider consumes. It mirrors the
// gateway leasecontrol handler's translation so the tier-4 flow drives Decide
// with the same input the production consumer would.
func scrubResultFor(o gatewaycontrol.PodScrubOutcome) podscrub.ScrubResult {
	if o == gatewaycontrol.PodScrubSucceeded {
		return podscrub.ScrubSucceeded
	}
	return podscrub.ScrubFailed
}
