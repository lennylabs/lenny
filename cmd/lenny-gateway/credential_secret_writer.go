// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// defaultCredentialSecretTimeout is the fixed deadline on each Kubernetes
// call the credential Secret writer makes. It is a constant rather than an
// operator flag because §4.9 bounds each call without naming a tunable.
const defaultCredentialSecretTimeout = 10 * time.Second

// The annotation and label keys a Lenny-written Secret carries.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 2.
const (
	annotationCredentialTenant = "lenny.dev/tenant-id"
	annotationCredentialPool   = "lenny.dev/credential-pool"
	annotationCredentialID     = "lenny.dev/credential-id"
	labelCredentialTenantHash  = "lenny.dev/tenant-hash"
)

// kubeCredentialSecretWriter is the gateway's admin.CredentialSecretWriter
// over client-go. It uses only the verbs the gateway's credentials-namespace
// Role grants: create, update, delete, and deletecollection. It never gets
// or patches a Secret, so a Put is a Create followed, on AlreadyExists, by
// an unconditional Update (an empty resourceVersion) that replaces the
// data, labels, and annotations.
//
// spec: §4.9 (Credential Storage Topology) — Credentials namespace rule 4;
// Credential values submitted through the admin API rules 2 and 3.
type kubeCredentialSecretWriter struct {
	secrets corev1client.SecretInterface
	timeout time.Duration
}

// newKubeCredentialSecretWriter returns a writer over secrets, the Secret
// client of the credentials namespace, bounding each call by timeout.
func newKubeCredentialSecretWriter(secrets corev1client.SecretInterface, timeout time.Duration) *kubeCredentialSecretWriter {
	if timeout <= 0 {
		timeout = defaultCredentialSecretTimeout
	}
	return &kubeCredentialSecretWriter{secrets: secrets, timeout: timeout}
}

// Put implements admin.CredentialSecretWriter.
func (w *kubeCredentialSecretWriter) Put(ctx context.Context, name string, meta admin.SecretOwner, data map[string]string) error {
	secret := managedSecret(name, meta, data)
	cctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	_, err := w.secrets.Create(cctx, secret, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create credential Secret %s: %w", name, err)
	}
	// An empty resourceVersion makes the Update unconditional, so no read
	// precedes it.
	secret.ResourceVersion = ""
	if _, err := w.secrets.Update(cctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update credential Secret %s: %w", name, err)
	}
	return nil
}

// Delete implements admin.CredentialSecretWriter. An absent Secret is not
// an error, because a Delete of an absent Secret succeeds.
func (w *kubeCredentialSecretWriter) Delete(ctx context.Context, name string) error {
	cctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	err := w.secrets.Delete(cctx, name, metav1.DeleteOptions{})
	if err == nil || apierrors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("delete credential Secret %s: %w", name, err)
}

// DeleteTenant implements admin.CredentialSecretWriter with one
// delete-collection on the tenant-hash label, which reaches a Secret that
// no stored reference names.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 3; §12.8.
func (w *kubeCredentialSecretWriter) DeleteTenant(ctx context.Context, tenant string) error {
	cctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	selector := labelCredentialTenantHash + "=" + credential.ManagedSecretTenantHash(tenant)
	if err := w.secrets.DeleteCollection(cctx, metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: selector}); err != nil {
		return fmt.Errorf("delete credential Secrets of tenant: %w", err)
	}
	return nil
}

// managedSecret builds the Lenny-written Secret name for meta holding
// exactly the submitted keys.
func managedSecret(name string, meta admin.SecretOwner, data map[string]string) *corev1.Secret {
	bytes := make(map[string][]byte, len(data))
	for k, v := range data {
		bytes[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				labelCredentialTenantHash: credential.ManagedSecretTenantHash(meta.TenantID),
			},
			Annotations: map[string]string{
				annotationCredentialTenant: meta.TenantID,
				annotationCredentialPool:   meta.Pool,
				annotationCredentialID:     meta.CredentialID,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: bytes,
	}
}

// buildCredentialSecretWriter returns the writer for the credentials
// namespace, or nil when no cluster config resolves. The caller builds it
// only when a Token Service is configured. A nil writer refuses every
// admin write that needs a Secret operation with 422
// TOKEN_SERVICE_NOT_CONFIGURED, so a gateway with no cluster access fails
// closed rather than committing a row whose Secret was never written.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 6.
func buildCredentialSecretWriter(namespace string, timeout time.Duration, logf func(string, ...any)) admin.CredentialSecretWriter {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		logf("lenny-gateway: §4.9 no cluster config for the credential Secret writer (%v); admin writes that need a credential Secret operation are refused", err)
		return nil
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logf("lenny-gateway: §4.9 build credential Secret writer clientset: %v; admin writes that need a credential Secret operation are refused", err)
		return nil
	}
	return newKubeCredentialSecretWriter(cs.CoreV1().Secrets(namespace), timeout)
}
