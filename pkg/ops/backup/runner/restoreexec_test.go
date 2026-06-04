// SPDX-License-Identifier: MIT

package runner

import (
	"archive/tar"
	"bytes"
	"context"
	"testing"
)

// buildPostgresComponent assembles a postgres component the way
// ExecDumper.DumpPostgres does: a tar of postgres/shard-N.dump entries.
func buildPostgresComponent(t *testing.T, shards [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i, d := range shards {
		hdr := &tar.Header{Name: tarShardName(i), Mode: 0o600, Size: int64(len(d))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(d); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

func tarShardName(i int) string {
	return "postgres/shard-" + string(rune('0'+i)) + ".dump"
}

// spec: §25.11 — the verify/restore read path reverses TarGzArchiver. A
// plaintext archive round-trips through the opener to the shard dumps.
func TestTarGzOpenerPlaintextRoundTrip_spec_25_11(t *testing.T) {
	shards := [][]byte{[]byte("dump-shard-0"), []byte("dump-shard-1")}
	pg := buildPostgresComponent(t, shards)
	arc, err := (&TarGzArchiver{}).Pack(context.Background(), []Component{{Name: "postgres", Bytes: pg}})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	dumps, err := (&TarGzOpener{}).ExtractPostgresDumps(context.Background(), arc.Data)
	if err != nil {
		t.Fatalf("ExtractPostgresDumps: %v", err)
	}
	if len(dumps) != 2 {
		t.Fatalf("got %d dumps, want 2", len(dumps))
	}
	for i, want := range shards {
		if !bytes.Equal(dumps[i], want) {
			t.Errorf("shard %d = %q, want %q", i, dumps[i], want)
		}
	}
}

// An encrypted archive round-trips through the opener under the same key.
func TestTarGzOpenerEncryptedRoundTrip_spec_25_11(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	shards := [][]byte{[]byte("enc-shard-0")}
	pg := buildPostgresComponent(t, shards)
	arc, err := (&TarGzArchiver{DataKey: key}).Pack(context.Background(), []Component{{Name: "postgres", Bytes: pg}})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if !arc.Encrypted {
		t.Fatal("expected the archive to be encrypted")
	}
	dumps, err := (&TarGzOpener{DataKey: key}).ExtractPostgresDumps(context.Background(), arc.Data)
	if err != nil {
		t.Fatalf("ExtractPostgresDumps: %v", err)
	}
	if len(dumps) != 1 || !bytes.Equal(dumps[0], shards[0]) {
		t.Fatalf("dumps = %q, want %q", dumps, shards)
	}
}

// A config-only archive (no postgres component) yields no dumps.
func TestTarGzOpenerConfigOnly(t *testing.T) {
	arc, err := (&TarGzArchiver{}).Pack(context.Background(), []Component{{Name: "config", Bytes: []byte("{}")}})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	dumps, err := (&TarGzOpener{}).ExtractPostgresDumps(context.Background(), arc.Data)
	if err != nil {
		t.Fatalf("ExtractPostgresDumps: %v", err)
	}
	if dumps != nil {
		t.Errorf("got %d dumps, want none", len(dumps))
	}
}

// ExecDumpInspector surfaces a missing pg_restore as an error rather
// than passing the verification.
func TestExecDumpInspectorMissingBinary(t *testing.T) {
	insp := &ExecDumpInspector{PgRestorePath: "/nonexistent/pg_restore_xyz"}
	if err := insp.ListDump(context.Background(), []byte("dump")); err == nil {
		t.Fatal("expected an error when pg_restore is absent")
	}
}

// ExecScratchRestorer requires a scratch DSN.
func TestExecScratchRestorerRequiresDSN(t *testing.T) {
	if err := (&ExecScratchRestorer{}).RestoreAndSmoke(context.Background(), [][]byte{[]byte("d")}); err == nil {
		t.Fatal("expected an error without a scratch DSN")
	}
}
