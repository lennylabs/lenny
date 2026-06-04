// SPDX-License-Identifier: MIT

package runner

import (
	"bytes"
	"strings"
	"testing"
)

// spec: §25.11 contentPolicy.redactColumns — a bare column name redacts
// that column in every table; a table.column qualifier redacts it only
// in the named table; the schema prefix is ignored on both sides.
func TestParseRedactTargets_spec_25_11_4012(t *testing.T) {
	got := parseRedactTargets([]string{
		"api_key",
		"tenant_secrets.token",
		"public.users.password",
		`"Weird Col"`,
		"   ",
		"",
	})
	want := []redactTarget{
		{column: "api_key"},
		{table: "tenant_secrets", column: "token"},
		{table: "users", column: "password"},
		{column: "Weird Col"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d targets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

const sampleDump = `--
-- PostgreSQL database dump
--
SET statement_timeout = 0;

COPY public.tenant_secrets (id, tenant_id, api_key) FROM stdin;
1	acme	sk-secret-1
2	globex	\N
\.

COPY public.users (id, email, api_key) FROM stdin;
10	alice@acme.com	user-key
\.
`

// spec: §25.11 contentPolicy.redactColumns — a bare column name redacts
// the column in every table that has it.
func TestRedactCopyStreamBareColumn_spec_25_11_4012(t *testing.T) {
	var out bytes.Buffer
	if err := redactCopyStream(strings.NewReader(sampleDump), &out, parseRedactTargets([]string{"api_key"})); err != nil {
		t.Fatalf("redactCopyStream: %v", err)
	}
	got := out.String()
	for _, leak := range []string{"sk-secret-1", "user-key"} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted output still contains %q:\n%s", leak, got)
		}
	}
	// Both api_key columns (index 2) are rewritten, including the NULL.
	if !strings.Contains(got, "1\tacme\t[REDACTED]") {
		t.Errorf("tenant_secrets row not redacted:\n%s", got)
	}
	if !strings.Contains(got, "2\tglobex\t[REDACTED]") {
		t.Errorf("NULL value not redacted:\n%s", got)
	}
	if !strings.Contains(got, "10\talice@acme.com\t[REDACTED]") {
		t.Errorf("users row not redacted:\n%s", got)
	}
	// Non-targeted columns and SQL preamble are untouched.
	if !strings.Contains(got, "SET statement_timeout = 0;") || !strings.Contains(got, "alice@acme.com") {
		t.Errorf("non-redacted content was altered:\n%s", got)
	}
}

// spec: §25.11 contentPolicy.redactColumns — a table.column qualifier
// redacts the column only in the named table.
func TestRedactCopyStreamQualifiedColumn_spec_25_11_4012(t *testing.T) {
	var out bytes.Buffer
	if err := redactCopyStream(strings.NewReader(sampleDump), &out, parseRedactTargets([]string{"tenant_secrets.api_key"})); err != nil {
		t.Fatalf("redactCopyStream: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "sk-secret-1") {
		t.Errorf("tenant_secrets.api_key was not redacted:\n%s", got)
	}
	// The users.api_key column is left intact: the qualifier is table-scoped.
	if !strings.Contains(got, "10\talice@acme.com\tuser-key") {
		t.Errorf("users.api_key was redacted despite a table-scoped target:\n%s", got)
	}
}

// A COPY block whose columns match no target passes through byte-for-byte.
func TestRedactCopyStreamNoMatchPassthrough(t *testing.T) {
	var out bytes.Buffer
	if err := redactCopyStream(strings.NewReader(sampleDump), &out, parseRedactTargets([]string{"nonexistent"})); err != nil {
		t.Fatalf("redactCopyStream: %v", err)
	}
	if out.String() != sampleDump {
		t.Errorf("no-match redaction altered the dump:\ngot:\n%s\nwant:\n%s", out.String(), sampleDump)
	}
}

// Empty targets leave the dump unchanged (the redact path is not entered).
func TestRedactCopyStreamEmptyTargets(t *testing.T) {
	var out bytes.Buffer
	if err := redactCopyStream(strings.NewReader(sampleDump), &out, nil); err != nil {
		t.Fatalf("redactCopyStream: %v", err)
	}
	if out.String() != sampleDump {
		t.Error("empty targets altered the dump")
	}
}

// A quoted, reserved-word column name in the COPY header is matched after
// unquoting; a tab embedded in data (escaped by pg_dump as \t) does not
// shift the column split because each row is one physical line.
func TestRedactCopyStreamQuotedColumn(t *testing.T) {
	dump := "COPY public.t (id, \"user\", note) FROM stdin;\n1\tbob\tsome\\tnote\n\\.\n"
	var out bytes.Buffer
	if err := redactCopyStream(strings.NewReader(dump), &out, parseRedactTargets([]string{"user"})); err != nil {
		t.Fatalf("redactCopyStream: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "1\t[REDACTED]\tsome\\tnote") {
		t.Errorf("quoted column not redacted or escaped tab mishandled:\n%s", got)
	}
}

// The column-list-less legacy COPY form is not redacted (no column
// positions are knowable); its data passes through.
func TestRedactCopyStreamLegacyCopyFormUnredacted(t *testing.T) {
	dump := "COPY t FROM stdin;\n1\tsecret\n\\.\n"
	var out bytes.Buffer
	if err := redactCopyStream(strings.NewReader(dump), &out, parseRedactTargets([]string{"secret"})); err != nil {
		t.Fatalf("redactCopyStream: %v", err)
	}
	if out.String() != dump {
		t.Errorf("legacy COPY form was altered:\n%s", out.String())
	}
}

func TestParseCopyHeader(t *testing.T) {
	table, cols, ok := parseCopyHeader("COPY public.tenant_secrets (id, api_key) FROM stdin;\n")
	if !ok || table != "public.tenant_secrets" || len(cols) != 2 || cols[1] != "api_key" {
		t.Fatalf("parseCopyHeader = (%q, %v, %v)", table, cols, ok)
	}
	for _, neg := range []string{
		"SELECT 1;",
		"COPY t FROM stdin;",          // no column list
		"-- COPY public.t (a) FROM x", // not a stdin copy
		"1\tdata\trow",                // a data row
	} {
		if _, _, ok := parseCopyHeader(neg); ok {
			t.Errorf("parseCopyHeader(%q) reported a header", neg)
		}
	}
}

func TestIsCustomFormatDump(t *testing.T) {
	if !isCustomFormatDump([]byte("PGDMP\x01\x0e\x00")) {
		t.Error("custom-format magic not recognized")
	}
	if isCustomFormatDump([]byte("--\n-- PostgreSQL database dump\n")) {
		t.Error("plain SQL dump misidentified as custom format")
	}
	if isCustomFormatDump(nil) {
		t.Error("empty input misidentified as custom format")
	}
}

func TestValidatePlainDump(t *testing.T) {
	if !validatePlainDump([]byte("--\n-- PostgreSQL database dump\nCOPY t (a) FROM stdin;\n")) {
		t.Error("a valid plain dump was rejected")
	}
	if validatePlainDump(nil) {
		t.Error("an empty dump was accepted")
	}
	if validatePlainDump([]byte{0xff, 0xfe, 0xfd}) {
		t.Error("invalid UTF-8 was accepted")
	}
	if validatePlainDump([]byte("just some prose, no SQL markers")) {
		t.Error("a non-SQL blob was accepted")
	}
}
