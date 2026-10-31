// SPDX-License-Identifier: MIT

package credential

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ManagedSecretPrefix is the Secret name prefix reserved for credential
// Secrets the gateway writes from values submitted through the admin API.
//
// spec: §4.9 (Credential Storage Topology)
const ManagedSecretPrefix = "lenny-cred-"

// DefaultNamespace is the default credentials namespace, the default of the
// Helm value credentials.namespace and of the gateway and Token Service
// flags that carry it.
//
// spec: §4.9 (Credential Storage Topology), §17.6 (Packaging and Installation)
const DefaultNamespace = "lenny-credentials"

// managedHashHexLen is the number of hexadecimal SHA-256 characters a
// Lenny-written Secret name and its tenant-hash label carry.
const managedHashHexLen = 40

// ErrInvalidSecretRef reports a secretRef that is neither a bare
// DNS-1123 subdomain name nor "<credentials namespace>/<name>". The admin
// handlers map it to 400 VALIDATION_ERROR with details.field "secretRef".
var ErrInvalidSecretRef = errors.New("invalid secretRef")

// ParseSecretRef returns the Secret name a ref names. A ref is either
// "<name>" or "<credNS>/<name>", where <name> is a DNS-1123 subdomain
// name. Every other ref, including one that names a namespace other than
// credNS, carries more than two segments, or has an empty segment, is
// refused with an error wrapping ErrInvalidSecretRef. An empty credNS
// refuses every ref, so a caller without a configured namespace fails
// closed.
//
// The reserved ManagedSecretPrefix is not checked here: whether a
// prefixed ref is acceptable depends on the stored pool entry, which the
// caller holds.
//
// spec: §4.9 (Credential Storage Topology)
func ParseSecretRef(ref, credNS string) (string, error) {
	if credNS == "" {
		return "", fmt.Errorf("%w: credentials namespace is not configured", ErrInvalidSecretRef)
	}
	segments := strings.Split(ref, "/")
	switch len(segments) {
	case 1:
		return validateSecretName(segments[0])
	case 2:
		if segments[0] != credNS {
			return "", fmt.Errorf("%w: namespace %q is not the credentials namespace", ErrInvalidSecretRef, segments[0])
		}
		return validateSecretName(segments[1])
	default:
		return "", fmt.Errorf("%w: %d path segments, want <name> or <namespace>/<name>", ErrInvalidSecretRef, len(segments))
	}
}

// validateSecretName returns name when it is a DNS-1123 subdomain name.
func validateSecretName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty Secret name", ErrInvalidSecretRef)
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return "", fmt.Errorf("%w: Secret name %q: %s", ErrInvalidSecretRef, name, strings.Join(errs, "; "))
	}
	return name, nil
}

// CanonicalSecretRef returns the stored form "<credNS>/<name>" of a ref.
//
// spec: §4.9 (Credential Storage Topology)
func CanonicalSecretRef(credNS, name string) string {
	return credNS + "/" + name
}

// ManagedSecretName returns the name of the Secret the gateway writes for
// one credential: ManagedSecretPrefix followed by the first 40
// hexadecimal characters of SHA-256 over tenant, pool, and credID joined
// by NUL bytes. NUL cannot appear in any member, so distinct tuples never
// hash the same input.
//
// spec: §4.9 (Credential Storage Topology)
func ManagedSecretName(tenant, pool, credID string) string {
	return ManagedSecretPrefix + hashHex(tenant+"\x00"+pool+"\x00"+credID)
}

// ManagedSecretTenantHash returns the lenny.dev/tenant-hash label value of
// a Lenny-written Secret: the first 40 hexadecimal characters of SHA-256
// over the tenant id. Tenant deletion selects the tenant's Secrets by it.
//
// spec: §4.9 (Credential Storage Topology), §12.8 (Compliance Interfaces)
func ManagedSecretTenantHash(tenant string) string {
	return hashHex(tenant)
}

// IsManaged reports whether name carries ManagedSecretPrefix.
//
// spec: §4.9 (Credential Storage Topology)
func IsManaged(name string) bool {
	return strings.HasPrefix(name, ManagedSecretPrefix)
}

// hashHex returns the first managedHashHexLen hexadecimal characters of
// SHA-256 over s.
func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:managedHashHexLen]
}
