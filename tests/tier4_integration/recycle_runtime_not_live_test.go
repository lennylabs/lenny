// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/podscrub"
)

// endedRecycleRuntime is a recycleFakeRuntime whose process has ended, so it
// reports that it cannot serve the pod's next session.
type endedRecycleRuntime struct{ recycleFakeRuntime }

func (endedRecycleRuntime) ServesNextSession() bool { return false }

// notLiveInspector resolves every pod to a recycling, non-preConnect pool on a
// schedulable host with generous limits, so only the runtime liveness can
// retire the pod.
type notLiveInspector struct{}

func (notLiveInspector) InspectForRecycle(context.Context, string) (leasecontrol.PodRecyclePolicy, bool, error) {
	return leasecontrol.PodRecyclePolicy{
		Pool: "recycle-pool", OnScrubFailure: podscrub.OnCleanupWarn,
		MaxScrubFailures: 3, MaxSessionsPerPod: 25, HostSchedulable: true, PodUptimeSeconds: 60,
	}, true, nil
}

// noopSessionRetirer satisfies the ScrubReporter's per-release seam, which the
// whole-pod scrub report never reaches.
type noopSessionRetirer struct{}

func (noopSessionRetirer) RetireOnSessionCount(context.Context, string, int) error { return nil }

// retireRecord is one Retire call the recording driver received.
type retireRecord struct {
	podID    string
	failed   bool
	reason   podscrub.RetireReason
	lifetime leasecontrol.PodLifetime
}

// recordingRetireDriver is a ClaimDispositionDriver that records every
// disposition the real ScrubReporter drives.
type recordingRetireDriver struct {
	mu       sync.Mutex
	retires  []retireRecord
	recycles int
}

func (d *recordingRetireDriver) Recycle(context.Context, string, bool, bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recycles++
	return nil
}

func (d *recordingRetireDriver) Retire(_ context.Context, podID string, failed, _ bool, reason podscrub.RetireReason, lifetime leasecontrol.PodLifetime, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retires = append(d.retires, retireRecord{podID: podID, failed: failed, reason: reason, lifetime: lifetime})
	return nil
}

func (d *recordingRetireDriver) snapshot() ([]retireRecord, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]retireRecord(nil), d.retires...), d.recycles
}

// scrubGatewayClient serves the real GatewayControl scrub handler, backed by
// the real ScrubReporter over driver, on a bufconn and returns the adapter's
// real GatewayControl client dialed to it.
func scrubGatewayClient(t *testing.T, driver *recordingRetireDriver) *gatewaycontrol.Client {
	t.Helper()
	counters := newPerReleaseCounterStore()
	counters.sessionsServed["sbx-r"] = 1
	reporter, err := leasecontrol.NewScrubReporter(leasecontrol.ScrubReporterOptions{
		Counters: counters, Ledger: perReleaseNoopLedger{}, SessionRetirer: noopSessionRetirer{},
		Inspector: notLiveInspector{}, Driver: driver,
	})
	if err != nil {
		t.Fatalf("NewScrubReporter: %v", err)
	}
	budgets := leasecontrol.NewMemoryBudgetSource()
	svc, err := leasecontrol.NewService(leasecontrol.Options{Budgets: budgets, Tenants: budgets, ScrubReports: reporter})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterGatewayControlServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpc.NewClient(
		"passthrough:///gwcontrol",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial gateway control: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return gatewaycontrol.New(conn)
}

// TestRecyclePathRuntimeNotLiveRetires_spec_5_2 drives the recycle boundary
// end to end with a runtime that cannot serve the pod's next session: the
// gateway binder releases a recycling session, the real adapter runs the
// whole-pod scrub and reports through its real GatewayControl client, and the
// real gateway handler and ScrubReporter retire the pod as a released drain
// with the non-counting runtime_not_live reason rather than reserving it.
//
// diagnosis: the adapter's report and the gateway disposition disagree, so a
// pod whose runtime is gone is reserved for a session whose start would fail,
// or the liveness fact is lost between the adapter and the disposition.
// spec: 5.2 (Pod retirement policy), 4.7 (ReportPodScrub), 3.4 (recycle
// disposition)
func TestRecyclePathRuntimeNotLiveRetires_spec_5_2(t *testing.T) {
	c := recycleCluster(t)
	ops := &recycleScrubOps{}
	srv, scrubDone := newRecycleAdapter(t, ops, nil)
	srv.Runtime = endedRecycleRuntime{}
	driver := &recordingRetireDriver{}
	srv.PodScrubReporter = scrubGatewayClient(t, driver)
	binder, _ := recycleBinder(c, recycleAdapterDialer(t, srv))

	res := bindRecyclingSession(t, binder, "sess-not-live", "standard", []string{"true"})
	if err := binder.Release(context.Background(), res, "completed"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	<-scrubDone

	retires, recycles := driver.snapshot()
	want := retireRecord{
		podID: "sbx-r", reason: podscrub.ReasonRuntimeNotLive,
		lifetime: leasecontrol.PodLifetime{SessionsServed: 1, UptimeSeconds: 60},
	}
	if len(retires) != 1 || retires[0] != want {
		t.Fatalf("retires = %+v, want [%+v]", retires, want)
	}
	if recycles != 0 {
		t.Errorf("recycles = %d, want 0 (the pod retired)", recycles)
	}
}
