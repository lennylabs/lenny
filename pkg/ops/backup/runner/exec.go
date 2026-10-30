// SPDX-License-Identifier: MIT

package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ExecDumper is the production §25.11 Dumper. It runs pg_dump against
// the configured Postgres shards (the §25.11 step-1 pg_dump
// --format=custom invocation) and reads the platform configuration and
// CRD manifests through the supplied command. The Kubernetes API and
// the configuration export are themselves CLI invocations so the Job
// pod's read-only root filesystem and minimal RBAC are honored.
type ExecDumper struct {
	// PgDumpPath is the pg_dump executable. Empty defaults to "pg_dump"
	// resolved on PATH.
	PgDumpPath string
	// ShardDSNs are the §25.11 per-shard Postgres connection strings.
	// One pg_dump runs per shard. A per-region deployment supplies the
	// region's shards only — there is no global aggregated dump.
	ShardDSNs []string
	// ExcludeTables are the §25.11 content-policy tables excluded from
	// the dump via --exclude-table-data. The lenny-backup binary fills
	// it from backups.contentPolicy plus the defaultExcludedTables.
	ExcludeTables []string
	// RedactColumns are the §25.11 contentPolicy.redactColumns entries.
	// When non-empty the shard dumps switch from --format=custom to
	// --format=plain so the matched column data can be rewritten to
	// "[REDACTED]"; each entry is a bare column name (matched in every
	// table) or a "table.column" qualifier (matched in that table only).
	RedactColumns []string
	// ConfigExport runs the §25.11 step-2 platform-configuration export
	// and returns the JSON. The lenny-backup binary supplies it; a
	// deployment without the gateway admin API leaves it nil and the
	// config component is empty.
	ConfigExport func(ctx context.Context) ([]byte, error)
	// CRDExport runs the §25.11 step-3 CRD-manifest export and returns
	// the manifests. The lenny-backup binary supplies it; a deployment
	// without a Kubernetes connection leaves it nil and the CRD
	// component is empty.
	CRDExport func(ctx context.Context) ([]byte, error)
}

var _ Dumper = (*ExecDumper)(nil)

// DumpPostgres implements Dumper. It runs one pg_dump per shard with the
// §25.11 content-policy --exclude-table-data flags and concatenates the
// per-shard dumps into one component. The dump uses --format=custom
// unless contentPolicy.redactColumns is configured, in which case it
// uses --format=plain and pipes the output through the column-redaction
// filter so the matched columns are rewritten to "[REDACTED]" (a binary
// custom-format archive cannot be text-filtered). A shard whose pg_dump
// fails aborts the run.
func (d *ExecDumper) DumpPostgres(ctx context.Context) (Component, error) {
	if len(d.ShardDSNs) == 0 {
		return Component{}, errors.New("runner: no Postgres shards configured")
	}
	pgDump := d.PgDumpPath
	if pgDump == "" {
		pgDump = "pg_dump"
	}
	targets := parseRedactTargets(d.RedactColumns)
	format, ext := "custom", "dump"
	if len(targets) > 0 {
		format, ext = "plain", "sql"
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i, dsn := range d.ShardDSNs {
		args := []string{"--format=" + format, "--no-owner", "--no-privileges"}
		for _, t := range d.ExcludeTables {
			args = append(args, "--exclude-table-data="+t)
		}
		args = append(args, dsn)
		cmd := exec.CommandContext(ctx, pgDump, args...)
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return Component{}, fmt.Errorf("pg_dump shard %d: %w: %s", i, err,
				strings.TrimSpace(stderr.String()))
		}
		shard := out.Bytes()
		if len(targets) > 0 {
			var redacted bytes.Buffer
			if err := redactCopyStream(bytes.NewReader(shard), &redacted, targets); err != nil {
				return Component{}, fmt.Errorf("redact shard %d: %w", i, err)
			}
			shard = redacted.Bytes()
		}
		hdr := &tar.Header{
			Name: fmt.Sprintf("postgres/shard-%d.%s", i, ext),
			Mode: 0o600,
			Size: int64(len(shard)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return Component{}, fmt.Errorf("tar header shard %d: %w", i, err)
		}
		if _, err := tw.Write(shard); err != nil {
			return Component{}, fmt.Errorf("tar write shard %d: %w", i, err)
		}
	}
	if err := tw.Close(); err != nil {
		return Component{}, fmt.Errorf("close postgres tar: %w", err)
	}
	return Component{Name: "postgres", Bytes: buf.Bytes()}, nil
}

// ExportConfig implements Dumper. It runs the configured configuration
// export; a nil ConfigExport yields an empty component.
func (d *ExecDumper) ExportConfig(ctx context.Context) (Component, error) {
	if d.ConfigExport == nil {
		return Component{Name: "config", Bytes: []byte("{}")}, nil
	}
	data, err := d.ConfigExport(ctx)
	if err != nil {
		return Component{}, fmt.Errorf("export platform configuration: %w", err)
	}
	return Component{Name: "config", Bytes: data}, nil
}

// ExportCRDs implements Dumper. It runs the configured CRD export; a
// nil CRDExport yields an empty component.
func (d *ExecDumper) ExportCRDs(ctx context.Context) (Component, error) {
	if d.CRDExport == nil {
		return Component{Name: "crds", Bytes: []byte("")}, nil
	}
	data, err := d.CRDExport(ctx)
	if err != nil {
		return Component{}, fmt.Errorf("export crds: %w", err)
	}
	return Component{Name: "crds", Bytes: data}, nil
}

// manifestEntry records, in the §25.11 archive internal manifest, what
// each component covered so restore tooling can detect what was
// included versus excluded.
type manifestEntry struct {
	Component string `json:"component"`
	SizeBytes int    `json:"sizeBytes"`
}

// archiveManifest is the §25.11 archive internal manifest: the
// component list plus the content-policy record.
type archiveManifest struct {
	Components     []manifestEntry `json:"components"`
	ExcludedTables []string        `json:"excludedTables"`
	// RedactedColumns records the §25.11 contentPolicy.redactColumns
	// applied to the postgres component so restore tooling can detect
	// that the shard dumps are plain-format and carry "[REDACTED]" in
	// place of the matched columns.
	RedactedColumns []string `json:"redactedColumns,omitempty"`
	Encrypted       bool     `json:"encrypted"`
}

// TarGzArchiver is the production §25.11 Archiver. It packages the
// backup components into a gzip-compressed tar archive, writes the
// §25.11 internal manifest into the archive, and applies §12.9
// encryption-at-rest: AES-256-GCM client-side encryption when a data
// key is supplied, otherwise the archive is left for MinIO server-side
// encryption on upload.
type TarGzArchiver struct {
	// DataKey is the 32-byte AES-256 data key for client-side
	// encryption. §25.11 wraps it with the configured KMS key; the
	// lenny-backup binary unwraps it and supplies the plaintext key
	// here. An empty key disables client-side encryption — the upload
	// then relies on MinIO server-side encryption (SSE-S3 / SSE-KMS),
	// the §12.9 fallback for a deployment without KMS.
	DataKey []byte
	// ExcludedTables is recorded in the §25.11 archive internal
	// manifest.
	ExcludedTables []string
	// RedactedColumns is the §25.11 contentPolicy.redactColumns set
	// applied to the postgres component; it is recorded in the archive
	// internal manifest so restore tooling knows the dumps are plain
	// and column-redacted.
	RedactedColumns []string
}

var _ Archiver = (*TarGzArchiver)(nil)

// Pack implements Archiver. It tars and gzips the components with the
// §25.11 internal manifest, then encrypts the archive client-side with
// AES-256-GCM when a data key is configured.
func (a *TarGzArchiver) Pack(_ context.Context, components []Component) (Archive, error) {
	plaintext, err := a.packTarGz(components)
	if err != nil {
		return Archive{}, err
	}
	if len(a.DataKey) == 0 {
		// §12.9 fallback: no client-side key. The archive is uploaded as
		// plaintext and MinIO server-side encryption protects it at rest.
		return Archive{Data: plaintext, Checksum: sha256Hex(plaintext), Encrypted: false}, nil
	}
	ciphertext, err := encryptAESGCM(a.DataKey, plaintext)
	if err != nil {
		return Archive{}, fmt.Errorf("encrypt archive: %w", err)
	}
	// §25.11 step-6: the checksum is the SHA-256 of the *encrypted*
	// archive.
	return Archive{Data: ciphertext, Checksum: sha256Hex(ciphertext), Encrypted: true}, nil
}

// packTarGz writes the components and the §25.11 internal manifest into
// a gzip-compressed tar archive.
func (a *TarGzArchiver) packTarGz(components []Component) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	manifest := archiveManifest{
		ExcludedTables:  a.ExcludedTables,
		RedactedColumns: a.RedactedColumns,
		Encrypted:       len(a.DataKey) > 0,
	}
	for _, c := range components {
		manifest.Components = append(manifest.Components, manifestEntry{
			Component: c.Name,
			SizeBytes: len(c.Bytes),
		})
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal archive manifest: %w", err)
	}
	if err := writeTarFile(tw, "manifest.json", manifestJSON); err != nil {
		return nil, err
	}
	for _, c := range components {
		if err := writeTarFile(tw, c.Name+".tar", c.Bytes); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close archive tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("close archive gzip: %w", err)
	}
	return buf.Bytes(), nil
}

// writeTarFile writes one file entry into a tar archive.
func writeTarFile(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("tar write %s: %w", name, err)
	}
	return nil
}

// encryptAESGCM encrypts plaintext with AES-256-GCM under key,
// returning nonce||ciphertext||tag. §12.9 mandates AES-256-GCM for
// client-side encryption of a backup archive.
func encryptAESGCM(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES-256 requires a 32-byte key, got %d bytes", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	// Seal appends the ciphertext and tag to nonce, so the result is the
	// §12.9 {nonce || ciphertext || tag} layout.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptAESGCM reverses encryptAESGCM: it decrypts a {nonce ||
// ciphertext || tag} blob with AES-256-GCM under key. The restore path
// and the verification path use it.
func DecryptAESGCM(key, blob []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES-256 requires a 32-byte key, got %d bytes", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("ciphertext shorter than the GCM nonce")
	}
	nonce, ciphertext := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}
	return plaintext, nil
}
