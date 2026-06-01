// SPDX-License-Identifier: MIT

package gatewaycontrol

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// SATokenCredentials is a gRPC per-RPC credential that attaches the
// pod's projected ServiceAccount token to every outbound GatewayControl
// call as an `authorization: Bearer <jwt>` header. The gateway validates
// the token's audience claim (the §10.3 deployment-specific audience) on
// every pod→gateway request; this is the SA-token layer of the §10.3
// defense-in-depth chain that sits alongside the mTLS SPIFFE check.
//
// The token is re-read from the projected-volume path on every call so a
// kubelet token refresh (the 900s expiry is auto-refreshed before expiry)
// is picked up without reconstructing the client. RequireTransportSecurity
// is true so the bearer token is only ever sent over the mTLS link, never
// in plaintext.
// spec: §10.3 line 334 (Projected SA token).
type SATokenCredentials struct {
	// TokenPath is the projected SA token file (the pod mounts it at
	// /var/run/secrets/lenny.dev/serviceaccount/token).
	TokenPath string
}

// NewSATokenCredentials builds a per-RPC credential reading the projected
// SA token from tokenPath. Pass the result to Dial via
// grpc.WithPerRPCCredentials.
func NewSATokenCredentials(tokenPath string) SATokenCredentials {
	return SATokenCredentials{TokenPath: tokenPath}
}

// GetRequestMetadata reads the projected token and returns it as the
// authorization bearer header.
func (c SATokenCredentials) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	raw, err := os.ReadFile(c.TokenPath)
	if err != nil {
		return nil, fmt.Errorf("gatewaycontrol: read projected SA token %s: %w", c.TokenPath, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return nil, fmt.Errorf("gatewaycontrol: projected SA token %s is empty", c.TokenPath)
	}
	return map[string]string{"authorization": "Bearer " + token}, nil
}

// RequireTransportSecurity reports that the bearer token must only travel
// over a secure transport, so gRPC refuses to attach it to a plaintext
// connection.
func (c SATokenCredentials) RequireTransportSecurity() bool { return true }

// WithSAToken returns a dial option that attaches the projected SA token
// to every GatewayControl call. It is a no-op (nil-equivalent) option
// when tokenPath is empty so the local-development plaintext path is
// unaffected.
func WithSAToken(tokenPath string) grpc.DialOption {
	if tokenPath == "" {
		return grpc.EmptyDialOption{}
	}
	return grpc.WithPerRPCCredentials(NewSATokenCredentials(tokenPath))
}

var _ credentials.PerRPCCredentials = SATokenCredentials{}
