// SPDX-License-Identifier: MIT

// Package connectorsecret resolves a §9.3 connector's confidential-client
// secret from its `auth.clientSecretRef` at OAuth token-exchange time.
// §9.3 keeps the raw secret out of the connector registry: the registry
// stores only a `namespace/name` reference, and the gateway resolves the
// live secret through this seam when it runs the authorization-code
// exchange. The resolver reads the referenced Kubernetes Secret, so the
// raw client secret never lands in Postgres or the connector document.
//
// KubeResolver satisfies the gateway admin ClientSecretResolver
// interface structurally; the production gateway wires it when it has a
// cluster client, and a connector with no clientSecretRef (a §9.3 public
// client authenticated by PKCE alone) never reaches it.
package connectorsecret

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// DefaultSecretKey is the Kubernetes Secret data key the resolver reads
// when a clientSecretRef names only `namespace/name`. The spec writes
// the reference as `namespace/name` (spec §9.3 line 129) without naming
// a key, so the gateway adopts a conventional key that an operator can
// override at wiring time, or address explicitly with a three-segment
// `namespace/name/key` reference.
const DefaultSecretKey = "clientSecret"

// ErrClientSecretNotFound reports that a clientSecretRef names no Secret,
// or names a Secret that carries no value under the resolved key. It
// mirrors the admin ClientSecretResolver contract for an absent secret.
var ErrClientSecretNotFound = errors.New("connectorsecret: connector client secret not found")

// KubeResolver resolves a confidential connector's client secret from a
// Kubernetes Secret referenced by `namespace/name` (or
// `namespace/name/key`).
type KubeResolver struct {
	reader     client.Reader
	defaultKey string
}

// NewKubeResolver returns a resolver that reads Secrets through reader.
// An empty defaultKey selects DefaultSecretKey.
func NewKubeResolver(reader client.Reader, defaultKey string) *KubeResolver {
	if defaultKey == "" {
		defaultKey = DefaultSecretKey
	}
	return &KubeResolver{reader: reader, defaultKey: defaultKey}
}

// Resolve returns the client secret for ref, a `namespace/name` or
// `namespace/name/key` reference. It returns ErrClientSecretNotFound
// when the Secret does not exist or carries no value under the resolved
// key, and a descriptive error for a malformed reference or a read
// failure.
func (r *KubeResolver) Resolve(ctx context.Context, ref string) (string, error) {
	ns, name, key, err := parseRef(ref, r.defaultKey)
	if err != nil {
		return "", err
	}
	var secret corev1.Secret
	if err := r.reader.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("%w: %s/%s", ErrClientSecretNotFound, ns, name)
		}
		return "", fmt.Errorf("connectorsecret: read secret %s/%s: %w", ns, name, err)
	}
	raw, ok := secret.Data[key]
	if !ok || len(raw) == 0 {
		return "", fmt.Errorf("%w: %s/%s has no value under key %q", ErrClientSecretNotFound, ns, name, key)
	}
	return string(raw), nil
}

// parseRef splits a clientSecretRef into namespace, name, and Secret
// data key. A two-segment reference uses defaultKey; a three-segment
// reference names the key explicitly. Every segment must be non-empty.
func parseRef(ref, defaultKey string) (namespace, name, key string, err error) {
	parts := strings.Split(ref, "/")
	switch len(parts) {
	case 2:
		namespace, name, key = parts[0], parts[1], defaultKey
	case 3:
		namespace, name, key = parts[0], parts[1], parts[2]
	default:
		return "", "", "", fmt.Errorf("connectorsecret: malformed clientSecretRef %q: want namespace/name or namespace/name/key", ref)
	}
	if namespace == "" || name == "" || key == "" {
		return "", "", "", fmt.Errorf("connectorsecret: malformed clientSecretRef %q: empty segment", ref)
	}
	return namespace, name, key, nil
}
