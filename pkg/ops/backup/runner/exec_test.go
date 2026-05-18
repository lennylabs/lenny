// SPDX-License-Identifier: MIT

package runner_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"io"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

func TestTarGzArchiverWithoutKeySkipsClientSideEncryption(t *testing.T) {
	a := &runner.TarGzArchiver{ExcludedTables: []string{"platform_secrets"}}
	archive, err := a.Pack(context.Background(), []runner.Component{
		{Name: "postgres", Bytes: []byte("dump")},
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	// §12.9 fallback: no client-side key, so the archive is not
	// encrypted (MinIO server-side encryption protects it on upload).
	if archive.Encrypted {
		t.Error("archive reports client-side encryption without a data key")
	}
	if archive.Checksum == "" {
		t.Error("archive has no checksum")
	}
	// The archive is a readable gzip tar carrying the manifest.
	assertArchiveHasManifest(t, archive.Data)
}

func TestTarGzArchiverWithKeyEncrypts(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	a := &runner.TarGzArchiver{DataKey: key}
	archive, err := a.Pack(context.Background(), []runner.Component{
		{Name: "postgres", Bytes: []byte("sensitive-dump-content")},
	})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if !archive.Encrypted {
		t.Fatal("archive reports no client-side encryption despite a data key")
	}
	// §25.11 step-6: the checksum is over the encrypted archive.
	// Decrypting yields a readable gzip tar.
	plaintext, err := runner.DecryptAESGCM(key, archive.Data)
	if err != nil {
		t.Fatalf("DecryptAESGCM: %v", err)
	}
	assertArchiveHasManifest(t, plaintext)
	// The ciphertext does not contain the plaintext marker.
	if bytes.Contains(archive.Data, []byte("sensitive-dump-content")) {
		t.Error("the encrypted archive leaks plaintext content")
	}
}

func TestDecryptAESGCMRejectsWrongKey(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	wrong := bytes.Repeat([]byte{2}, 32)
	a := &runner.TarGzArchiver{DataKey: key}
	archive, err := a.Pack(context.Background(), []runner.Component{{Name: "config", Bytes: []byte("x")}})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if _, err := runner.DecryptAESGCM(wrong, archive.Data); err == nil {
		t.Error("DecryptAESGCM accepted a wrong key")
	}
}

func TestDecryptAESGCMRejectsShortKey(t *testing.T) {
	if _, err := runner.DecryptAESGCM([]byte("too-short"), []byte("data")); err == nil {
		t.Error("DecryptAESGCM accepted a non-32-byte key")
	}
}

func TestHashReader(t *testing.T) {
	sum, err := runner.HashReader(bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Fatalf("HashReader: %v", err)
	}
	// SHA-256 of "abc".
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if sum != want {
		t.Errorf("HashReader = %q, want %q", sum, want)
	}
}

func TestExecDumperRequiresShards(t *testing.T) {
	d := &runner.ExecDumper{}
	if _, err := d.DumpPostgres(context.Background()); err == nil {
		t.Error("DumpPostgres accepted an empty shard list")
	}
}

func TestExecDumperConfigAndCRDFallbacks(t *testing.T) {
	// With no ConfigExport / CRDExport wired, the exports yield empty
	// components rather than failing — the correct behavior for a Job
	// that can reach only Postgres.
	d := &runner.ExecDumper{}
	cfg, err := d.ExportConfig(context.Background())
	if err != nil {
		t.Fatalf("ExportConfig: %v", err)
	}
	if cfg.Name != "config" {
		t.Errorf("config component name = %q, want config", cfg.Name)
	}
	crds, err := d.ExportCRDs(context.Background())
	if err != nil {
		t.Fatalf("ExportCRDs: %v", err)
	}
	if crds.Name != "crds" {
		t.Errorf("crd component name = %q, want crds", crds.Name)
	}
}

// assertArchiveHasManifest checks that data is a gzip-compressed tar
// carrying the §25.11 manifest.json entry.
func assertArchiveHasManifest(t *testing.T, data []byte) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Name == "manifest.json" {
			found = true
		}
	}
	if !found {
		t.Error("the archive carries no manifest.json")
	}
}
