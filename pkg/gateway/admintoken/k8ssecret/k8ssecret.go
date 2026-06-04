// SPDX-License-Identifier: MIT

// Package k8ssecret adapts a controller-runtime client to the
// admintoken.SecretStore contract, backing the §17.6
// lenny-system/lenny-admin-token Secret with a real Kubernetes Secret.
//
// spec: §17.6 lines 455-474 — F-17.6.3.
package k8ssecret

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Store implements admintoken.SecretStore over a controller-runtime
// client. The gateway ServiceAccount needs `get`/`create`/`patch` on
// Secrets in the target namespace (§17.6 line 474).
type Store struct {
	c client.Client
}

// New wraps c.
func New(c client.Client) *Store { return &Store{c: c} }

// Get returns the Secret's data, or exists=false (nil error) when the
// Secret is absent so the caller distinguishes "not yet created" from a
// transport failure.
func (s *Store) Get(ctx context.Context, namespace, name string) (map[string][]byte, bool, error) {
	var sec corev1.Secret
	err := s.c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &sec)
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("k8ssecret: get %s/%s: %w", namespace, name, err)
	}
	return sec.Data, true, nil
}

// Create creates an Opaque Secret with the given labels and data.
func (s *Store) Create(ctx context.Context, namespace, name string, labels map[string]string, data map[string][]byte) error {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
	if err := s.c.Create(ctx, sec); err != nil {
		return fmt.Errorf("k8ssecret: create %s/%s: %w", namespace, name, err)
	}
	return nil
}

// Update reads the Secret and replaces its data. A Secret that vanished
// between Get and Update surfaces the not-found error to the caller.
func (s *Store) Update(ctx context.Context, namespace, name string, data map[string][]byte) error {
	var sec corev1.Secret
	if err := s.c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &sec); err != nil {
		return fmt.Errorf("k8ssecret: re-read %s/%s: %w", namespace, name, err)
	}
	sec.Data = data
	if err := s.c.Update(ctx, &sec); err != nil {
		return fmt.Errorf("k8ssecret: update %s/%s: %w", namespace, name, err)
	}
	return nil
}
