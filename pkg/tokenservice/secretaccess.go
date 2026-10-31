// SPDX-License-Identifier: MIT

package tokenservice

import "context"

// SecretAccessVerdict is the outcome of one §4.9 Token Service read of a
// credential Secret.
//
// spec: §4.9 Token Service secret resolution.
type SecretAccessVerdict int

const (
	// SecretAccessAllowed: the get returned the Secret and, on a keyed
	// read, the named key holds a non-empty value.
	SecretAccessAllowed SecretAccessVerdict = iota
	// SecretAccessDenied: the get returned Forbidden (the chart Role is
	// absent).
	SecretAccessDenied
	// SecretAccessNotFound: the Secret does not exist in the credentials
	// namespace, or a keyed read named a key that is missing or empty.
	SecretAccessNotFound
)

// SecretAccessProber reads one credential Secret in the Token Service's
// configured credentials namespace. The k8s-backed implementation lives in
// pkg/tokenservice/secretprobe; the gRPC server depends only on this
// interface so the core package stays free of a client-go dependency and
// the read is unit-testable with a fake.
//
// A definitive ALLOWED, DENIED, or NOT_FOUND is returned as a verdict with
// a nil error. Any other failure is returned as a non-nil error, which the
// RPC maps to codes.Unavailable so the caller never fails open. When key
// is non-empty and the verdict is ALLOWED, the returned bytes are that
// key's value; an unkeyed read never returns a value.
//
// spec: §4.9 Token Service secret resolution.
type SecretAccessProber interface {
	ProbeSecretAccess(ctx context.Context, name, key string) (SecretAccessVerdict, []byte, error)
}
