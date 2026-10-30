// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// rollbackTestAllowance is the release allowance the rollback tests set on the
// server, short enough that a blocked release returns quickly and long enough
// that a gRPC round trip over bufconn completes inside it.
const rollbackTestAllowance = 2 * time.Second

// ctxObservation is what a fake observed about the context a rollback handed
// it: whether the context had already ended and how long its deadline left.
type ctxObservation struct {
	err         error
	hasDeadline bool
	remaining   time.Duration
}

func observeCtx(ctx context.Context) ctxObservation {
	obs := ctxObservation{err: ctx.Err()}
	if dl, ok := ctx.Deadline(); ok {
		obs.hasDeadline = true
		obs.remaining = time.Until(dl)
	}
	return obs
}

// ctxRecorder collects the context observations of one fake.
type ctxRecorder struct {
	mu  sync.Mutex
	obs []ctxObservation
}

func (r *ctxRecorder) record(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.obs = append(r.obs, observeCtx(ctx))
}

func (r *ctxRecorder) snapshot() []ctxObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ctxObservation(nil), r.obs...)
}

// rollbackFakeAdapter is an adapter gRPC server whose Shutdown records the
// context the gateway's call arrived with. When block is set, Shutdown waits
// for that context to end, standing in for an adapter that never answers.
type rollbackFakeAdapter struct {
	adapterv1.UnimplementedAdapterServer
	shutdowns ctxRecorder
	block     bool
}

func (a *rollbackFakeAdapter) Shutdown(ctx context.Context, _ *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	a.shutdowns.record(ctx)
	if a.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &adapterv1.ShutdownResponse{ExitedCleanly: true}, nil
}

// dialRollbackFakeAdapter serves fa over bufconn and returns a connected
// adapter client, the live connection a BindResult carries.
func dialRollbackFakeAdapter(t *testing.T, fa *rollbackFakeAdapter) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, fa)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial fake adapter: %v", err)
	}
	return cl
}

// rollbackFakeCluster is a fake controller-runtime client recording the
// context of every Get and Delete the binder's claim writes issue.
func rollbackFakeCluster(t *testing.T, rec *ctxRecorder) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme lennyv1: %v", err)
	}
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	return interceptor.NewClient(base, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			rec.record(ctx)
			return c.Get(ctx, key, obj, opts...)
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			rec.record(ctx)
			return c.Delete(ctx, obj, opts...)
		},
	})
}

// recordingReleaseLeaseStore is the fakeLeaseStore with its Release context
// recorded.
type recordingReleaseLeaseStore struct {
	*fakeLeaseStore
	releases ctxRecorder
}

func (f *recordingReleaseLeaseStore) Release(ctx context.Context, tenantID, sessionID, holder string) error {
	f.releases.record(ctx)
	return f.fakeLeaseStore.Release(ctx, tenantID, sessionID, holder)
}

// rollbackTestServer builds a server whose pod binder talks to the recording
// cluster and whose release allowance is allowance.
func rollbackTestServer(t *testing.T, cluster client.Client, leases *recordingReleaseLeaseStore, allowance time.Duration) *Server {
	t.Helper()
	opts := Options{
		DevTenantHeader:            true,
		PodBinder:                  &podsession.Binder{Client: cluster, Namespace: "lenny-agents"},
		ReplicaID:                  "replica-a",
		StreamFailureReportTimeout: allowance,
	}
	if leases != nil {
		opts.CoordinationLeaseStore = leases
	}
	return New(memstore.New(), opts)
}

// cancelledCtx returns a context that has already ended, the request context
// of a client that disconnected before the rollback ran.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// assertDetachedWithinAllowance fails unless every observation saw a live
// context whose deadline lies within allowance.
func assertDetachedWithinAllowance(t *testing.T, what string, obs []ctxObservation, allowance time.Duration) {
	t.Helper()
	if len(obs) == 0 {
		t.Fatalf("%s was never reached: the rollback did not run past the caller's cancellation", what)
	}
	for i, o := range obs {
		if o.err != nil {
			t.Errorf("%s call %d received a done context (%v), want one detached from the caller's cancellation", what, i, o.err)
		}
		if !o.hasDeadline {
			t.Errorf("%s call %d received a context with no deadline, want one bounded by the release allowance", what, i)
			continue
		}
		if o.remaining <= 0 || o.remaining > allowance {
			t.Errorf("%s call %d deadline left %v, want within the release allowance %v", what, i, o.remaining, allowance)
		}
	}
}

// spec: §7.1 (Atomicity of session creation (steps 2–8)), §4.6.1 (Orphaned
// SandboxClaim detection)
// diagnosis: rollbackBinding must release a bound pod even when the request
// context that ran the create has already ended. The exclusive release goes
// through Binder.Release and the concurrent-slot release through ReleaseSlot;
// both send the adapter Shutdown on the context rollbackBinding hands them. A
// failure here means the rollback ran on the caller's cancelled context, so
// the Shutdown never left the gateway and the pod stayed claimed until the
// orphan-claim GC.
func TestRollbackBindingDetachesFromCancelledCaller_spec_7_1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		slotID string
	}{
		{name: "exclusive binding releases through Release"},
		{name: "concurrent slot releases through ReleaseSlot", slotID: "slot-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var claimWrites ctxRecorder
			srv := rollbackTestServer(t, rollbackFakeCluster(t, &claimWrites), nil, rollbackTestAllowance)
			fa := &rollbackFakeAdapter{}
			result := &podsession.BindResult{
				SessionID:   "sess-rollback",
				SandboxName: "sbx-1",
				SlotID:      tc.slotID,
				Adapter:     dialRollbackFakeAdapter(t, fa),
			}

			srv.rollbackBinding(cancelledCtx(), result)

			assertDetachedWithinAllowance(t, "adapter Shutdown", fa.shutdowns.snapshot(), rollbackTestAllowance)
			if tc.slotID == "" {
				// The retire path reads the Sandbox on the binder's own
				// detached context, which keeps the caller's values but
				// must not inherit its cancellation.
				for i, o := range claimWrites.snapshot() {
					if o.err != nil {
						t.Errorf("claim write %d received a done context (%v)", i, o.err)
					}
				}
			}
		})
	}
}

// spec: §7.1 (Atomicity of session creation (steps 2–8)), §4.6.1 (Orphaned
// SandboxClaim detection)
// diagnosis: rollbackClaim must delete the create-time claim even when the
// request context has already ended. A failure here means ReclaimClaimed saw
// a done context, so the claim delete failed and the warm pod stayed claimed
// until the orphan-claim GC.
func TestRollbackClaimDetachesFromCancelledCaller_spec_7_1(t *testing.T) {
	t.Parallel()
	var claimWrites ctxRecorder
	srv := rollbackTestServer(t, rollbackFakeCluster(t, &claimWrites), nil, rollbackTestAllowance)

	srv.rollbackClaim(cancelledCtx(), &podsession.ClaimResult{SandboxName: "sbx-1"}, "sess-rollback")

	obs := claimWrites.snapshot()
	if len(obs) == 0 {
		t.Fatal("ReclaimClaimed issued no claim delete: the rollback did not reach the binder")
	}
	for i, o := range obs {
		if o.err != nil {
			t.Errorf("claim delete %d received a done context (%v), want one detached from the caller's cancellation", i, o.err)
		}
		if !o.hasDeadline || o.remaining <= 0 {
			t.Errorf("claim delete %d received an unbounded or expired context (deadline set %v, left %v)", i, o.hasDeadline, o.remaining)
		}
	}
}

// spec: §7.1 (Atomicity of session creation (steps 2–8)), §4.6.1 (Orphaned
// SandboxClaim detection)
// diagnosis: releaseCoordinationLease runs beside rollbackBinding on the
// early-commit paths and must release the lease this replica acquired even
// when the request context has already ended. A failure here means the lease
// store saw a done context, the Release write failed, and the lease stayed
// held with no binding until its TTL.
func TestReleaseCoordinationLeaseDetachesFromCancelledCaller_spec_7_1(t *testing.T) {
	t.Parallel()
	leases := &recordingReleaseLeaseStore{fakeLeaseStore: newFakeLeaseStore()}
	leases.holders[leaseK("acme", "sess-rollback")] = "replica-a"
	var claimWrites ctxRecorder
	srv := rollbackTestServer(t, rollbackFakeCluster(t, &claimWrites), leases, rollbackTestAllowance)

	srv.releaseCoordinationLease(cancelledCtx(), "acme", "sess-rollback")

	assertDetachedWithinAllowance(t, "lease store Release", leases.releases.snapshot(), rollbackTestAllowance)
	if _, held := leases.holders[leaseK("acme", "sess-rollback")]; held {
		t.Error("coordination lease still held after the rollback release")
	}
}

// spec: §7.1 (Terminal pipeline context), §7.1 (Atomicity of session creation
// (steps 2–8))
// diagnosis: the detached rollback is bounded by the release allowance. An
// adapter that never answers the Shutdown must not hold the rollback, and the
// request that runs it, past that allowance. A failure here means the
// rollback ran on an unbounded context and blocked for as long as the
// adapter did.
func TestRollbackBindingBoundedByReleaseAllowance_spec_7_1(t *testing.T) {
	t.Parallel()
	const allowance = 200 * time.Millisecond
	var claimWrites ctxRecorder
	srv := rollbackTestServer(t, rollbackFakeCluster(t, &claimWrites), nil, allowance)
	fa := &rollbackFakeAdapter{block: true}
	result := &podsession.BindResult{
		SessionID:   "sess-blocked",
		SandboxName: "sbx-1",
		Adapter:     dialRollbackFakeAdapter(t, fa),
	}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		srv.rollbackBinding(context.Background(), result)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("rollbackBinding still blocked 10s after the release allowance elapsed")
	}
	if len(fa.shutdowns.snapshot()) == 0 {
		t.Fatal("adapter Shutdown was never reached")
	}
	if elapsed := time.Since(start); elapsed < allowance {
		t.Errorf("rollbackBinding returned after %v, before the %v allowance elapsed on a blocked Shutdown", elapsed, allowance)
	}
}
