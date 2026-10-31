// SPDX-License-Identifier: MIT

// Package secretprobe implements the §4.9 Token Service read of a
// credential Secret. The gateway reaches it through the ProbeSecretAccess
// RPC, unkeyed as the admin-time probe before persisting a credential pool
// that introduces a secretRef, and keyed for its credential reads.
//
// Each read is one get on the named Secret in the configured credentials
// namespace, with no cache and a bounded timeout. Forbidden maps to
// DENIED, NotFound (or a missing or empty key on a keyed read) maps to
// NOT_FOUND, and any other failure is returned as an error so the RPC
// fails closed with codes.Unavailable.
//
// spec: §4.9 Token Service secret resolution.
package secretprobe

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// ReadTimeout bounds each Secret get, so a kube-apiserver that never
// answers fails the read closed rather than holding the caller.
const ReadTimeout = 5 * time.Second

// Prober is the client-go-backed tokenservice.SecretAccessProber. It holds
// the Token Service's own clientset and the credentials namespace named by
// --secret-namespace; no request can name another namespace.
type Prober struct {
	client    kubernetes.Interface
	namespace string
}

var _ tokenservice.SecretAccessProber = (*Prober)(nil)

// New returns a Prober that reads Secrets in namespace through clientset.
func New(clientset kubernetes.Interface, namespace string) *Prober {
	return &Prober{client: clientset, namespace: namespace}
}

// ProbeSecretAccess implements tokenservice.SecretAccessProber with one
// uncached get bounded by ReadTimeout. A keyed read returns the key's
// value with ALLOWED; an unkeyed read never returns a value.
//
// spec: §4.9 Token Service secret resolution, rules 1 to 4.
func (p *Prober) ProbeSecretAccess(ctx context.Context, name, key string) (tokenservice.SecretAccessVerdict, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	secret, err := p.client.CoreV1().Secrets(p.namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return tokenservice.SecretAccessNotFound, nil, nil
	case apierrors.IsForbidden(err):
		return tokenservice.SecretAccessDenied, nil, nil
	case err != nil:
		return 0, nil, fmt.Errorf("secretprobe: get secret %q in %q: %w", name, p.namespace, err)
	}
	if key == "" {
		return tokenservice.SecretAccessAllowed, nil, nil
	}
	value := secret.Data[key]
	if len(value) == 0 {
		return tokenservice.SecretAccessNotFound, nil, nil
	}
	return tokenservice.SecretAccessAllowed, value, nil
}
