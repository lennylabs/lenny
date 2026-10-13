// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/alicebob/miniredis/v2"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// errPostClaimHook is the error the post-claim hook cases return.
var errPostClaimHook = errors.New("post-claim hook: the resuming write found the row ended")

// postClaimEnv is one envtest cluster holding the idle pod sbx-1, a binder on
// it whose claim deletes and adapter dials are counted, and a recording
// credential assigner.
type postClaimEnv struct {
	kube     client.WithWatch
	binder   *podsession.Binder
	assigner *fakeAssigner
	deletes  *atomic.Int32
	dials    *atomic.Int32
	redis    *miniredis.Miniredis
}

// newPostClaimEnv starts the cluster with sbx-1 idle and wires the binder to
// dial through dial. A non-nil slot counter is wired so the concurrent paths
// run; an exclusive path never touches it.
func newPostClaimEnv(t *testing.T, dial func(string) (*adapterclient.Client, error)) *postClaimEnv {
	t.Helper()
	base := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	deletes := &atomic.Int32{}
	kube := interceptor.NewClient(base, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok {
				deletes.Add(1)
			}
			return c.Delete(ctx, obj, opts...)
		},
	})
	dials := &atomic.Int32{}
	counted := func(addr string) (*adapterclient.Client, error) {
		dials.Add(1)
		return dial(addr)
	}
	binder, mr := resumeSlotFixture(t, kube, counted)
	assigner := &fakeAssigner{numbered: true}
	binder.Credentials = assigner
	return &postClaimEnv{kube: base, binder: binder, assigner: assigner, deletes: deletes, dials: dials, redis: mr}
}

// activeSlots reads sbx-1's §5.2 occupancy counter.
func (e *postClaimEnv) activeSlots(t *testing.T) string {
	t.Helper()
	return resumeActiveSlots(t, e.redis, "sbx-1")
}

// assertNoCredentialRelease fails when the binder released any §4.9 lease,
// session-wide or by identifier.
func (e *postClaimEnv) assertNoCredentialRelease(t *testing.T) {
	t.Helper()
	if len(e.assigner.released) != 0 || len(e.assigner.leaseReleases) != 0 {
		t.Errorf("credential releases = session-wide %v, by id %v; want none", e.assigner.released, e.assigner.leaseReleases)
	}
}

// workingAdapterDialer serves a session-mode adapter able to bind and resume.
func workingAdapterDialer(t *testing.T) func(string) (*adapterclient.Client, error) {
	t.Helper()
	srv := adapter.New("adapter-test")
	srv.WorkspaceBase = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	return adapterDialer(t, srv)
}

// negotiateFailingDialer serves an adapter that implements no RPC, so the
// §15.5 NegotiateVersion handshake fails with Unimplemented.
func negotiateFailingDialer(t *testing.T) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, &unimplementedAdapter{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return bufconnDialer(lis)
}

type unimplementedAdapter struct {
	adapterv1.UnimplementedAdapterServer
}

// interceptedAdapterDialer serves srv and installs a unary client
// interceptor on every connection the binder opens.
func interceptedAdapterDialer(t *testing.T, srv *adapter.Server, icpt grpc.UnaryClientInterceptor) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return bufconnDialer(lis, grpc.WithUnaryInterceptor(icpt))
}

func bufconnDialer(lis *bufconn.Listener, extra ...grpc.DialOption) func(string) (*adapterclient.Client, error) {
	return func(string) (*adapterclient.Client, error) {
		opts := append([]grpc.DialOption{
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		}, extra...)
		return adapterclient.Dial("passthrough:///bufnet", opts...)
	}
}

// cancelOn returns an interceptor that, on the named RPC, cancels the
// request context and fails the call, which is a request context ending
// while the RPC is in flight.
func cancelOn(rpc string, cancel context.CancelFunc) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if strings.HasSuffix(method, "/"+rpc) {
			cancel()
			return status.Error(codes.Canceled, rpc+" cancelled with the request context")
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func postClaimBindReq() podsession.BindRequest {
	return podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan:            &adapterv1.WorkspacePlan{},
		CredentialPools: map[string]string{"anthropic": "claude-prod"},
	}
}

func postClaimSlotReq() podsession.SlotBindRequest {
	return podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 4, Plan: &adapterv1.WorkspacePlan{},
		CredentialPools: map[string]string{"anthropic": "claude-prod"},
	}
}

// postClaimPath is one binder entry point driven with a post-claim hook.
type postClaimPath struct {
	name string
	// slot is true for the paths that reserve a §5.2 slot.
	slot bool
	// concurrentAdapter selects the multi-slot adapter fake the slot bind
	// needs; the other paths use the session-mode adapter.
	concurrentAdapter bool
	call              func(ctx context.Context, b *podsession.Binder, hook func(context.Context) error) error
}

func postClaimPaths() []postClaimPath {
	return []postClaimPath{
		{name: "resume_exclusive", call: func(ctx context.Context, b *podsession.Binder, hook func(context.Context) error) error {
			req := resumeRequest(1)
			req.OnClaimed = hook
			res, err := b.Resume(ctx, req)
			if err == nil {
				res.Result.Adapter.Close()
			}
			return err
		}},
		{name: "resume_slot", slot: true, call: func(ctx context.Context, b *podsession.Binder, hook func(context.Context) error) error {
			req := resumeRequest(4)
			req.OnClaimed = hook
			res, err := b.Resume(ctx, req)
			if err == nil {
				res.Result.Adapter.Close()
			}
			return err
		}},
		{name: "bind", call: func(ctx context.Context, b *podsession.Binder, hook func(context.Context) error) error {
			req := postClaimBindReq()
			req.OnClaimed = hook
			res, err := b.Bind(ctx, req)
			if err == nil {
				res.Adapter.Close()
			}
			return err
		}},
		{name: "bind_slot", slot: true, concurrentAdapter: true, call: func(ctx context.Context, b *podsession.Binder, hook func(context.Context) error) error {
			req := postClaimSlotReq()
			req.OnClaimed = hook
			res, err := b.BindSlot(ctx, req)
			if err == nil {
				res.Adapter.Close()
			}
			return err
		}},
	}
}

func (p postClaimPath) dialer(t *testing.T) func(string) (*adapterclient.Client, error) {
	if p.concurrentAdapter {
		return concurrentAdapterDialer(t, newConcurrentAdapter())
	}
	return workingAdapterDialer(t)
}

// spec: 7.3 (Retry and Resume), 5.2 (atomic slot reservation)
// diagnosis: a binder path ran the post-claim hook at the wrong point. A hook
// that runs before the claim lets the resume driver write `resuming` for an
// attempt that holds no pod, and one that runs after the handshake lets a pod
// see an RPC from an attempt whose `resuming` write has not landed. On a
// concurrent pool the hook must also follow the slot reservation, so the
// write never names a pod whose capacity the attempt does not hold.
func TestPostClaimHookRunsAfterTheClaimAndBeforeTheHandshake_spec_7_3(t *testing.T) {
	for _, p := range postClaimPaths() {
		t.Run(p.name, func(t *testing.T) {
			e := newPostClaimEnv(t, p.dialer(t))
			var calls int
			hook := func(context.Context) error {
				calls++
				if !podClaimExists(t, e.kube, "sbx-1") {
					t.Error("the hook ran before the pod claim")
				}
				if p.slot {
					if got := e.activeSlots(t); got != "1" {
						t.Errorf("active_slots at the hook = %s, want 1 (the reservation precedes the hook)", got)
					}
				}
				if got := e.dials.Load(); got != 0 {
					t.Errorf("adapter dials before the hook = %d, want 0 (the hook precedes the handshake)", got)
				}
				return nil
			}
			if err := p.call(context.Background(), e.binder, hook); err != nil {
				t.Fatalf("binder call: %v", err)
			}
			if calls != 1 {
				t.Errorf("hook calls = %d, want 1", calls)
			}
		})
	}
}

// spec: 7.3 (Retry and Resume), 5.2 (slot retry releases the reservation)
// diagnosis: a post-claim hook error left the claim or the slot reservation
// behind, released it twice, sent the pod an RPC, revoked the session's
// credential leases, or came back wrapped. A leaked claim holds a warm pod
// until the orphan-claim GC; a double release under-counts a co-tenant's
// slot; a wrapped error is retried by the §5.2 slot retry policy or read as
// an exhaustion by the claim queue.
func TestPostClaimHookErrorReleasesTheClaimOnce_spec_7_3(t *testing.T) {
	for _, p := range postClaimPaths() {
		t.Run(p.name, func(t *testing.T) {
			e := newPostClaimEnv(t, p.dialer(t))
			err := p.call(context.Background(), e.binder, func(context.Context) error { return errPostClaimHook })
			if err != errPostClaimHook {
				t.Fatalf("binder err = %v, want the hook's own error unwrapped", err)
			}
			var sbe *podsession.SlotBindError
			if errors.As(err, &sbe) {
				t.Error("the hook error is carried in a *SlotBindError")
			}
			if podClaimExists(t, e.kube, "sbx-1") {
				t.Error("the per-pod claim survived the hook error")
			}
			if got := e.deletes.Load(); got != 1 {
				t.Errorf("claim deletes = %d, want exactly 1", got)
			}
			if p.slot {
				if got := e.activeSlots(t); got != "0" {
					t.Errorf("active_slots = %s after the hook error, want 0", got)
				}
			}
			if got := e.dials.Load(); got != 0 {
				t.Errorf("adapter dials = %d, want 0 (no compensation or handshake RPC)", got)
			}
			e.assertNoCredentialRelease(t)
		})
	}

	t.Run("resume_slot_with_sibling", func(t *testing.T) {
		e := newPostClaimEnv(t, workingAdapterDialer(t))
		// A co-tenant's slot on the pod the resume claims.
		if _, _, err := e.binder.SlotCounter.Reserve(context.Background(), "sbx-1", 4); err != nil {
			t.Fatalf("seed sibling occupancy: %v", err)
		}
		req := resumeRequest(4)
		req.OnClaimed = func(context.Context) error { return errPostClaimHook }
		if _, err := e.binder.Resume(context.Background(), req); err != errPostClaimHook {
			t.Fatalf("Resume err = %v, want the hook's own error", err)
		}
		if got := e.activeSlots(t); got != "1" {
			t.Errorf("active_slots = %s, want 1 (the sibling's slot survives)", got)
		}
		if !podClaimExists(t, e.kube, "sbx-1") {
			t.Error("the per-pod claim was disposed while a sibling slot remained")
		}
		if got := e.deletes.Load(); got != 0 {
			t.Errorf("claim deletes = %d, want 0", got)
		}
	})
}

// spec: 7.3 (Retry and Resume), 4.6.1 (orphan-claim collection)
// diagnosis: a failure after the claim left the claim for the orphan-claim
// GC. A handshake failure on the create-time Claim, on Bind, or on either
// Resume pool, and a failed adapter Resume on an exclusive pool, each hold a
// warm pod for the GC window per failed attempt, which a retrying restore
// multiplies.
func TestPostClaimFailureReleasesTheClaim_spec_7_3(t *testing.T) {
	negotiateCases := []struct {
		name string
		slot bool
		call func(*podsession.Binder) error
	}{
		{name: "claim_nil_hook", call: func(b *podsession.Binder) error {
			_, err := b.Claim(context.Background(), postClaimBindReq())
			return err
		}},
		{name: "bind", call: func(b *podsession.Binder) error {
			_, err := b.Bind(context.Background(), postClaimBindReq())
			return err
		}},
		{name: "resume_exclusive", call: func(b *podsession.Binder) error {
			_, err := b.Resume(context.Background(), resumeRequest(1))
			return err
		}},
		{name: "resume_slot", slot: true, call: func(b *podsession.Binder) error {
			_, err := b.Resume(context.Background(), resumeRequest(4))
			return err
		}},
		{name: "claim_slot", slot: true, call: func(b *podsession.Binder) error {
			_, err := b.ClaimSlot(context.Background(), postClaimSlotReq())
			return err
		}},
	}
	for _, tc := range negotiateCases {
		t.Run("negotiate_failure/"+tc.name, func(t *testing.T) {
			e := newPostClaimEnv(t, negotiateFailingDialer(t))
			err := tc.call(e.binder)
			if err == nil || !strings.Contains(err.Error(), "negotiate version") {
				t.Fatalf("binder err = %v, want the NegotiateVersion failure", err)
			}
			if podClaimExists(t, e.kube, "sbx-1") {
				t.Error("the per-pod claim survived the handshake failure")
			}
			if tc.slot {
				if got := e.activeSlots(t); got != "0" {
					t.Errorf("active_slots = %s, want 0", got)
				}
			}
			e.assertNoCredentialRelease(t)
		})
	}

	t.Run("adapter_resume_failure_exclusive", func(t *testing.T) {
		// No workspace base: the adapter refuses the Resume after the
		// handshake, the failure failResume compensates.
		srv := adapter.New("adapter-test")
		srv.Runtime = &fakeRuntime{}
		e := newPostClaimEnv(t, adapterDialer(t, srv))
		if _, err := e.binder.Resume(context.Background(), resumeRequest(1)); err == nil {
			t.Fatal("Resume err = nil, want the adapter refusal")
		}
		if podClaimExists(t, e.kube, "sbx-1") {
			t.Error("the per-pod claim survived the failed adapter Resume on an exclusive pool")
		}
		if got := e.deletes.Load(); got != 1 {
			t.Errorf("claim deletes = %d, want exactly 1", got)
		}
	})
}

// spec: 7.3 (Retry and Resume)
// diagnosis: a release after the claim ran on the request context. An
// attempt whose context its deadline, a server shutdown, or a terminal
// write's cancel ended then fails its delete, and the claim waits for the
// orphan-claim GC.
func TestPostClaimReleaseRunsOnADetachedContext_spec_7_3(t *testing.T) {
	t.Run("hook_cancels_then_fails", func(t *testing.T) {
		for _, p := range postClaimPaths() {
			t.Run(p.name, func(t *testing.T) {
				e := newPostClaimEnv(t, p.dialer(t))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				err := p.call(ctx, e.binder, func(context.Context) error {
					cancel()
					return errPostClaimHook
				})
				if err != errPostClaimHook {
					t.Fatalf("binder err = %v, want the hook error", err)
				}
				if podClaimExists(t, e.kube, "sbx-1") {
					t.Error("the per-pod claim survived a release issued after the request context ended")
				}
				if p.slot {
					if got := e.activeSlots(t); got != "0" {
						t.Errorf("active_slots = %s, want 0", got)
					}
				}
			})
		}
	})

	t.Run("launch_failure_after_cancel", func(t *testing.T) {
		srv := adapter.New("adapter-test")
		srv.WorkspaceBase = t.TempDir()
		srv.CredentialsDir = t.TempDir()
		srv.Runtime = &fakeRuntime{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		e := newPostClaimEnv(t, interceptedAdapterDialer(t, srv, cancelOn("StartSession", cancel)))
		if _, err := e.binder.Bind(ctx, postClaimBindReq()); err == nil {
			t.Fatal("Bind err = nil, want the StartSession failure")
		}
		if podClaimExists(t, e.kube, "sbx-1") {
			t.Error("the per-pod claim survived a Launch failure after the request context ended")
		}
	})

	t.Run("adapter_resume_failure_after_cancel", func(t *testing.T) {
		srv := adapter.New("adapter-test")
		srv.WorkspaceBase = t.TempDir()
		srv.Runtime = &fakeRuntime{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		e := newPostClaimEnv(t, interceptedAdapterDialer(t, srv, cancelOn("Resume", cancel)))
		if _, err := e.binder.Resume(ctx, resumeRequest(1)); err == nil {
			t.Fatal("Resume err = nil, want the adapter Resume failure")
		}
		if podClaimExists(t, e.kube, "sbx-1") {
			t.Error("the per-pod claim survived an adapter Resume failure after the request context ended")
		}
	})
}

// spec: 7.3 (Retry and Resume), 4.9 (Credential Leasing Service)
// diagnosis: a failed whole-sequence Bind revoked every §4.9 lease its
// session holds. A resume rebuild's session can hold a lease another attempt
// minted, and a session-wide revoke strips it, so that attempt's runtime
// loses its credential. The decomposed Launch, which Bind does not build,
// keeps its session-wide release (TestLaunchFailureRevokesAssignedLease_spec_7_1).
func TestBindReleasesOnlyItsOwnAttemptsLeases_spec_7_3(t *testing.T) {
	// otherAttemptLease mints a lease for sess-1 the way another attempt
	// would, before the Bind under test runs, and returns its identifier.
	otherAttemptLease := func(t *testing.T, e *postClaimEnv) string {
		t.Helper()
		l, err := e.assigner.AssignProto("claude-prod", "sess-1", "", "acme", 0)
		if err != nil {
			t.Fatalf("mint the other attempt's lease: %v", err)
		}
		return l.GetLeaseId()
	}
	// bindMinted returns the leases the Bind under test minted.
	bindMinted := func(e *postClaimEnv, other string) []string {
		return slices.DeleteFunc(slices.Clone(e.assigner.minted), func(id string) bool { return id == other })
	}

	t.Run("launch_failure", func(t *testing.T) {
		srv := adapter.New("adapter-test")
		srv.WorkspaceBase = t.TempDir()
		srv.CredentialsDir = t.TempDir()
		srv.Runtime = &startFailRuntime{}
		e := newPostClaimEnv(t, adapterDialer(t, srv))
		other := otherAttemptLease(t, e)
		if _, err := e.binder.Bind(context.Background(), postClaimBindReq()); err == nil {
			t.Fatal("Bind err = nil, want the start failure")
		}
		own := bindMinted(e, other)
		if len(own) != 1 {
			t.Fatalf("Bind minted %v, want one lease", own)
		}
		if len(e.assigner.released) != 0 {
			t.Errorf("session-wide releases = %v, want none", e.assigner.released)
		}
		if !slices.Equal(e.assigner.leaseReleases, own) {
			t.Errorf("leases released = %v, want exactly the attempt's own %v", e.assigner.leaseReleases, own)
		}
		if podClaimExists(t, e.kube, "sbx-1") {
			t.Error("the per-pod claim survived the Launch failure")
		}
	})

	t.Run("prepare_reconnect_failure", func(t *testing.T) {
		// Claim dials once; Prepare's reconnect dial fails.
		e := newPostClaimEnv(t, dialerFailingAfter(1, workingAdapterDialer(t)))
		otherAttemptLease(t, e)
		if _, err := e.binder.Bind(context.Background(), postClaimBindReq()); err == nil {
			t.Fatal("Bind err = nil, want the reconnect failure")
		}
		if len(e.assigner.released) != 0 || len(e.assigner.leaseReleases) != 0 {
			t.Errorf("credential releases = session-wide %v, by id %v; want none (Prepare minted nothing)",
				e.assigner.released, e.assigner.leaseReleases)
		}
		if podClaimExists(t, e.kube, "sbx-1") {
			t.Error("the per-pod claim survived the Prepare reconnect failure")
		}
	})

	t.Run("launch_reconnect_failure", func(t *testing.T) {
		// Claim and Prepare dial once each; Launch's reconnect dial fails.
		e := newPostClaimEnv(t, dialerFailingAfter(2, workingAdapterDialer(t)))
		other := otherAttemptLease(t, e)
		if _, err := e.binder.Bind(context.Background(), postClaimBindReq()); err == nil {
			t.Fatal("Bind err = nil, want the reconnect failure")
		}
		own := bindMinted(e, other)
		if len(e.assigner.released) != 0 {
			t.Errorf("session-wide releases = %v, want none", e.assigner.released)
		}
		if len(own) != 1 || !slices.Equal(e.assigner.leaseReleases, own) {
			t.Errorf("leases released = %v, want exactly the attempt's own %v", e.assigner.leaseReleases, own)
		}
		if podClaimExists(t, e.kube, "sbx-1") {
			t.Error("the per-pod claim survived the Launch reconnect failure")
		}
	})
}
