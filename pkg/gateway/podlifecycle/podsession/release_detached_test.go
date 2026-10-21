// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// shutdownMode selects how releaseAdapter answers a Shutdown.
type shutdownMode int

const (
	shutdownClean shutdownMode = iota
	// shutdownBlocks waits until the request's context ends, the adapter
	// that outlasts its caller.
	shutdownBlocks
	shutdownFails
)

// releaseAdapter is an adapter fake that serves only Shutdown and records
// each request's recycle disposition and the instant it answered.
type releaseAdapter struct {
	adapterv1.UnimplementedAdapterServer

	mode shutdownMode

	mu         sync.Mutex
	recycles   []bool
	returnedAt time.Time
}

func (a *releaseAdapter) Shutdown(ctx context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	a.mu.Lock()
	a.recycles = append(a.recycles, req.GetRecycle() != nil)
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.returnedAt = time.Now()
		a.mu.Unlock()
	}()
	switch a.mode {
	case shutdownBlocks:
		<-ctx.Done()
		return nil, ctx.Err()
	case shutdownFails:
		return nil, errors.New("adapter unreachable")
	}
	return &adapterv1.ShutdownResponse{ExitedCleanly: true}, nil
}

func (a *releaseAdapter) sent() []bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]bool(nil), a.recycles...)
}

// dialReleaseAdapter serves a over bufconn and returns a connected client.
func dialReleaseAdapter(t *testing.T, a *releaseAdapter) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, a)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	return cl
}

// claimWriteFaults scripts the release's claim writes through a client
// interceptor and records the context each write received. A fault "before"
// fails without applying; a fault "after" applies the write and then reports
// an error.
type claimWriteFaults struct {
	getSandboxErr bool
	deleteBefore  bool
	deleteAfter   bool
	patchBefore   bool
	patchAfter    bool

	mu             sync.Mutex
	sandboxGetDL   time.Time
	sandboxGetHasD bool
	endedWrites    []string
}

func (f *claimWriteFaults) noteCtx(op string, ctx context.Context) {
	if ctx.Err() != nil {
		f.mu.Lock()
		f.endedWrites = append(f.endedWrites, op)
		f.mu.Unlock()
	}
}

func (f *claimWriteFaults) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*lennyv1.Sandbox); ok {
				f.mu.Lock()
				f.sandboxGetDL, f.sandboxGetHasD = ctx.Deadline()
				f.mu.Unlock()
				f.noteCtx("get sandbox", ctx)
				if f.getSandboxErr {
					return errors.New("apiserver unavailable")
				}
			}
			return cl.Get(ctx, key, obj, opts...)
		},
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); !ok {
				return cl.Delete(ctx, obj, opts...)
			}
			f.noteCtx("delete claim", ctx)
			if f.deleteBefore {
				return errors.New("apiserver unavailable")
			}
			if err := cl.Delete(ctx, obj, opts...); err != nil {
				return err
			}
			if f.deleteAfter {
				return errors.New("apiserver timed out after the delete applied")
			}
			return nil
		},
		SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); !ok {
				return cl.SubResource(sub).Patch(ctx, obj, patch, opts...)
			}
			f.noteCtx("patch claim", ctx)
			if f.patchBefore {
				return errors.New("apiserver unavailable")
			}
			if err := cl.SubResource(sub).Patch(ctx, obj, patch, opts...); err != nil {
				return err
			}
			if f.patchAfter {
				return errors.New("apiserver timed out after the patch applied")
			}
			return nil
		},
	}
}

// seedBoundPod seeds an idle Sandbox and its per-pod claim in `bound`.
func seedBoundPod(t *testing.T, c client.Client, name string) {
	t.Helper()
	seedObjects(t, c, idleSandbox(name, "10.244.9.9"), &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(name), Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: name, TenantID: "acme"},
	})
	if err := podclaim.WriteBoundStatus(context.Background(), c, testNS, podclaim.ClaimName(name)); err != nil {
		t.Fatalf("write bound on %s: %v", name, err)
	}
}

// claimPhase returns the claim's binding state, or "gone" when it is absent.
func claimPhase(t *testing.T, c client.Client, name string) string {
	t.Helper()
	var sc lennyv1.SandboxClaim
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: podclaim.ClaimName(name)}, &sc)
	if apierrors.IsNotFound(err) {
		return "gone"
	}
	if err != nil {
		t.Fatalf("get claim of %s: %v", name, err)
	}
	return sc.Status.Phase
}

// releaseCase is one Binder.Release run against a seeded pod.
type releaseCase struct {
	name        string
	recycle     bool
	disposition string
	mode        shutdownMode
	faults      claimWriteFaults
	callerCtx   func() (context.Context, context.CancelFunc)
	preDelete   bool
	wantErr     bool
	wantPhase   string
	wantArmed   bool
	wantSent    []bool
}

func runReleaseCase(t *testing.T, base client.WithWatch, idx int, tc *releaseCase) (*releaseAdapter, *claimWriteFaults) {
	t.Helper()
	name := fmt.Sprintf("sbx-rel-%d", idx)
	seedBoundPod(t, base, name)
	if tc.preDelete {
		if err := podclaim.DeleteClaim(context.Background(), base, testNS, name); err != nil {
			t.Fatalf("pre-delete: %v", err)
		}
	}
	a := &releaseAdapter{mode: tc.mode}
	faults := &tc.faults
	b := newBinder(interceptor.NewClient(base, faults.funcs()), nil)
	armer := &fakeRecycleBoundary{}
	b.RecycleBoundary = armer
	res := &podsession.BindResult{SessionID: "sess-1", TenantID: "acme", SandboxName: name, Adapter: dialReleaseAdapter(t, a), Recycle: tc.recycle}

	ctx, cancel := context.WithCancel(context.Background())
	if tc.callerCtx != nil {
		ctx, cancel = tc.callerCtx()
	}
	defer cancel()
	err := b.Release(ctx, res, tc.disposition)
	if (err != nil) != tc.wantErr {
		t.Fatalf("Release error = %v, want error %v", err, tc.wantErr)
	}
	if got := claimPhase(t, base, name); got != tc.wantPhase {
		t.Errorf("claim = %q, want %q", got, tc.wantPhase)
	}
	if armed := len(armer.armed) == 1; armed != tc.wantArmed {
		t.Errorf("missing-report timeouts armed = %v, want armed %v", armer.armed, tc.wantArmed)
	}
	if got := a.sent(); fmt.Sprint(got) != fmt.Sprint(tc.wantSent) {
		t.Errorf("Shutdown requests (recycle flag each) = %v, want %v", got, tc.wantSent)
	}
	faults.mu.Lock()
	ended := faults.endedWrites
	faults.mu.Unlock()
	if len(ended) != 0 {
		t.Errorf("claim writes on an ended context: %v, want every claim write detached", ended)
	}
	return a, faults
}

var (
	recycling = string(claimstate.Recycling)
	bound     = string(claimstate.Bound)
)

func alreadyCancelled() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx, cancel
}

// TestBinderReleaseClaimWritesOutlastCallerContext_spec_5_2 pins the detached
// claim writes of Binder.Release: when the adapter Shutdown runs to the
// caller's deadline, the retire path still reads the Sandbox and deletes the
// claim on a context created after the Shutdown returned, and a caller whose
// context already ended still has its claim deleted or patched to
// `recycling`. The executor removed the binding before the call, so no path
// would repeat a release lost to the caller's context.
//
// spec: §5.2 (Recycle lifecycle), §4.6.3 (SandboxClaim: a release that ends
// the occupancy without reuse)
//
// diagnosis: a failure means a release whose caller's context ended during
// or before the best-effort Shutdown leaves the claim to the orphan GC,
// holding a pod whose session ended.
func TestBinderReleaseClaimWritesOutlastCallerContext_spec_5_2(t *testing.T) {
	base := k8sClient(t)

	t.Run("retire path, Shutdown outlasts the caller's deadline", func(t *testing.T) {
		tc := &releaseCase{
			disposition: "completed", mode: shutdownBlocks,
			callerCtx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 300*time.Millisecond)
			},
			wantPhase: "gone", wantSent: []bool{false},
		}
		a, faults := runReleaseCase(t, base, 0, tc)
		a.mu.Lock()
		returned := a.returnedAt
		a.mu.Unlock()
		faults.mu.Lock()
		dl, has := faults.sandboxGetDL, faults.sandboxGetHasD
		faults.mu.Unlock()
		if !has {
			t.Fatalf("the retire path's Sandbox Get carried no deadline, want the detached cleanup bound")
		}
		if min := returned.Add(podsession.SlotCleanupFloorForTest); dl.Before(min) {
			t.Errorf("Sandbox Get deadline = %v, want at or after %v (the Shutdown's return plus the per-slot cleanup floor)", dl, min)
		}
	})
	t.Run("retire path, caller context already ended", func(t *testing.T) {
		runReleaseCase(t, base, 1, &releaseCase{
			disposition: "failed", recycle: true, callerCtx: alreadyCancelled,
			// The ended context cuts the Shutdown before it reaches the
			// adapter.
			wantPhase: "gone",
		})
	})
	t.Run("recycle path, caller context already ended", func(t *testing.T) {
		runReleaseCase(t, base, 2, &releaseCase{
			disposition: "completed", recycle: true, callerCtx: alreadyCancelled,
			// The ended context cuts the recycle Shutdown, which leaves the
			// pod to the armed missing-report timeout.
			wantPhase: recycling, wantArmed: true,
		})
	})
}

// TestBinderReleaseFailureOutcomes_spec_5_2 pins each failure outcome of
// Binder.Release on both dispositions: a recycling patch that fails, or that
// applies and then errors, returns the error before the timeout is armed and
// before any Shutdown; a retire path whose Sandbox read or claim delete fails
// returns the error with the claim left; a delete that applied and then
// errored returns the error with the claim gone; a claim already gone fails
// the recycling patch's read and is a no-op for the retire delete; and a
// failed Shutdown is ignored on both paths.
//
// spec: §5.2 (Recycle lifecycle), §4.6.3 (SandboxClaim binding state), §6.2
// (a failed session retires its pod)
//
// diagnosis: a failure means a release step's failure leaves the claim in a
// state other than the one the release reports, or arms the missing-report
// timeout for a claim that never reached `recycling`.
func TestBinderReleaseFailureOutcomes_spec_5_2(t *testing.T) {
	base := k8sClient(t)
	cases := []releaseCase{
		{
			name: "recycling patch fails before it applies", recycle: true, disposition: "completed",
			faults:  claimWriteFaults{patchBefore: true},
			wantErr: true, wantPhase: bound,
		},
		{
			name: "recycling patch applies and then errors", recycle: true, disposition: "completed",
			faults:  claimWriteFaults{patchAfter: true},
			wantErr: true, wantPhase: recycling,
		},
		{
			name: "retire Sandbox read fails", disposition: "completed",
			faults:  claimWriteFaults{getSandboxErr: true},
			wantErr: true, wantPhase: bound, wantSent: []bool{false},
		},
		{
			name: "retire claim delete fails before it applies", disposition: "completed",
			faults:  claimWriteFaults{deleteBefore: true},
			wantErr: true, wantPhase: bound, wantSent: []bool{false},
		},
		{
			name: "retire claim delete applies and then errors", disposition: "completed",
			faults:  claimWriteFaults{deleteAfter: true},
			wantErr: true, wantPhase: "gone", wantSent: []bool{false},
		},
		{
			name: "recycle path, claim already gone", recycle: true, disposition: "completed", preDelete: true,
			wantErr: true, wantPhase: "gone",
		},
		{
			name: "retire path, claim already gone", disposition: "completed", preDelete: true, mode: shutdownClean,
			wantPhase: "gone", wantSent: []bool{false},
		},
		{
			name: "retire path, plain Shutdown fails", disposition: "failed", mode: shutdownFails,
			wantPhase: "gone", wantSent: []bool{false},
		},
		{
			name: "recycle path, recycle Shutdown fails", recycle: true, disposition: "completed", mode: shutdownFails,
			wantPhase: recycling, wantArmed: true, wantSent: []bool{true},
		},
	}
	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			runReleaseCase(t, base, 10+i, tc)
		})
	}
}
