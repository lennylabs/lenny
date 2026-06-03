// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"errors"
	"testing"
)

// TestGatewayAuthCheck covers the §25.4 gateway-auth self-health check
// (lines 1956-1971): healthy on a successful probe, unhealthy on a
// service-account TokenError, degraded on a reachability error, and
// healthy (not-applicable) when no probe is wired.
func TestGatewayAuthCheck_spec_25_4(t *testing.T) {
	tests := []struct {
		name  string
		probe GatewayAuthProbe
		want  HealthStatus
	}{
		{
			name:  "nil probe reports healthy",
			probe: nil,
			want:  StatusHealthy,
		},
		{
			name:  "successful probe is healthy",
			probe: func(context.Context) error { return nil },
			want:  StatusHealthy,
		},
		{
			name:  "token error is unhealthy",
			probe: func(context.Context) error { return &TokenError{Err: errors.New("token below floor")} },
			want:  StatusUnhealthy,
		},
		{
			name:  "reachability error is degraded",
			probe: func(context.Context) error { return errors.New("connection refused") },
			want:  StatusDegraded,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := GatewayAuthCheck(tc.probe)()
			if res.Name != CheckGatewayAuth {
				t.Fatalf("Name = %q, want %q", res.Name, CheckGatewayAuth)
			}
			if res.Status != tc.want {
				t.Fatalf("Status = %v, want %v (detail: %q)", res.Status, tc.want, res.Detail)
			}
		})
	}
}

// TestTokenError_Unwrap covers errors.As / errors.Is over the wrapped
// cause so the check classifies a wrapped TokenError correctly.
func TestTokenError_Unwrap(t *testing.T) {
	cause := errors.New("root")
	err := error(&TokenError{Err: cause})
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is must reach the wrapped cause")
	}
	var te *TokenError
	if !errors.As(err, &te) {
		t.Fatal("errors.As must match *TokenError")
	}
}
