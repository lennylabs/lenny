// SPDX-License-Identifier: MIT

package pgstore

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
)

// TestClassifyErr_OperatorIntervention pins the §25.4 durable-tier
// fall-through contract at the connectivity classifier. The Storage Tiers
// (Query Path) require that when Postgres is down the highest available
// store takes over ("Postgres down, Redis available: queries scan Redis
// ..."); the create path likewise falls from Tier 1 to Tier 2 when the
// Postgres insert cannot durably record. During a Postgres administrator
// shutdown or failover window the server answers an in-flight request with
// a FATAL operator_intervention SQLSTATE (57P01 admin_shutdown, 57P02
// crash_shutdown, 57P03 cannot_connect_now) or a connection-exception
// class-08 code before the connection drops. Those are Postgres-is-down
// signals, so classifyErr must map them to escalation.ErrStoreUnavailable
// to trigger the tier fall-through, rather than surfacing them as an
// internal error. A genuine server-side query error (a constraint
// violation, for example) is left intact so the caller surfaces it.
//
// spec: §25.4 lines 2422-2434 (Storage Tiers, Query Path; "Postgres down,
// Redis available" and "Both down" degraded fall-through).
func TestClassifyErr_OperatorIntervention_spec_25_4(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantUnavail   bool
		wantPassThrgh bool // want the original PgError returned intact
	}{
		{"nil", nil, false, false},
		{"admin_shutdown 57P01", &pgconn.PgError{Code: "57P01"}, true, false},
		{"crash_shutdown 57P02", &pgconn.PgError{Code: "57P02"}, true, false},
		{"cannot_connect_now 57P03", &pgconn.PgError{Code: "57P03"}, true, false},
		{"connection_exception 08000", &pgconn.PgError{Code: "08000"}, true, false},
		{"connection_failure 08006", &pgconn.PgError{Code: "08006"}, true, false},
		{"wrapped admin_shutdown", fmt.Errorf("exec: %w", &pgconn.PgError{Code: "57P01"}), true, false},
		// A genuine server-side query error is not an outage; the caller
		// surfaces it. classifyErr returns it intact rather than masking it
		// as a store-unavailable fall-through.
		{"unique_violation 23505", &pgconn.PgError{Code: "23505"}, false, true},
		{"foreign_key_violation 23503", &pgconn.PgError{Code: "23503"}, false, true},
		{"check_violation 23514", &pgconn.PgError{Code: "23514"}, false, true},
		// A non-PgError transport failure (dial refused, closed pool) is the
		// already-handled Postgres-outage case.
		{"plain transport error", errors.New("connection refused"), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyErr(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("classifyErr(nil) = %v, want nil", got)
				}
				return
			}
			if tc.wantUnavail {
				if !errors.Is(got, escalation.ErrStoreUnavailable) {
					t.Errorf("classifyErr(%v) = %v, want escalation.ErrStoreUnavailable", tc.err, got)
				}
			}
			if tc.wantPassThrgh {
				var pgErr *pgconn.PgError
				if !errors.As(got, &pgErr) {
					t.Errorf("classifyErr(%v) = %v, want the original PgError surfaced intact", tc.err, got)
				}
				if errors.Is(got, escalation.ErrStoreUnavailable) {
					t.Errorf("classifyErr(%v) masked a query error as store-unavailable", tc.err)
				}
			}
		})
	}
}
