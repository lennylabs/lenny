// SPDX-License-Identifier: MIT

package runner_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

// fakePgDump writes a script at <dir>/pg_dump that emits body on stdout
// regardless of its arguments, so DumpPostgres can be exercised without a
// live Postgres.
func fakePgDump(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake pg_dump script needs a POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pg_dump")
	script := "#!/bin/sh\ncat <<'LENNY_EOF'\n" + body + "\nLENNY_EOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}
	return path
}

// readPostgresShards untars the postgres component of a DumpPostgres
// result and returns each postgres/shard-* entry by name.
func readPostgresShards(t *testing.T, component []byte) map[string]string {
	t.Helper()
	shards := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(component))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read postgres tar: %v", err)
		}
		if !strings.HasPrefix(hdr.Name, "postgres/shard-") {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read shard %s: %v", hdr.Name, err)
		}
		shards[hdr.Name] = string(b)
	}
	return shards
}

// spec: §25.11 contentPolicy.redactColumns — when redactColumns is set
// the shard dump switches to plain format (.sql) and the matched columns
// are rewritten to [REDACTED].
func TestDumpPostgresRedactsPlainFormat_spec_25_11_4012(t *testing.T) {
	body := "--\n-- PostgreSQL database dump\n--\n" +
		"COPY public.tenant_secrets (id, api_key) FROM stdin;\n1\tsk-leak\n\\.\n"
	d := &runner.ExecDumper{
		PgDumpPath:    fakePgDump(t, body),
		ShardDSNs:     []string{"postgres://shard0"},
		RedactColumns: []string{"api_key"},
	}
	comp, err := d.DumpPostgres(context.Background())
	if err != nil {
		t.Fatalf("DumpPostgres: %v", err)
	}
	shards := readPostgresShards(t, comp.Bytes)
	got, ok := shards["postgres/shard-0.sql"]
	if !ok {
		t.Fatalf("redacted dump not stored as a .sql shard: %v", keys(shards))
	}
	if _, hasCustom := shards["postgres/shard-0.dump"]; hasCustom {
		t.Error("redacting run also emitted a custom-format .dump shard")
	}
	if strings.Contains(got, "sk-leak") {
		t.Errorf("redacted shard still leaks the api_key:\n%s", got)
	}
	if !strings.Contains(got, "1\t[REDACTED]") {
		t.Errorf("api_key column not redacted:\n%s", got)
	}
}

// Without redactColumns the dump stays in custom format (.dump) and is
// not text-filtered. spec: §25.11 step-1 (--format=custom).
func TestDumpPostgresCustomFormatWithoutRedact_spec_25_11_3999(t *testing.T) {
	body := "PGDMP-custom-archive-bytes"
	d := &runner.ExecDumper{
		PgDumpPath: fakePgDump(t, body),
		ShardDSNs:  []string{"postgres://shard0"},
	}
	comp, err := d.DumpPostgres(context.Background())
	if err != nil {
		t.Fatalf("DumpPostgres: %v", err)
	}
	shards := readPostgresShards(t, comp.Bytes)
	got, ok := shards["postgres/shard-0.dump"]
	if !ok {
		t.Fatalf("non-redacting run did not emit a .dump shard: %v", keys(shards))
	}
	if !strings.Contains(got, "PGDMP") {
		t.Errorf("custom-format shard altered:\n%s", got)
	}
}

// spec: §25.11 — the archive internal manifest records the redacted
// columns so restore tooling detects plain, column-redacted shards.
func TestArchiveManifestRecordsRedactedColumns_spec_25_11_4012(t *testing.T) {
	a := &runner.TarGzArchiver{RedactedColumns: []string{"tenant_secrets.api_key"}}
	archive, err := a.Pack(context.Background(), []runner.Component{{Name: "postgres", Bytes: []byte("x")}})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	var manifest struct {
		RedactedColumns []string `json:"redactedColumns"`
	}
	if err := json.Unmarshal(readArchiveManifest(t, archive.Data), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(manifest.RedactedColumns) != 1 || manifest.RedactedColumns[0] != "tenant_secrets.api_key" {
		t.Errorf("manifest redactedColumns = %v, want [tenant_secrets.api_key]", manifest.RedactedColumns)
	}
}

// readArchiveManifest decompresses the archive and returns manifest.json.
func readArchiveManifest(t *testing.T, archive []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read archive tar: %v", err)
		}
		if hdr.Name == "manifest.json" {
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read manifest: %v", err)
			}
			return b
		}
	}
	t.Fatal("manifest.json not found in archive")
	return nil
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
