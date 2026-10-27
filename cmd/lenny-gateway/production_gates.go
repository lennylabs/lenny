// SPDX-License-Identifier: MIT

package main

import "github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"

// The gateway's production-only startup refusals, each a pure decision over
// the production-mode bit and the condition the gate checks. The caller
// computes production from the finalized --environment value through
// deployenv.IsProduction and acts on the result with log.Fatalf.
//
// spec: §17.9.1 (Composition Dimensions) "Production mode"; §11.7 (Audit Logging); §12.3 (Postgres HA Requirements)

// siemGateFatal reports whether the §11.7 regulated-tenant SIEM check refuses
// startup: production mode and the regulated-tenant-without-SIEM error. Any
// other error (a failed tenant listing) is not the refusal.
func siemGateFatal(production bool, err error) bool {
	return production && err != nil && err.Error() == admin.SIEMStartupFatalMessage
}

// pgauditGateFatal reports whether the §11.7 regulated-tenant pgaudit check
// refuses startup: production mode and the regulated-tenant-without-pgaudit
// error. Any other error (a failed tenant listing) is not the refusal.
func pgauditGateFatal(production bool, err error) bool {
	return production && err != nil && err.Error() == admin.PgauditStartupFatalMessage
}

// pgauditPreflightFatal reports whether a failed §11.7 pgaudit extension
// preflight refuses startup: production mode with a regulated tenant present.
func pgauditPreflightFatal(production, regulatedPresent bool) bool {
	return production && regulatedPresent
}

// auditIntegrityFatal reports whether a failed §11.7 / §12.3 append-only
// ledger integrity check refuses startup: production mode and a non-nil
// verification error.
func auditIntegrityFatal(production bool, err error) bool {
	return production && err != nil
}
