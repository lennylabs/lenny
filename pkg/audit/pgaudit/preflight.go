// SPDX-License-Identifier: MIT

package pgaudit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Preflight errors. The §11.7 line 375 startup check returns one of
// these so the gateway can distinguish a missing extension from a
// misconfigured log-class list when it decides whether to refuse to
// start.
var (
	// ErrExtensionNotInstalled reports that the `pgaudit` extension is
	// absent from the connected Postgres cluster.
	ErrExtensionNotInstalled = errors.New("pgaudit preflight: pgaudit extension is not installed")
	// ErrLogClassesMissing reports that `pgaudit.log` does not include
	// both the DDL and ROLE classes.
	ErrLogClassesMissing = errors.New("pgaudit preflight: pgaudit.log must include the DDL and ROLE classes")
)

// preflightQuerier is the minimal Postgres handle Preflight needs. A
// *pgxpool.Pool satisfies it; tests supply a fake.
type preflightQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Preflight implements the §11.7 line 375 pgaudit startup check: it
// verifies that the `pgaudit` extension is installed on the connected
// Postgres cluster and that the `pgaudit.log` setting includes both the
// DDL and ROLE classes. It returns ErrExtensionNotInstalled or
// ErrLogClassesMissing on a failed check, or a wrapped query error.
//
// The gateway runs this when `audit.pgaudit.enabled` is true; a failure
// is fatal in production mode when any active tenant carries a regulated
// complianceProfile. spec: §11.7 lines 374-379.
func Preflight(ctx context.Context, q preflightQuerier) error {
	var installed bool
	if err := q.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pgaudit')").Scan(&installed); err != nil {
		return fmt.Errorf("pgaudit preflight: query pg_extension: %w", err)
	}
	if !installed {
		return ErrExtensionNotInstalled
	}

	// pgaudit.log is NULL when the GUC has never been set; the missing_ok
	// form of current_setting returns SQL NULL, scanned into a nil
	// *string. An unset or empty value carries neither required class.
	var logSetting *string
	if err := q.QueryRow(ctx,
		"SELECT current_setting('pgaudit.log', true)").Scan(&logSetting); err != nil {
		return fmt.Errorf("pgaudit preflight: read pgaudit.log: %w", err)
	}
	setting := ""
	if logSetting != nil {
		setting = *logSetting
	}
	if !logClassesCoverDDLAndRole(setting) {
		return fmt.Errorf("%w (pgaudit.log = %q)", ErrLogClassesMissing, setting)
	}
	return nil
}

// logClassesCoverDDLAndRole reports whether the pgaudit.log class list
// enables both the `ddl` and `role` classes. The setting is a
// comma-separated, case-insensitive list of classes; `all` enables every
// class, and a leading `-` on a class subtracts it (meaningful after
// `all`). spec: §11.7 line 375.
func logClassesCoverDDLAndRole(setting string) bool {
	// allClasses is the closed pgaudit class set that `all` expands to.
	allClasses := []string{"read", "write", "function", "role", "ddl", "misc", "misc_set"}
	enabled := map[string]bool{}
	for _, raw := range strings.Split(setting, ",") {
		tok := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case tok == "":
			continue
		case tok == "all":
			for _, c := range allClasses {
				enabled[c] = true
			}
		case strings.HasPrefix(tok, "-"):
			delete(enabled, strings.TrimPrefix(tok, "-"))
		default:
			enabled[tok] = true
		}
	}
	return enabled["ddl"] && enabled["role"]
}
