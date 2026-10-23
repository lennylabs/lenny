// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const reclaimNS = "lenny-agents"

// releaseRecorder is a CredentialAssigner that records ReleaseSession calls.
type releaseRecorder struct {
	mu       sync.Mutex
	released []string
}

func (*releaseRecorder) AssignProto(_, _, _, _ string, _ int64) (*adapterv1.CredentialLease, error) {
	return nil, nil
}

func (r *releaseRecorder) ReleaseSession(sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released = append(r.released, sessionID)
	return nil
}

func (*releaseRecorder) ReleaseThrough(string, int64) error { return nil }
func (*releaseRecorder) Release(string) error               { return nil }

// deleteFault scripts the claim Delete: before fails without applying, and
// after applies the delete and then reports an error.
type deleteFault struct {
	mu      sync.Mutex
	before  bool
	after   bool
	calls   int
	ctxErrs []error
}

func (d *deleteFault) intercept(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
	d.mu.Lock()
	d.calls++
	d.ctxErrs = append(d.ctxErrs, ctx.Err())
	before, after := d.before, d.after
	d.mu.Unlock()
	if before {
		return errors.New("apiserver unavailable")
	}
	if err := cl.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	if after {
		return errors.New("apiserver timed out after the delete applied")
	}
	return nil
}

type reclaimFixture struct {
	b      *Binder
	c      client.Client
	creds  *releaseRecorder
	faults *deleteFault
	mr     *miniredis.Miniredis
}

// newReclaimFixture builds a Binder over a fake cluster holding the per-pod
// claim of pod-a, a miniredis slot set, and a recording assigner.
func newReclaimFixture(t *testing.T) *reclaimFixture {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	faults := &deleteFault{}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(&lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName("pod-a"), Namespace: reclaimNS},
	}).WithInterceptorFuncs(interceptor.Funcs{Delete: faults.intercept}).Build()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	creds := &releaseRecorder{}
	b := &Binder{Client: c, Namespace: reclaimNS, Credentials: creds, SlotCounter: slotcounter.New(rc)}
	return &reclaimFixture{b: b, c: c, creds: creds, faults: faults, mr: mr}
}

func (f *reclaimFixture) claimPresent(t *testing.T) bool {
	t.Helper()
	err := f.c.Get(context.Background(), client.ObjectKey{Namespace: reclaimNS, Name: podclaim.ClaimName("pod-a")}, &lennyv1.SandboxClaim{})
	if err == nil {
		return true
	}
	if apierrors.IsNotFound(err) {
		return false
	}
	t.Fatalf("get claim: %v", err)
	return false
}

func (f *reclaimFixture) reserve(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, _, err := f.b.SlotCounter.Reserve(context.Background(), "pod-a", id, 4); err != nil {
			t.Fatalf("reserve %s: %v", id, err)
		}
	}
}

func (f *reclaimFixture) members(t *testing.T) []string {
	t.Helper()
	if !f.mr.Exists("lenny:pod:{pod-a}:active_slots") {
		return nil
	}
	m, err := f.mr.Members("lenny:pod:{pod-a}:active_slots")
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	sort.Strings(m)
	return m
}

// TestReclaimClaimedFailureOutcomes_spec_4_6_3 pins every cell of the
// pre-running reclaim's failure outcomes on both pool kinds: the exclusive
// by-name delete runs detached, so a cancelled caller context still deletes
// the claim; a delete that fails leaves the claim and returns the error; a
// delete that applied and then failed returns the error with the claim gone;
// a claim already gone is nil. On a concurrent-workspace pool a slot-set
// failure returns the error with the member kept, a disposition failure after
// the removal is not repeated, and a duplicate release removes nothing and
// writes no claim. The lease release runs first on every path.
//
// spec: §4.6.3 (SandboxClaim: a release that ends the occupancy without
// reuse), §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics),
// §7.1 step 23 (lease release)
func TestReclaimClaimedFailureOutcomes_spec_4_6_3(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name        string
		perSlot     bool
		ctx         context.Context
		setup       func(t *testing.T, f *reclaimFixture)
		wantErr     bool
		wantSlotErr bool
		wantClaim   bool
		wantMembers []string
		wantDeletes int
	}{
		{
			name: "exclusive, caller context ended: the detached delete still runs", ctx: cancelled,
			wantClaim: false, wantDeletes: 1,
		},
		{
			name: "exclusive, delete fails before it applies: claim left", ctx: context.Background(),
			setup:   func(_ *testing.T, f *reclaimFixture) { f.faults.before = true },
			wantErr: true, wantClaim: true, wantDeletes: 1,
		},
		{
			name: "exclusive, delete applies and then errors: claim gone", ctx: context.Background(),
			setup:   func(_ *testing.T, f *reclaimFixture) { f.faults.after = true },
			wantErr: true, wantClaim: false, wantDeletes: 1,
		},
		{
			name: "exclusive, claim already gone: nil", ctx: context.Background(),
			setup: func(t *testing.T, f *reclaimFixture) {
				if err := podclaim.DeleteClaim(context.Background(), f.c, reclaimNS, "pod-a"); err != nil {
					t.Fatalf("pre-delete: %v", err)
				}
				f.faults.calls = 0
			},
			wantClaim: false, wantDeletes: 1,
		},
		{
			name:    "per-slot, caller context ended: the detached release removes the last member and deletes the claim",
			perSlot: true, ctx: cancelled,
			setup:     func(t *testing.T, f *reclaimFixture) { f.reserve(t, "sess-1") },
			wantClaim: false, wantDeletes: 1,
		},
		{
			name: "per-slot, slot-set failure: member kept, no claim write", perSlot: true, ctx: context.Background(),
			setup: func(t *testing.T, f *reclaimFixture) {
				f.reserve(t, "sess-1")
				f.b.slotReleasePause = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
				f.mr.SetError("READONLY injected")
			},
			wantErr: true, wantSlotErr: true, wantClaim: true, wantDeletes: 0,
		},
		{
			name: "per-slot, disposition delete fails after the removal: error, not repeated", perSlot: true, ctx: context.Background(),
			setup: func(t *testing.T, f *reclaimFixture) {
				f.reserve(t, "sess-1")
				f.faults.before = true
			},
			wantErr: true, wantClaim: true, wantDeletes: 1,
		},
		{
			name: "per-slot, member already removed: nil, no claim write", perSlot: true, ctx: context.Background(),
			setup:     func(t *testing.T, f *reclaimFixture) { f.reserve(t, "sib") },
			wantClaim: true, wantMembers: []string{"sib"}, wantDeletes: 0,
		},
		{
			name: "per-slot, live sibling: member removed, claim kept", perSlot: true, ctx: context.Background(),
			setup:     func(t *testing.T, f *reclaimFixture) { f.reserve(t, "sess-1", "sib") },
			wantClaim: true, wantMembers: []string{"sib"}, wantDeletes: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReclaimFixture(t)
			if tc.setup != nil {
				tc.setup(t, f)
			}
			err := f.b.ReclaimClaimed(tc.ctx, "pod-a", "sess-1", tc.perSlot)
			f.mr.SetError("")
			if (err != nil) != tc.wantErr {
				t.Fatalf("ReclaimClaimed error = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantSlotErr && !errors.Is(err, podclaim.ErrSlotCounterRelease) {
				t.Errorf("error = %v, want it to carry ErrSlotCounterRelease", err)
			}
			if got := f.claimPresent(t); got != tc.wantClaim {
				t.Errorf("claim present = %v, want %v", got, tc.wantClaim)
			}
			if f.faults.calls != tc.wantDeletes {
				t.Errorf("claim deletes = %d, want %d", f.faults.calls, tc.wantDeletes)
			}
			for i, cerr := range f.faults.ctxErrs {
				if cerr != nil {
					t.Errorf("delete %d ran on an ended context (%v), want a detached context", i, cerr)
				}
			}
			if tc.wantSlotErr {
				if got := f.members(t); len(got) != 1 || got[0] != "sess-1" {
					t.Errorf("members after a slot-set failure = %v, want [sess-1] (kept)", got)
				}
			} else if tc.perSlot {
				if got := f.members(t); !slices.Equal(got, tc.wantMembers) {
					t.Errorf("members = %v, want %v", got, tc.wantMembers)
				}
			}
			if len(f.creds.released) != 1 || f.creds.released[0] != "sess-1" {
				t.Errorf("ReleaseSession calls = %v, want [sess-1]", f.creds.released)
			}
		})
	}
}
