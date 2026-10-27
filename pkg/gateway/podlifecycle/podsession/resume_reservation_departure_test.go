// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// reservationFault is one way reserveResumeSlot fails before or at the slot
// counter's reservation, after claimPod created the resume's claim.
type reservationFault struct {
	name string
	// sandboxGet, claimGet, and terminalClaim fault the reads
	// ReserveSlotOnPod makes once the claim is in place.
	sandboxGet    bool
	claimGet      bool
	terminalClaim bool
	// fullPod fills the pod to its bound before the resume.
	fullPod bool
	// counterDown closes Redis before the resume, so the reservation
	// fails at the counter.
	counterDown bool
	// wantErr is a fragment of the reservation error the case reaches, so
	// the case fails when the resume stopped before its reservation.
	wantErr string
	// claimRemains is whether the claim is still readable afterwards: a
	// faulted read leaves the claim in the apiserver.
	claimRemains bool
}

// spec: §5.2 (Slot assignment atomicity), §4.6.1 (Orphaned SandboxClaim
// detection)
//
// Binder.Resume leaves the claim its claimPod created when the slot
// reservation fails before or at the counter's reservation: the Sandbox read,
// a missing or terminal claim, a full pod, and a counter error each return
// the error with no claim delete and no member added, and the claim-orphan GC
// reclaims the claim once no sibling reserved onto it and no non-terminal row
// names the pod. A claim delete here would drain a pod a same-tenant sibling
// may have joined in the window.
func TestResumeReservationFailureLeavesClaimForGC_spec_5_2(t *testing.T) {
	for _, tc := range []reservationFault{
		{name: "sandbox read fails", sandboxGet: true, wantErr: "injected sandbox read failure", claimRemains: true},
		{name: "claim reads missing", claimGet: true, wantErr: "holds no per-pod claim", claimRemains: true},
		{name: "claim reads terminal", terminalClaim: true, wantErr: "is terminal", claimRemains: true},
		{name: "pod at its bound", fullPod: true, wantErr: podclaim.ErrNoConcurrentSlot.Error(), claimRemains: true},
		{name: "counter unreachable", counterDown: true, wantErr: "reserve slot via counter", claimRemains: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := testNamespace(t)
			srv := adapter.New("adapter-test")
			srv.WorkspaceBase = t.TempDir()
			srv.Runtime = &fakeRuntime{}
			base := k8sClient(t, idleSandbox(ns, "sbx-1", "10.244.1.7"))
			claimed := &atomic.Bool{}
			deletes := &atomic.Int32{}
			kube := interceptor.NewClient(base, reservationFaults(tc, claimed, deletes))
			binder, mr := resumeSlotFixture(t, kube, adapterDialer(t, srv))
			ctx := context.Background()
			if tc.fullPod {
				for i := 0; i < 2; i++ {
					if _, _, err := binder.SlotCounter.Reserve(ctx, "sbx-1", fmt.Sprintf("co-%d", i), 2); err != nil {
						t.Fatalf("seed occupancy: %v", err)
					}
				}
			}
			if tc.counterDown {
				mr.Close()
			}

			res, err := binder.Resume(ctx, resumeRequest(2))
			if err == nil {
				res.Result.Adapter.Close()
				t.Fatal("Resume succeeded, want the reservation's error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Resume error = %v, want the reservation's %q", err, tc.wantErr)
			}
			if n := deletes.Load(); n != 0 {
				t.Errorf("claim deletes = %d, want none; the claim is left for the orphan GC", n)
			}
			if got := podClaimExists(t, base, "sbx-1"); got != tc.claimRemains {
				t.Errorf("claim exists = %v, want %v", got, tc.claimRemains)
			}
			if !tc.counterDown {
				members, err := binder.SlotCounter.Members(ctx, "sbx-1")
				if err != nil {
					t.Fatalf("read the slot set: %v", err)
				}
				for _, m := range members {
					if m == "sess-1" {
						t.Errorf("slot set %v holds the resumed session, want no member added", members)
					}
				}
			}
		})
	}
}

// reservationFaults returns the interceptor that faults the case's reads
// once claimPod has stamped the pod's tenant, which is its last write, and
// counts every SandboxClaim delete.
func reservationFaults(tc reservationFault, claimed *atomic.Bool, deletes *atomic.Int32) interceptor.Funcs {
	return interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
			err := c.Patch(ctx, obj, p, opts...)
			if _, ok := obj.(*corev1.Pod); ok {
				claimed.Store(true)
			}
			return err
		},
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if claimed.Load() {
				switch o := obj.(type) {
				case *lennyv1.Sandbox:
					if tc.sandboxGet {
						return errors.New("injected sandbox read failure")
					}
				case *lennyv1.SandboxClaim:
					if tc.claimGet {
						return apierrors.NewNotFound(schema.GroupResource{Group: "lenny.dev", Resource: "sandboxclaims"}, key.Name)
					}
					if tc.terminalClaim {
						if err := c.Get(ctx, key, o, opts...); err != nil {
							return err
						}
						o.Status.Phase = string(claimstate.Released)
						return nil
					}
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok {
				deletes.Add(1)
			}
			return c.Delete(ctx, obj, opts...)
		},
	}
}
