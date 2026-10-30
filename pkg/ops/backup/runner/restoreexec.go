// SPDX-License-Identifier: MIT

package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// TarGzOpener is the production ArchiveOpener. It reverses TarGzArchiver:
// it decrypts the archive when a data key is configured (the §12.9
// AES-256-GCM {nonce || ciphertext || tag} layout), decompresses the
// gzip stream, untars the outer archive to find the postgres component,
// and untars that to recover each shard's pg_dump custom-format dump.
type TarGzOpener struct {
	// DataKey is the 32-byte AES-256 data key the archive was encrypted
	// under. It mirrors TarGzArchiver.DataKey: an empty key means the
	// archive was uploaded as plaintext (MinIO server-side encryption
	// only) and no client-side decryption is applied.
	DataKey []byte
}

var _ ArchiveOpener = (*TarGzOpener)(nil)

// ExtractPostgresDumps implements ArchiveOpener.
func (o *TarGzOpener) ExtractPostgresDumps(_ context.Context, archive []byte) ([][]byte, error) {
	plaintext := archive
	if len(o.DataKey) > 0 {
		dec, err := DecryptAESGCM(o.DataKey, archive)
		if err != nil {
			return nil, fmt.Errorf("decrypt archive: %w", err)
		}
		plaintext = dec
	}
	gz, err := gzip.NewReader(bytes.NewReader(plaintext))
	if err != nil {
		return nil, fmt.Errorf("open archive gzip: %w", err)
	}
	defer gz.Close()
	postgresTar, err := readTarEntry(gz, "postgres.tar")
	if err != nil {
		return nil, err
	}
	if postgresTar == nil {
		// A config-only archive carries no Postgres component; there is
		// nothing to pg_restore --list.
		return nil, nil
	}
	return readShardDumps(postgresTar)
}

// readTarEntry reads the first entry named name from the tar stream r
// and returns its bytes, or nil when no such entry exists.
func readTarEntry(r io.Reader, name string) ([]byte, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read archive tar: %w", err)
		}
		if hdr.Name == name {
			buf, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read archive entry %s: %w", name, err)
			}
			return buf, nil
		}
	}
}

// readShardDumps untars the postgres component and returns each
// postgres/shard-*.dump entry's bytes in tar order.
func readShardDumps(postgresTar []byte) ([][]byte, error) {
	tr := tar.NewReader(bytes.NewReader(postgresTar))
	var dumps [][]byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read postgres tar: %w", err)
		}
		if !strings.HasPrefix(hdr.Name, "postgres/shard-") {
			continue
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read shard dump %s: %w", hdr.Name, err)
		}
		dumps = append(dumps, buf)
	}
	return dumps, nil
}

// ExecDumpInspector is the production DumpInspector. It writes a shard
// dump to a temp file and runs pg_restore --list to prove the dump is
// readable without restoring any data (§25.11 Backup Verification step
// 3). The temp file lives under the Job pod's writable /tmp emptyDir.
type ExecDumpInspector struct {
	// PgRestorePath is the pg_restore executable. Empty defaults to
	// "pg_restore" resolved on PATH.
	PgRestorePath string
}

var _ DumpInspector = (*ExecDumpInspector)(nil)

// ListDump implements DumpInspector. A custom-format dump is proved
// readable with pg_restore --list; a §25.11 redacted dump is plain SQL
// (pg_restore cannot list it) and is proved readable with the
// plain-dump text check, the scratch-DB restore-test being the
// authoritative restorability proof.
func (e *ExecDumpInspector) ListDump(ctx context.Context, dump []byte) error {
	if !isCustomFormatDump(dump) {
		if !validatePlainDump(dump) {
			return errors.New("plain-format dump is empty or not readable SQL")
		}
		return nil
	}
	path, cleanup, err := writeTempDump(dump)
	if err != nil {
		return err
	}
	defer cleanup()
	bin := e.PgRestorePath
	if bin == "" {
		bin = "pg_restore"
	}
	cmd := exec.CommandContext(ctx, bin, "--list", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore --list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ExecScratchRestorer is the production ScratchRestorer. It restores
// each shard dump into the scratch Postgres named by ScratchDSN with
// pg_restore, proving the backup is actually restorable (§25.11 Test
// Restore). A successful restore is the smoke test: pg_restore exits
// non-zero on a dump it cannot apply. The scratch database is provided
// by the restore-test Job (a temporary namespace per §25.11); this type
// only drives pg_restore against the supplied DSN.
type ExecScratchRestorer struct {
	// PgRestorePath is the pg_restore executable. Empty defaults to
	// "pg_restore".
	PgRestorePath string
	// PsqlPath is the psql executable used to restore §25.11 redacted
	// (plain-format) dumps that pg_restore cannot apply. Empty defaults
	// to "psql".
	PsqlPath string
	// ScratchDSN is the scratch Postgres connection string the dumps are
	// restored into. Required.
	ScratchDSN string
}

var _ ScratchRestorer = (*ExecScratchRestorer)(nil)

// RestoreAndSmoke implements ScratchRestorer. Each shard dump is
// restored into the scratch Postgres: a custom-format dump via
// pg_restore, a §25.11 redacted (plain-format) dump via psql with
// ON_ERROR_STOP so a malformed dump fails the smoke test the same way.
func (e *ExecScratchRestorer) RestoreAndSmoke(ctx context.Context, dumps [][]byte) error {
	if e.ScratchDSN == "" {
		return errors.New("runner: scratch restore requires a scratch DSN")
	}
	for i, d := range dumps {
		path, cleanup, err := writeTempDump(d)
		if err != nil {
			return err
		}
		var cmd *exec.Cmd
		if isCustomFormatDump(d) {
			bin := e.PgRestorePath
			if bin == "" {
				bin = "pg_restore"
			}
			cmd = exec.CommandContext(ctx, bin,
				"--clean", "--if-exists", "--no-owner", "--no-privileges",
				"--dbname="+e.ScratchDSN, path)
		} else {
			bin := e.PsqlPath
			if bin == "" {
				bin = "psql"
			}
			cmd = exec.CommandContext(ctx, bin,
				"--set", "ON_ERROR_STOP=on", "--dbname="+e.ScratchDSN, "--file="+path)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		cleanup()
		if runErr != nil {
			return fmt.Errorf("restore shard %d: %w: %s", i, runErr, strings.TrimSpace(stderr.String()))
		}
	}
	return nil
}

// writeTempDump writes a shard dump to a temp file under /tmp and
// returns its path and a cleanup func that removes it.
func writeTempDump(dump []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "lenny-restore-*.dump")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp dump file: %w", err)
	}
	if _, err := f.Write(dump); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("write temp dump file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("close temp dump file: %w", err)
	}
	name := f.Name()
	return name, func() { os.Remove(name) }, nil
}
