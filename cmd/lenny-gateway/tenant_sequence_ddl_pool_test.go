// SPDX-License-Identifier: MIT

package main

import "testing"

// TestAliasPrimaryDDLToBillingAudit pins the single-instance vs
// separate-instance topology decision for the primary DDL pool. The
// per-tenant audit_seq_ sequence the §13.3 issued-token write-before-issue
// path seals on the primary must be created through a CREATE-privileged
// connection to the primary. In the single-instance topology (no separate
// billing/audit instance, no distinct primary DDL DSN) the primary and the
// billing/audit instance are one, so the single billing/audit DDL pool
// addresses both. In every other combination the primary DDL pool is opened
// from LENNY_PG_PRIMARY_DDL_DSN, so aliasing must not happen.
//
// A regression that aliased in the separate-instance topology would point the
// primary CREATE SEQUENCE at the wrong instance and the §13.3 nextval would
// fail on a nonexistent relation. This test asserts the alias only where the
// two instances are genuinely one.
//
// spec: §12.3, §15.1. F-11.2.10
func TestAliasPrimaryDDLToBillingAudit(t *testing.T) {
	cases := []struct {
		name            string
		billingAuditDSN string
		primaryDDLDSN   string
		wantAlias       bool
	}{
		{
			name:      "single instance: no separate billing/audit, no primary ddl dsn -> alias",
			wantAlias: true,
		},
		{
			name:            "separate billing/audit instance, no primary ddl dsn -> no alias",
			billingAuditDSN: "postgres://ba@host/lenny",
			wantAlias:       false,
		},
		{
			name:          "single instance but explicit primary ddl dsn -> no alias (own pool)",
			primaryDDLDSN: "postgres://pddl@host/lenny",
			wantAlias:     false,
		},
		{
			name:            "separate instance with explicit primary ddl dsn -> no alias",
			billingAuditDSN: "postgres://ba@host/lenny",
			primaryDDLDSN:   "postgres://pddl@host/lenny",
			wantAlias:       false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := aliasPrimaryDDLToBillingAudit(tc.billingAuditDSN, tc.primaryDDLDSN); got != tc.wantAlias {
				t.Errorf("aliasPrimaryDDLToBillingAudit(%q, %q) = %v, want %v",
					tc.billingAuditDSN, tc.primaryDDLDSN, got, tc.wantAlias)
			}
		})
	}
}
