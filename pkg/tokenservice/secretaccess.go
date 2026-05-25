// SPDX-License-Identifier: MIT

package tokenservice

import "context"

// SecretAccessVerdict is the §4.9 admin-time RBAC-probe outcome the
// Token Service returns for a named Kubernetes Secret.
//
// spec: spec/04_system-components.md §4.9 line 1212.
type SecretAccessVerdict int

const (
	// SecretAccessAllowed — the Token Service ServiceAccount can get the
	// Secret and the Secret exists.
	SecretAccessAllowed SecretAccessVerdict = iota
	// SecretAccessDenied — the SelfSubjectAccessReview denied the get
	// verb on the Secret (the resourceName is not in the Token Service
	// Role).
	SecretAccessDenied
	// SecretAccessNotFound — the get verb is allowed but the Secret
	// object does not exist.
	SecretAccessNotFound
)

// SecretAccessProber answers whether the Token Service's own
// ServiceAccount can read a named Kubernetes Secret. The k8s-backed
// implementation lives in pkg/tokenservice/secretprobe; the gRPC server
// depends only on this interface so the core package stays free of a
// client-go dependency and the probe is unit-testable with a fake.
//
// The probe is Token-Service-owned per §4.9: the gateway never
// impersonates the Token Service ServiceAccount nor performs the review
// under its own identity. A definitive ALLOWED/DENIED/NOT_FOUND is
// returned as a verdict with a nil error; any non-deterministic failure
// (API timeout, transport error) is returned as a non-nil error so the
// RPC maps it to codes.Unavailable and the caller never fails open.
type SecretAccessProber interface {
	ProbeSecretAccess(ctx context.Context, namespace, name string) (SecretAccessVerdict, error)
}
