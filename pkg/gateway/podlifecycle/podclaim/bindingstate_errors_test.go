// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// These tier-1 tests inject API-server failures into the binding-state
// writers and the precondition DELETE via a fake client wrapped in
// error-returning interceptors. They exercise the wrapped-error and
// NotFound/Conflict branches the envtest happy-path tests do not reach, so
// the writers fail closed rather than silently swallowing an API error.
//
// The fake client's underlying store is never reached: each interceptor
// short-circuits before the call, so the SSA/status semantics of the fake
// are irrelevant to these tests.

// fakeWith returns a fake client seeded with a claim in the given binding
// state and the SandboxClaim status subresource registered.
func fakeWith(t *testing.T, phase claimstate.State) client.WithWatch {
	t.Helper()
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-sbx-1", Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: "sbx-1", TenantID: "acme"},
		Status:     lennyv1.SandboxClaimStatus{Phase: string(phase)},
	}
	return fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(claim).
		WithStatusSubresource(&lennyv1.SandboxClaim{}).
		Build()
}

// injectGetErr wraps c so every Get returns err.
func injectGetErr(c client.WithWatch, err error) client.Client {
	return interceptor.NewClient(c, interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return err
		},
	})
}

// injectStatusPatchErr wraps c so every status-subresource Patch returns err.
func injectStatusPatchErr(c client.WithWatch, err error) client.Client {
	return interceptor.NewClient(c, interceptor.Funcs{
		SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
			return err
		},
	})
}

// TestBindingStateWritersWrapGetErrors_spec_4_6_3 asserts the writers that
// read the claim first surface a Get failure as a wrapped error rather than
// proceeding on a stale or absent claim.
//
// diagnosis: a failure means a binding-state writer swallowed an API-server
// read error and either skipped the write or panicked on a nil claim.
//
// spec: §4.6.3 (gateway-owned binding-state writes).
func TestBindingStateWritersWrapGetErrors_spec_4_6_3(t *testing.T) {
	ctx := context.Background()
	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "sandboxclaims"}, "claim-sbx-1")
	cases := []struct {
		name string
		call func(client.Client) error
	}{
		{"WriteBoundStatus", func(c client.Client) error {
			return podclaim.WriteBoundStatus(ctx, c, testNS, "claim-sbx-1")
		}},
		{"WriteRecyclingStatus", func(c client.Client) error {
			return podclaim.WriteRecyclingStatus(ctx, c, testNS, "claim-sbx-1", nil)
		}},
		{"WriteRewarmStartedStatus", func(c client.Client) error {
			return podclaim.WriteRewarmStartedStatus(ctx, c, testNS, "claim-sbx-1", nil)
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := injectGetErr(fakeWith(t, claimstate.Recycling), notFound)
			err := tc.call(c)
			if err == nil {
				t.Fatalf("%s returned nil on a Get NotFound, want wrapped error", tc.name)
			}
			if !apierrors.IsNotFound(err) {
				t.Errorf("%s error does not preserve NotFound in the chain: %v", tc.name, err)
			}
		})
	}
}

// TestBindingStateWritersWrapPatchErrors_spec_4_6_3 asserts every writer that
// issues a status PATCH surfaces an apply failure as a wrapped error, so a
// dropped binding-state transition is never silent.
//
// diagnosis: a failure means a binding-state writer ignored the SSA status
// PATCH error and reported success while the claim status was unchanged.
//
// spec: §4.6.3 (gateway-owned binding-state writes via status PATCH).
func TestBindingStateWritersWrapPatchErrors_spec_4_6_3(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("apiserver unavailable")
	cases := []struct {
		name  string
		phase claimstate.State
		call  func(client.Client) error
	}{
		{"WriteBoundStatus", claimstate.State(""), func(c client.Client) error {
			return podclaim.WriteBoundStatus(ctx, c, testNS, "claim-sbx-1")
		}},
		{"WriteRecyclingStatus", claimstate.Bound, func(c client.Client) error {
			return podclaim.WriteRecyclingStatus(ctx, c, testNS, "claim-sbx-1", nil)
		}},
		{"WriteRewarmStartedStatus", claimstate.Recycling, func(c client.Client) error {
			return podclaim.WriteRewarmStartedStatus(ctx, c, testNS, "claim-sbx-1", nil)
		}},
		{"WriteReservedStatus", claimstate.Recycling, func(c client.Client) error {
			_, err := podclaim.WriteReservedStatus(ctx, c, testNS, "claim-sbx-1", 10*time.Second, nil)
			return err
		}},
		{"WriteRebindStatus", claimstate.Reserved, func(c client.Client) error {
			return podclaim.WriteRebindStatus(ctx, c, testNS, "claim-sbx-1", nil)
		}},
		{"WriteDispositionStatus", claimstate.Bound, func(c client.Client) error {
			return podclaim.WriteDispositionStatus(ctx, c, testNS, "claim-sbx-1", claimstate.Released, nil)
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := injectStatusPatchErr(fakeWith(t, tc.phase), boom)
			err := tc.call(c)
			if err == nil {
				t.Fatalf("%s returned nil on a status PATCH failure, want wrapped error", tc.name)
			}
			if !errors.Is(err, boom) {
				t.Errorf("%s did not wrap the PATCH error with %%w: %v", tc.name, err)
			}
		})
	}
}

// TestDeleteOnHoldExpiryWrapsUnexpectedError_spec_4_6_1 asserts the
// precondition DELETE surfaces a non-Conflict, non-NotFound failure as a real
// error (not an aborted-race signal), so a transient API failure is not
// mistaken for a lost rebind race.
//
// diagnosis: a failure means DeleteOnHoldExpiry reported aborted=false,err=nil
// (a silent success) or aborted=true (a phantom rebind) on an API error that
// is neither a precondition conflict nor a missing claim.
//
// spec: §4.6.1 (precondition-guarded hold-expiry DELETE).
func TestDeleteOnHoldExpiryWrapsUnexpectedError_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("apiserver unavailable")
	c := interceptor.NewClient(fakeWith(t, claimstate.Reserved), interceptor.Funcs{
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			return boom
		},
	})
	aborted, err := podclaim.DeleteOnHoldExpiry(ctx, c, testNS, "claim-sbx-1", podclaim.ReservedHold{
		UID: "uid-1", ResourceVersion: "42",
	})
	if err == nil {
		t.Fatalf("DeleteOnHoldExpiry returned nil on an unexpected API error, want wrapped error")
	}
	if aborted {
		t.Errorf("DeleteOnHoldExpiry reported aborted on a non-conflict error; only a precondition Conflict aborts")
	}
	if !errors.Is(err, boom) {
		t.Errorf("DeleteOnHoldExpiry did not wrap the delete error with %%w: %v", err)
	}
}

// TestDeleteOnHoldExpiryAbortsOnConflict_spec_3_2 asserts that a precondition
// Conflict (the resourceVersion changed under a rebind) is reported as an
// aborted race with a nil error, so the caller distinguishes a lost race from
// a real failure and leaves the rebound claim intact.
//
// diagnosis: a failure means DeleteOnHoldExpiry treated a precondition
// Conflict as a hard error or as a successful delete, breaking the
// rebind-wins-the-race contract.
//
// spec: §4.6.1 (precondition DELETE), §3.2 (rebind-vs-hold-expiry race).
func TestDeleteOnHoldExpiryAbortsOnConflict_spec_3_2(t *testing.T) {
	ctx := context.Background()
	conflict := apierrors.NewConflict(
		schema.GroupResource{Resource: "sandboxclaims"}, "claim-sbx-1",
		errors.New("the object has been modified"),
	)
	c := interceptor.NewClient(fakeWith(t, claimstate.Reserved), interceptor.Funcs{
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			return conflict
		},
	})
	aborted, err := podclaim.DeleteOnHoldExpiry(ctx, c, testNS, "claim-sbx-1", podclaim.ReservedHold{
		UID: "uid-1", ResourceVersion: "42",
	})
	if err != nil {
		t.Fatalf("DeleteOnHoldExpiry returned error on a precondition Conflict, want aborted with nil error: %v", err)
	}
	if !aborted {
		t.Error("DeleteOnHoldExpiry did not report aborted on a precondition Conflict")
	}
}
