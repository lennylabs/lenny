// SPDX-License-Identifier: MIT

// Package credsecretfake is an in-memory credentials namespace behind the
// admin.CredentialSecretWriter interface, for tests of the Lenny-written
// credential Secret path. Each Secret it holds carries the
// lenny.dev/tenant-hash label value its owner's tenant gives, and
// DeleteTenant deletes by that label alone, as the kube-apiserver executes
// the gateway's label-selector delete-collection. A Secret the test seeds
// with no tenant carries no label and no DeleteTenant reaches it.
//
// The writer records every call in order and runs an optional hook before
// each DeleteTenant, through which a test injects a fault or inspects the
// store at the moment the delete-collection would run.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 2 and 3.
package credsecretfake

import (
	"context"
	"sort"
	"sync"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// Writer is the fake credentials namespace. The zero value is not usable;
// call New.
type Writer struct {
	mu     sync.Mutex
	labels map[string]string // Secret name -> tenant-hash label ("" when unlabelled)
	calls  []string
	hook   func(ctx context.Context, tenant string) error
}

// New returns an empty namespace.
func New() *Writer {
	return &Writer{labels: map[string]string{}}
}

// Seed places a Secret in the namespace without recording a call. A
// non-empty tenant labels it with that tenant's hash; an empty tenant
// leaves it unlabelled, as an operator-created Secret is.
func (w *Writer) Seed(name, tenant string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.labels[name] = tenantHash(tenant)
}

// BeforeDeleteTenant installs a hook that runs inside each DeleteTenant
// before the delete-collection. A non-nil return fails the call and deletes
// nothing.
func (w *Writer) BeforeDeleteTenant(hook func(ctx context.Context, tenant string) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.hook = hook
}

// Put implements admin.CredentialSecretWriter. It labels the Secret with
// its owner's tenant hash on both create and replace.
func (w *Writer) Put(_ context.Context, name string, meta admin.SecretOwner, _ map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, "put "+name)
	w.labels[name] = tenantHash(meta.TenantID)
	return nil
}

// Delete implements admin.CredentialSecretWriter. An absent Secret is not
// an error.
func (w *Writer) Delete(_ context.Context, name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, "delete "+name)
	delete(w.labels, name)
	return nil
}

// DeleteTenant implements admin.CredentialSecretWriter: it deletes every
// Secret whose tenant-hash label matches tenant.
func (w *Writer) DeleteTenant(ctx context.Context, tenant string) error {
	w.mu.Lock()
	w.calls = append(w.calls, "deleteTenant "+tenant)
	hook := w.hook
	w.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, tenant); err != nil {
			return err
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	want := tenantHash(tenant)
	for name, label := range w.labels {
		if label != "" && label == want {
			delete(w.labels, name)
		}
	}
	return nil
}

// Has reports whether the namespace holds the Secret name.
func (w *Writer) Has(name string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.labels[name]
	return ok
}

// Names returns the Secret names the namespace holds, sorted.
func (w *Writer) Names() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.labels))
	for name := range w.labels {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Calls returns the calls made so far, in order, each as "<op> <arg>".
func (w *Writer) Calls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

func tenantHash(tenant string) string {
	if tenant == "" {
		return ""
	}
	return credential.ManagedSecretTenantHash(tenant)
}

var _ admin.CredentialSecretWriter = (*Writer)(nil)
