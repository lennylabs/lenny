// SPDX-License-Identifier: MIT

package runner

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"unicode/utf8"
)

// redactPlaceholder is the value written in place of a matched column's
// data. spec: §25.11 contentPolicy.redactColumns — "pg_dump output is
// piped through a sed filter that replaces matched columns with
// '[REDACTED]'". The column-name-aware filter below realizes that
// requirement; a bare positional sed cannot resolve a column name to its
// position in the COPY stream, so the filter parses each COPY header to
// find the named columns and rewrites only their fields.
const redactPlaceholder = `[REDACTED]`

// pgCustomDumpMagic is the leading marker of a pg_dump --format=custom
// archive. A --format=plain dump is SQL text and does not carry it.
var pgCustomDumpMagic = []byte("PGDMP")

// isCustomFormatDump reports whether b is a pg_dump --format=custom
// archive rather than a --format=plain SQL dump. The restore and verify
// paths sniff this to choose pg_restore (custom) versus psql (plain):
// a redacted dump is plain text so the column filter can rewrite it, so
// the dump format is no longer uniform across runs. spec: §25.11.
func isCustomFormatDump(b []byte) bool {
	return bytes.HasPrefix(b, pgCustomDumpMagic)
}

// redactTarget is one parsed backups.contentPolicy.redactColumns entry.
// A bare "api_key" redacts that column in every table; a qualified
// "tenant_secrets.api_key" (or "public.tenant_secrets.api_key") redacts
// it only in that table. The schema prefix, where present, is ignored on
// both the target and the COPY header so "tenant_secrets" matches
// "public.tenant_secrets".
type redactTarget struct {
	table  string // empty matches any table
	column string
}

// parseRedactTargets turns the redactColumns config list into match
// targets, dropping blank entries. spec: §25.11 contentPolicy.redactColumns.
func parseRedactTargets(cols []string) []redactTarget {
	targets := make([]redactTarget, 0, len(cols))
	for _, c := range cols {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		var t redactTarget
		if i := strings.LastIndex(c, "."); i >= 0 {
			t.table = stripSchema(unquoteIdent(c[:i]))
			t.column = unquoteIdent(c[i+1:])
		} else {
			t.column = unquoteIdent(c)
		}
		if t.column == "" {
			continue
		}
		targets = append(targets, t)
	}
	return targets
}

// redactCopyStream copies r to w, rewriting the data of every column
// named by targets to redactPlaceholder inside each plain-format
// pg_dump COPY block. Lines outside a COPY block, and COPY blocks whose
// columns match no target, pass through byte-for-byte. spec: §25.11
// contentPolicy.redactColumns.
func redactCopyStream(r io.Reader, w io.Writer, targets []redactTarget) error {
	br := bufio.NewReader(r)
	bw := bufio.NewWriter(w)
	inData := false
	var redactIdx map[int]bool
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			switch {
			case inData:
				// pg_dump terminates COPY data with a line that is exactly
				// "\.". Everything before it is a tab-delimited data row.
				if strings.TrimRight(line, "\r\n") == `\.` {
					inData = false
					redactIdx = nil
					if _, werr := bw.WriteString(line); werr != nil {
						return werr
					}
				} else if len(redactIdx) == 0 {
					if _, werr := bw.WriteString(line); werr != nil {
						return werr
					}
				} else if werr := writeRedactedRow(bw, line, redactIdx); werr != nil {
					return werr
				}
			default:
				if table, cols, ok := parseCopyHeader(line); ok {
					inData = true
					redactIdx = redactIndices(table, cols, targets)
				}
				if _, werr := bw.WriteString(line); werr != nil {
					return werr
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return bw.Flush()
}

// writeRedactedRow rewrites the redacted columns of one COPY data row and
// writes it, preserving the row's line terminator.
func writeRedactedRow(bw *bufio.Writer, line string, redactIdx map[int]bool) error {
	body := line
	term := ""
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		term = body[i:]
		body = body[:i]
	}
	fields := strings.Split(body, "\t")
	for i := range fields {
		if redactIdx[i] {
			fields[i] = redactPlaceholder
		}
	}
	if _, err := bw.WriteString(strings.Join(fields, "\t")); err != nil {
		return err
	}
	_, err := bw.WriteString(term)
	return err
}

// parseCopyHeader recognizes a plain-format pg_dump COPY header
// ("COPY <table> (col, ...) FROM stdin;") and returns the table name and
// the ordered column list. It returns ok=false for any other line,
// including the column-list-less legacy form ("COPY t FROM stdin;").
func parseCopyHeader(line string) (table string, cols []string, ok bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "COPY ") || !strings.HasSuffix(s, "FROM stdin;") {
		return "", nil, false
	}
	open := strings.IndexByte(s, '(')
	closeIdx := strings.LastIndexByte(s, ')')
	if open < 0 || closeIdx < open {
		return "", nil, false
	}
	table = strings.TrimSpace(s[len("COPY "):open])
	for _, c := range strings.Split(s[open+1:closeIdx], ",") {
		c = unquoteIdent(strings.TrimSpace(c))
		if c != "" {
			cols = append(cols, c)
		}
	}
	if table == "" || len(cols) == 0 {
		return "", nil, false
	}
	return table, cols, true
}

// redactIndices returns the column positions in cols that match any
// target for the COPY block's table.
func redactIndices(table string, cols []string, targets []redactTarget) map[int]bool {
	bare := stripSchema(unquoteIdent(table))
	idx := make(map[int]bool)
	for i, col := range cols {
		for _, t := range targets {
			if t.column != col {
				continue
			}
			if t.table != "" && t.table != bare {
				continue
			}
			idx[i] = true
		}
	}
	return idx
}

// stripSchema returns the identifier after the last dot, so a
// schema-qualified table name compares on its bare name.
func stripSchema(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// unquoteIdent removes the surrounding double quotes pg_dump applies to
// identifiers that need quoting and collapses the doubled-quote escape.
func unquoteIdent(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}

// validatePlainDump proves a plain-format (redacted) shard dump is
// readable text rather than a corrupted or truncated stream. It is the
// verify-mode counterpart to pg_restore --list, which only handles
// custom-format archives; the authoritative restorability proof for a
// plain dump is the scratch-DB restore-test. spec: §25.11 Backup
// Verification.
func validatePlainDump(dump []byte) bool {
	if len(dump) == 0 || !utf8.Valid(dump) {
		return false
	}
	for _, marker := range [][]byte{
		[]byte("PostgreSQL database dump"),
		[]byte("COPY "),
		[]byte("CREATE "),
		[]byte("INSERT INTO "),
	} {
		if bytes.Contains(dump, marker) {
			return true
		}
	}
	return false
}
