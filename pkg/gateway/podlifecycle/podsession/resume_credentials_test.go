// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// rpcTrace records each adapter RPC a binder sends, with the bind-attempt
// token the request carries, and can fail one named RPC.
type rpcTrace struct {
	mu     sync.Mutex
	calls  []string
	tokens map[string][]string
	failOn string
}

func newRPCTrace() *rpcTrace { return &rpcTrace{tokens: map[string][]string{}} }

func (r *rpcTrace) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
}

func (r *rpcTrace) interceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		name := method[strings.LastIndex(method, "/")+1:]
		r.record(name)
		if tok, ok := req.(interface{ GetBindAttempt() string }); ok {
			r.mu.Lock()
			r.tokens[name] = append(r.tokens[name], tok.GetBindAttempt())
			r.mu.Unlock()
		}
		if name == r.failOn {
			return status.Error(codes.Unavailable, name+" failed by the test")
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func (r *rpcTrace) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *rpcTrace) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c == name {
			n++
		}
	}
	return n
}

func tracedAdapterDialer(t *testing.T, trace *rpcTrace) func(string) (*adapterclient.Client, error) {
	t.Helper()
	srv := adapter.New("adapter-test")
	srv.WorkspaceBase = t.TempDir()
	srv.CredentialsDir = t.TempDir()
	srv.Runtime = &fakeRuntime{}
	return interceptedAdapterDialer(t, srv, trace.interceptor())
}

func credentialedResumeRequest(maxConcurrent int32) podsession.ResumeRequest {
	req := resumeRequest(maxConcurrent)
	req.CredentialPools = map[string]string{"anthropic": "claude-prod", "openai": "openai-prod"}
	req.CoordinationGeneration = 6
	return req
}

// spec: 7.3 (Credential leases on the replacement pod), 4.7.1 (Role and
// Gateway RPC Contract)
// diagnosis: Binder.Resume restored a session without its credential leases,
// or in an order the adapter refuses. The hook, NegotiateVersion,
// AssignCredentials keyed by every named provider, and Resume must run in that
// order under one bind-attempt token, the minted lease IDs must be returned on
// the result at the request's generation, and a request naming no provider
// sends no AssignCredentials.
func TestResumeAssignsCredentialsUnderTheResumeToken_spec_7_3(t *testing.T) {
	for _, pool := range []int32{1, 4} {
		t.Run(map[int32]string{1: "exclusive", 4: "concurrent"}[pool], func(t *testing.T) {
			trace := newRPCTrace()
			e := newPostClaimEnv(t, tracedAdapterDialer(t, trace))
			req := credentialedResumeRequest(pool)
			req.OnClaimed = func(context.Context) error { trace.record("hook"); return nil }
			res, err := e.binder.Resume(context.Background(), req)
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			defer res.Result.Adapter.Close()
			got := slices.DeleteFunc(trace.list(), func(s string) bool {
				return s != "hook" && s != "NegotiateVersion" && s != "AssignCredentials" && s != "Resume"
			})
			want := []string{"hook", "NegotiateVersion", "AssignCredentials", "Resume"}
			if !slices.Equal(got, want) {
				t.Errorf("order = %v, want %v", got, want)
			}
			assignTok, resumeTok := trace.tokens["AssignCredentials"], trace.tokens["Resume"]
			if len(assignTok) != 1 || len(resumeTok) != 1 || assignTok[0] == "" || assignTok[0] != resumeTok[0] ||
				res.Result.BindAttempt != resumeTok[0] {
				t.Errorf("tokens: assign %v, resume %v, result %q; want one shared non-empty token", assignTok, resumeTok, res.Result.BindAttempt)
			}
			minted := slices.Clone(res.Result.MintedLeaseIDs)
			slices.Sort(minted)
			wantMinted := slices.Clone(e.assigner.minted)
			slices.Sort(wantMinted)
			if len(minted) != 2 || !slices.Equal(minted, wantMinted) {
				t.Errorf("MintedLeaseIDs = %v, want the two minted %v", minted, wantMinted)
			}
			for _, c := range e.assigner.calls {
				if c.generation != 6 {
					t.Errorf("minted at generation %d, want 6", c.generation)
				}
			}
			e.assertNoCredentialRelease(t)
		})
	}

	t.Run("no provider named", func(t *testing.T) {
		trace := newRPCTrace()
		e := newPostClaimEnv(t, tracedAdapterDialer(t, trace))
		res, err := e.binder.Resume(context.Background(), resumeRequest(1))
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		defer res.Result.Adapter.Close()
		if n := trace.count("AssignCredentials"); n != 0 {
			t.Errorf("AssignCredentials calls = %d, want 0", n)
		}
		if len(res.Result.MintedLeaseIDs) != 0 {
			t.Errorf("MintedLeaseIDs = %v, want none", res.Result.MintedLeaseIDs)
		}
	})
}

// spec: 7.3 (Credential leases on the replacement pod)
// diagnosis: a failed restore returned leases it did not mint, kept leases it
// did mint, or left its pod held. A resume that fails at minting, at the
// adapter's AssignCredentials, or at the adapter's Resume, on an exclusive and
// a concurrent pool, must release exactly the leases it minted and no other,
// send one compensating Shutdown under its token, return a *SlotBindError at
// stage resume (with a *CredentialAssignmentError for a minting failure), and
// delete the claim on an exclusive pool.
func TestResumeFailureReleasesOnlyItsMintedLeases_spec_7_3(t *testing.T) {
	for _, pool := range []int32{1, 4} {
		for _, stage := range []string{"minting", "AssignCredentials", "Resume"} {
			name := map[int32]string{1: "exclusive", 4: "concurrent"}[pool] + "/" + stage
			t.Run(name, func(t *testing.T) {
				trace := newRPCTrace()
				e := newPostClaimEnv(t, tracedAdapterDialer(t, trace))
				switch stage {
				case "minting":
					e.assigner.failFrom = 2
				default:
					trace.failOn = stage
				}
				_, err := e.binder.Resume(context.Background(), credentialedResumeRequest(pool))
				var sbe *podsession.SlotBindError
				if !errors.As(err, &sbe) || sbe.Stage != "resume" {
					t.Fatalf("err = %v, want a *SlotBindError at stage resume", err)
				}
				if stage == "minting" {
					var cae *podsession.CredentialAssignmentError
					if !errors.As(err, &cae) {
						t.Errorf("err = %v, want a *CredentialAssignmentError in the chain", err)
					}
				}
				released := slices.Clone(e.assigner.leaseReleases)
				slices.Sort(released)
				minted := slices.Clone(e.assigner.minted)
				slices.Sort(minted)
				if !slices.Equal(released, minted) {
					t.Errorf("released %v, want exactly the minted %v", released, minted)
				}
				if len(e.assigner.released) != 0 || len(e.assigner.through) != 0 {
					t.Errorf("session-wide releases %v / %v, want none", e.assigner.released, e.assigner.through)
				}
				shutdownToks := trace.tokens["Shutdown"]
				if len(shutdownToks) != 1 || shutdownToks[0] == "" {
					t.Errorf("Shutdown tokens = %v, want one compensation under the attempt's token", shutdownToks)
				} else if assign := trace.tokens["AssignCredentials"]; len(assign) > 0 && assign[0] != shutdownToks[0] {
					t.Errorf("compensation token %q, want the attempt's %q", shutdownToks[0], assign[0])
				}
				if pool == 1 && podClaimExists(t, e.kube, "sbx-1") {
					t.Error("the exclusive claim survived the failed restore")
				}
				if pool == 4 {
					if got := e.activeSlots(t); got != "0" {
						t.Errorf("active_slots = %s, want 0", got)
					}
				}
			})
		}
	}
}

// spec: 7.3 (Resume driver; Credential leases on the replacement pod), 4.7.1
// (Role and Gateway RPC Contract)
// diagnosis: an uncommitted restore leaked its pod or leases, or released
// them when another releaser may already own the pod. ReleaseUncommitted must
// send one reclaim under the attempt's token, release the claim or the slot,
// and return exactly the leases the attempt minted; with requireReclaimed set,
// a reply other than RECLAIMED releases nothing and returns
// ErrReclaimUnconfirmed.
func TestReleaseUncommittedReturnsOnlyTheAttemptsPodAndLeases_spec_7_3(t *testing.T) {
	for _, pool := range []int32{1, 4} {
		t.Run(map[int32]string{1: "exclusive", 4: "concurrent"}[pool], func(t *testing.T) {
			trace := newRPCTrace()
			e := newPostClaimEnv(t, tracedAdapterDialer(t, trace))
			res, err := e.binder.Resume(context.Background(), credentialedResumeRequest(pool))
			if err != nil {
				t.Fatalf("Resume: %v", err)
			}
			if err := e.binder.ReleaseUncommitted(context.Background(), res.Result, false); err != nil {
				t.Fatalf("ReleaseUncommitted: %v", err)
			}
			toks := trace.tokens["Shutdown"]
			if len(toks) != 1 || toks[0] != res.Result.BindAttempt {
				t.Errorf("Shutdown tokens = %v, want one reclaim under %q", toks, res.Result.BindAttempt)
			}
			released := slices.Clone(e.assigner.leaseReleases)
			slices.Sort(released)
			minted := slices.Clone(res.Result.MintedLeaseIDs)
			slices.Sort(minted)
			if !slices.Equal(released, minted) || len(e.assigner.released) != 0 || len(e.assigner.through) != 0 {
				t.Errorf("released by id %v, session-wide %v/%v; want exactly %v by id", released, e.assigner.released, e.assigner.through, minted)
			}
			if pool == 1 && podClaimExists(t, e.kube, "sbx-1") {
				t.Error("the exclusive claim survived")
			}
			if pool == 4 {
				if got := e.activeSlots(t); got != "0" {
					t.Errorf("active_slots = %s, want 0", got)
				}
			}
		})
	}

	t.Run("unconfirmed reclaim releases nothing", func(t *testing.T) {
		trace := newRPCTrace()
		e := newPostClaimEnv(t, tracedAdapterDialer(t, trace))
		res, err := e.binder.Resume(context.Background(), credentialedResumeRequest(1))
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		trace.failOn = "Shutdown"
		err = e.binder.ReleaseUncommitted(context.Background(), res.Result, true)
		if !errors.Is(err, podsession.ErrReclaimUnconfirmed) {
			t.Fatalf("ReleaseUncommitted = %v, want ErrReclaimUnconfirmed", err)
		}
		if !podClaimExists(t, e.kube, "sbx-1") {
			t.Error("the claim was released after an unconfirmed reclaim")
		}
		if len(e.assigner.leaseReleases) != 0 {
			t.Errorf("leases released = %v, want none", e.assigner.leaseReleases)
		}
	})
}

// spec: 5.2 (Pool Configuration and Execution Modes)
// diagnosis: a pod whose stream ended in a crash was returned to a recycling
// pool after a clean terminal. Binder.Release of a binding marked crashed must
// send a plain Shutdown, delete the claim, and neither patch the claim
// recycling nor arm the recycle boundary, for every clean disposition, while
// an unmarked binding with the same disposition still recycles.
func TestReleaseOfACrashedBindingRetiresTheRecyclingPod_spec_5_2(t *testing.T) {
	for _, disp := range []string{"completed", "cancelled", "expired", ""} {
		for _, crashed := range []bool{true, false} {
			name := disp + map[bool]string{true: "/crashed", false: "/unmarked"}[crashed]
			if disp == "" {
				name = "empty" + map[bool]string{true: "/crashed", false: "/unmarked"}[crashed]
			}
			t.Run(name, func(t *testing.T) {
				rec := &recordingShutdownAdapter{}
				srv := adapter.New("adapter-test")
				srv.WorkspaceBase = t.TempDir()
				srv.Runtime = &fakeRuntime{}
				c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
				binder := newBinder(c, adapterDialer(t, srv))
				armer := &fakeRecycleBoundary{}
				binder.RecycleBoundary = armer
				// A real bind creates the bound claim the release acts on;
				// the release then talks to the recording adapter.
				res, err := binder.Bind(context.Background(), podsession.BindRequest{
					Pool: testPool, SessionID: "sess-1", Runtime: "claude-code", Recycle: true,
					CleanupCommands: []string{"rm -rf /workspace/*"}, CleanupTimeoutSeconds: 25,
				})
				if err != nil {
					t.Fatalf("Bind: %v", err)
				}
				res.Adapter.Close()
				res.Adapter = dialRecordingAdapter(t, rec)
				if crashed {
					res.MarkCrashed()
				}
				if err := binder.Release(context.Background(), res, disp); err != nil {
					t.Fatalf("Release: %v", err)
				}
				reqs := rec.shutdownRequests()
				if len(reqs) != 1 {
					t.Fatalf("Shutdown calls = %d, want 1", len(reqs))
				}
				recycled := reqs[0].GetRecycle() != nil
				if crashed && (recycled || len(armer.armed) != 0 || claimPresent(t, c)) {
					t.Errorf("crashed: recycle scrub %v, armed %v, claim present %v; want a plain Shutdown, no arming, and the claim deleted",
						recycled, armer.armed, claimPresent(t, c))
				}
				if !crashed && (disp == "completed" || disp == "cancelled" || disp == "expired") &&
					(!recycled || !claimPresent(t, c)) {
					t.Errorf("unmarked %q release: recycle scrub %v, claim present %v; an unmarked binding still recycles",
						disp, recycled, claimPresent(t, c))
				}
			})
		}
	}
}

var _ = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED
