// SPDX-License-Identifier: MIT

// Package secretprobe implements the §4.9 admin-time RBAC live-probe
// the Token Service exposes to the gateway. The gateway calls it before
// persisting a credential pool that references a new secretRef; the
// Token Service answers whether its own ServiceAccount can read the
// named Kubernetes Secret.
//
// The prober runs a SelfSubjectAccessReview for the `get` verb on the
// Secret resource under the Token Service identity and, on an allowed
// review, a `get` on the named Secret to confirm it exists. This
// distinguishes a missing RBAC grant (DENIED) from an absent Secret
// object (NOT_FOUND), the two §4.9 422-mapped outcomes. The probe is
// Token-Service-owned: it reviews the Token Service's own access, never
// the gateway's, so the gateway cannot substitute a semantically
// meaningless self-review under its own RBAC.
//
// spec: spec/04_system-components.md §4.9 line 1212.
package secretprobe

import (
	"context"
	"fmt"

	authzv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// Prober is the client-go-backed tokenservice.SecretAccessProber. It
// holds the Token Service's own clientset (built from the in-cluster
// config) and the default namespace its Secrets live in.
type Prober struct {
	client    kubernetes.Interface
	namespace string
}

var _ tokenservice.SecretAccessProber = (*Prober)(nil)

// New returns a Prober backed by clientset, defaulting probes to
// namespace (the Token Service's own namespace, where credentialPool
// secretRef Secrets are mounted). A request that names an explicit
// namespace overrides the default.
func New(clientset kubernetes.Interface, namespace string) *Prober {
	return &Prober{client: clientset, namespace: namespace}
}

// ProbeSecretAccess implements tokenservice.SecretAccessProber. spec:
// §4.9 line 1212.
func (p *Prober) ProbeSecretAccess(ctx context.Context, namespace, name string) (tokenservice.SecretAccessVerdict, error) {
	ns := namespace
	if ns == "" {
		ns = p.namespace
	}
	review := &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Namespace: ns,
				Verb:      "get",
				Group:     "",
				Resource:  "secrets",
				Name:      name,
			},
		},
	}
	res, err := p.client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		// The review itself could not be evaluated (API timeout,
		// transport error). Indeterminate: surface as an error so the
		// RPC maps it to codes.Unavailable rather than guessing a
		// verdict.
		return 0, fmt.Errorf("secretprobe: selfsubjectaccessreview for %q/%q: %w", ns, name, err)
	}
	if !res.Status.Allowed {
		return tokenservice.SecretAccessDenied, nil
	}
	// The review allows the get; confirm the Secret object exists so a
	// dangling secretRef is reported as NOT_FOUND rather than ALLOWED.
	if _, err := p.client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return tokenservice.SecretAccessNotFound, nil
		}
		if apierrors.IsForbidden(err) {
			// The live get is forbidden despite the review allowing it
			// (an admission webhook or a narrower live policy). Treat as
			// denied: the Token Service cannot read the Secret.
			return tokenservice.SecretAccessDenied, nil
		}
		return 0, fmt.Errorf("secretprobe: get secret %q/%q: %w", ns, name, err)
	}
	return tokenservice.SecretAccessAllowed, nil
}
