// SPDX-License-Identifier: MIT

package pgaudit

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeRow returns scanVals on Scan and scanErr if set.
type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		if i >= len(r.vals) {
			break
		}
		switch d := dest[i].(type) {
		case *bool:
			*d = r.vals[i].(bool)
		case **string:
			if r.vals[i] == nil {
				*d = nil
			} else {
				s := r.vals[i].(string)
				*d = &s
			}
		default:
			return errors.New("fakeRow: unsupported dest type")
		}
	}
	return nil
}

// fakeQuerier returns a queued fakeRow per QueryRow call in order: the
// first call is the pg_extension EXISTS probe, the second is the
// pgaudit.log read.
type fakeQuerier struct {
	rows []fakeRow
	n    int
}

func (q *fakeQuerier) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	r := q.rows[q.n]
	q.n++
	return r
}

func extensionRow(installed bool) fakeRow { return fakeRow{vals: []any{installed}} }
func logRow(setting any) fakeRow          { return fakeRow{vals: []any{setting}} }

// spec: §11.7 line 375 — the preflight passes when the extension is
// installed and pgaudit.log carries both ddl and role.
func TestPreflightPasses_spec_11_7_375(t *testing.T) {
	for _, setting := range []string{"ddl, role", "role,ddl", "all", "write, ddl, role, misc", "all, -misc"} {
		q := &fakeQuerier{rows: []fakeRow{extensionRow(true), logRow(setting)}}
		if err := Preflight(context.Background(), q); err != nil {
			t.Fatalf("setting %q: Preflight = %v, want nil", setting, err)
		}
	}
}

// spec: §11.7 line 375 — a missing extension fails with
// ErrExtensionNotInstalled (no log read attempted).
func TestPreflightExtensionMissing_spec_11_7_375(t *testing.T) {
	q := &fakeQuerier{rows: []fakeRow{extensionRow(false)}}
	if err := Preflight(context.Background(), q); !errors.Is(err, ErrExtensionNotInstalled) {
		t.Fatalf("err = %v, want ErrExtensionNotInstalled", err)
	}
}

// spec: §11.7 line 375 — pgaudit.log without both ddl and role (including
// an unset NULL setting and an `all, -role` subtraction) fails with
// ErrLogClassesMissing.
func TestPreflightLogClassesMissing_spec_11_7_375(t *testing.T) {
	cases := []any{nil, "", "ddl", "role", "write, function", "all, -role", "all, -ddl"}
	for _, setting := range cases {
		q := &fakeQuerier{rows: []fakeRow{extensionRow(true), logRow(setting)}}
		if err := Preflight(context.Background(), q); !errors.Is(err, ErrLogClassesMissing) {
			t.Fatalf("setting %v: err = %v, want ErrLogClassesMissing", setting, err)
		}
	}
}

// logClassesCoverDDLAndRole table check, independent of the DB probe.
func TestLogClassesCoverDDLAndRole(t *testing.T) {
	cover := map[string]bool{
		"ddl,role":         true,
		"ROLE, DDL":        true,
		"all":              true,
		"all,-misc":        true,
		"read,write":       false,
		"ddl":              false,
		"role":             false,
		"all,-ddl,-role":   false,
		"":                 false,
		" ddl , role , - ": true, // trailing "-" trims to "" -> ignored
	}
	for setting, want := range cover {
		if got := logClassesCoverDDLAndRole(setting); got != want {
			t.Errorf("logClassesCoverDDLAndRole(%q) = %v, want %v", setting, got, want)
		}
	}
}
