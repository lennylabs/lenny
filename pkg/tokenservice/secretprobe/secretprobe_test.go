// SPDX-License-Identifier: MIT

package secretprobe

import (
	"context"
	"errors"
	"testing"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// withSSARReactor makes the fake clientset answer SelfSubjectAccessReview
// creates with a fixed Allowed verdict (or an error when reviewErr is
// set). Without it the fake echoes the submitted review, whose zero
// Status.Allowed is false.
func withSSARReactor(cs *fake.Clientset, allowed bool, reviewErr error) {
	cs.PrependReactor("create", "selfsubjectaccessreviews",
		func(ktesting.Action) (bool, runtime.Object, error) {
			if reviewErr != nil {
				return true, nil, reviewErr
			}
			return true, &authzv1.SelfSubjectAccessReview{
				Status: authzv1.SubjectAccessReviewStatus{Allowed: allowed},
			}, nil
		})
}

func secretObj(ns, name string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
}

// spec: §4.9 line 1212 — an allowed review plus an existing Secret is
// ALLOWED.
func TestProbe_AllowedAndExists(t *testing.T) {
	cs := fake.NewSimpleClientset(secretObj("lenny-system", "anthropic-key-1"))
	withSSARReactor(cs, true, nil)
	got, err := New(cs, "lenny-system").ProbeSecretAccess(context.Background(), "", "anthropic-key-1")
	if err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if got != tokenservice.SecretAccessAllowed {
		t.Fatalf("verdict = %v, want Allowed", got)
	}
}

// spec: §4.9 line 1212 — a denied review is DENIED (RBAC grant missing).
func TestProbe_Denied(t *testing.T) {
	cs := fake.NewSimpleClientset(secretObj("lenny-system", "anthropic-key-1"))
	withSSARReactor(cs, false, nil)
	got, err := New(cs, "lenny-system").ProbeSecretAccess(context.Background(), "", "anthropic-key-1")
	if err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if got != tokenservice.SecretAccessDenied {
		t.Fatalf("verdict = %v, want Denied", got)
	}
}

// spec: §4.9 line 1212 — the grant exists but the Secret object is
// absent: NOT_FOUND, distinct from DENIED so the operator creates the
// Secret rather than patching RBAC.
func TestProbe_AllowedButMissing(t *testing.T) {
	cs := fake.NewSimpleClientset() // no secrets seeded
	withSSARReactor(cs, true, nil)
	got, err := New(cs, "lenny-system").ProbeSecretAccess(context.Background(), "", "absent")
	if err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if got != tokenservice.SecretAccessNotFound {
		t.Fatalf("verdict = %v, want NotFound", got)
	}
}

// An indeterminate review (API error) is surfaced as an error so the RPC
// returns codes.Unavailable rather than guessing a verdict.
func TestProbe_ReviewErrorIsIndeterminate(t *testing.T) {
	cs := fake.NewSimpleClientset()
	withSSARReactor(cs, false, errors.New("apiserver timeout"))
	_, err := New(cs, "lenny-system").ProbeSecretAccess(context.Background(), "", "anthropic-key-1")
	if err == nil {
		t.Fatal("ProbeSecretAccess: want error for indeterminate review")
	}
}

// An explicit request namespace overrides the prober default.
func TestProbe_RequestNamespaceOverridesDefault(t *testing.T) {
	cs := fake.NewSimpleClientset(secretObj("other-ns", "k"))
	withSSARReactor(cs, true, nil)
	got, err := New(cs, "lenny-system").ProbeSecretAccess(context.Background(), "other-ns", "k")
	if err != nil {
		t.Fatalf("ProbeSecretAccess: %v", err)
	}
	if got != tokenservice.SecretAccessAllowed {
		t.Fatalf("verdict = %v, want Allowed (secret is in other-ns)", got)
	}
}
