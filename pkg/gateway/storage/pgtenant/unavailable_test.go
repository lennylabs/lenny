// SPDX-License-Identifier: MIT

package pgtenant

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// spec: §13.3 line 591 / line 601 — the connectivity classifier that
// distinguishes a Postgres outage (mapped to 503 token_store_unavailable
// / token_validation_unavailable) from a genuine internal error or a
// constraint violation (mapped to 500). F-13.3.4.
func TestIsUnavailable_F1334(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline", fmt.Errorf("pgtenant: begin: %w", context.DeadlineExceeded), true},
		{"connect error", &pgconn.ConnectError{}, true},
		{"wrapped connect error", fmt.Errorf("dial: %w", &pgconn.ConnectError{}), true},
		{"pg connection_exception 08000", &pgconn.PgError{Code: "08000"}, true},
		{"pg connection_failure 08006", &pgconn.PgError{Code: "08006"}, true},
		{"pg admin_shutdown 57P01", &pgconn.PgError{Code: "57P01"}, true},
		{"pg cannot_connect_now 57P03", &pgconn.PgError{Code: "57P03"}, true},
		{"pg too_many_connections 53300", &pgconn.PgError{Code: "53300"}, true},
		{"net op error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		// Non-unavailability: a unique-violation is a real conflict, not an
		// outage; a foreign-key violation and a plain error stay 500.
		{"unique_violation 23505", &pgconn.PgError{Code: "23505"}, false},
		{"foreign_key_violation 23503", &pgconn.PgError{Code: "23503"}, false},
		{"context canceled", context.Canceled, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnavailable(tc.err); got != tc.want {
				t.Errorf("IsUnavailable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
