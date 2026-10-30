//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §25.11 backup MinIO write/read surfaces against
// a real MinIO container. It exercises the production MinIOUploader
// (step-7 encrypted-archive upload with server-side encryption) and the
// production MinIODownloader (the verify / restore-test read path) as a
// byte-exact round trip, asserts that server-side encryption is applied
// on every upload (SSE-S3 by default, SSE-KMS when a KMS key is
// configured), asserts that the uploader refuses an empty archive, and
// asserts that the Pruner's DeleteBackupObject removes the object so a
// subsequent download fails.
package stores_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// backupKMSKeyName is the single SSE-KMS key the KMS-enabled MinIO
// container is provisioned with. The uploader points its KMSKeyID at
// this name for the SSE-KMS branch; the same configured KMS backend also
// serves the default SSE-S3 key MinIO requires for a plain SSE-S3 upload.
const backupKMSKeyName = "lenny-backup-key"

// randArchive builds a runner.Archive whose Data is n random bytes, so a
// downloaded copy that compares byte-equal proves a real round trip
// rather than an accidental match on constant content.
func randArchive(t *testing.T, n int) runner.Archive {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("generate random archive bytes: %v", err)
	}
	return runner.Archive{Data: buf, Encrypted: true}
}

// spec: 25.11 (Backup and Restore API) — "Uploads to MinIO at
// backups/{type}/{id}/{timestamp}.tar.gz.enc with server-side encryption
// (SSE-S3 or SSE-KMS per backups.encryption.minioServerSide)"; "Access
// to MinIO via a dedicated lenny-backup access key with s3:PutObject,
// s3:GetObject (for verification), and s3:DeleteObject (for retention)".
//
// diagnosis: the §25.11 backup MinIO write/read surfaces are broken. The
// production MinIOUploader must write the encrypted archive to the
// backup bucket with server-side encryption applied (SSE-S3 by default,
// SSE-KMS when a KMS key is configured), the production MinIODownloader
// must read those exact bytes back for the verify and restore-test
// paths, and the Pruner must delete a pruned object during retention. A
// failure means a backup archive is stored unencrypted at rest, cannot
// be read back for verification/restore, or cannot be pruned.
func TestBackupMinIORoundTrip(t *testing.T) {
	m := containers.StartMinIO(t, containers.MinIOOptions{
		Bucket:     "lenny-backups",
		KMSKeyName: backupKMSKeyName,
	})

	downloader, err := runner.NewMinIODownloader(m.Client, m.Bucket)
	if err != nil {
		t.Fatalf("build MinIODownloader: %v", err)
	}

	// spec: 25.11 step 7 — "server-side encryption (SSE-S3 or SSE-KMS ...)".
	// The default uploader (no KMS key) applies SSE-S3, and the archive
	// bytes survive a MinIOUploader.Upload -> MinIODownloader.Download
	// round trip byte-for-byte.
	t.Run("sse-s3 upload downloads byte-exact", func(t *testing.T) {
		up, err := runner.NewMinIOUploader(runner.MinIOUploaderConfig{
			Client: m.Client,
			Bucket: m.Bucket,
		})
		if err != nil {
			t.Fatalf("build MinIOUploader (SSE-S3): %v", err)
		}
		arc := randArchive(t, 4096)
		const objectPath = "backups/full/bkp-sse-s3/20260716T000000Z.tar.gz.enc"

		got, err := up.Upload(t.Context(), objectPath, arc)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if got != objectPath {
			t.Errorf("Upload returned object path %q, want %q", got, objectPath)
		}

		info, err := m.Client.StatObject(t.Context(), m.Bucket, objectPath, minio.StatObjectOptions{})
		if err != nil {
			t.Fatalf("StatObject: %v", err)
		}
		if sse := info.Metadata.Get("X-Amz-Server-Side-Encryption"); sse != "AES256" {
			t.Errorf("§25.11 step-7 violation: default upload SSE header = %q, want AES256 (SSE-S3)", sse)
		}

		back, err := downloader.Download(t.Context(), objectPath)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		if !bytes.Equal(back, arc.Data) {
			t.Errorf("§25.11 round trip violation: downloaded %d bytes, differ from the %d uploaded", len(back), len(arc.Data))
		}
	})

	// spec: 25.11 MinIO Bucket Policy — "If SSE-KMS is configured, requires
	// s3:x-amz-server-side-encryption-aws-kms-key-id to match
	// backups.encryption.kmsKeyId on PutObject". With a KMS key set, the
	// uploader applies SSE-KMS under that key and the archive still
	// round-trips byte-for-byte.
	t.Run("sse-kms upload records the key and downloads byte-exact", func(t *testing.T) {
		up, err := runner.NewMinIOUploader(runner.MinIOUploaderConfig{
			Client:   m.Client,
			Bucket:   m.Bucket,
			KMSKeyID: backupKMSKeyName,
		})
		if err != nil {
			t.Fatalf("build MinIOUploader (SSE-KMS): %v", err)
		}
		arc := randArchive(t, 4096)
		const objectPath = "backups/full/bkp-sse-kms/20260716T000000Z.tar.gz.enc"

		if _, err := up.Upload(t.Context(), objectPath, arc); err != nil {
			t.Fatalf("Upload: %v", err)
		}

		info, err := m.Client.StatObject(t.Context(), m.Bucket, objectPath, minio.StatObjectOptions{})
		if err != nil {
			t.Fatalf("StatObject: %v", err)
		}
		if sse := info.Metadata.Get("X-Amz-Server-Side-Encryption"); sse != "aws:kms" {
			t.Errorf("§25.11 violation: SSE-KMS upload SSE header = %q, want aws:kms", sse)
		}
		if keyID := info.Metadata.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id"); !bytes.Contains([]byte(keyID), []byte(backupKMSKeyName)) {
			t.Errorf("§25.11 violation: SSE-KMS upload key id header = %q, want it to name %q", keyID, backupKMSKeyName)
		}

		back, err := downloader.Download(t.Context(), objectPath)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		if !bytes.Equal(back, arc.Data) {
			t.Errorf("§25.11 round trip violation: SSE-KMS downloaded bytes differ from uploaded")
		}
	})

	// The uploader must never write an empty object: an empty archive is a
	// failed dump, and a completed backup pointing at a zero-byte object is
	// worse than a reported failure.
	t.Run("empty archive is refused", func(t *testing.T) {
		up, err := runner.NewMinIOUploader(runner.MinIOUploaderConfig{
			Client: m.Client,
			Bucket: m.Bucket,
		})
		if err != nil {
			t.Fatalf("build MinIOUploader: %v", err)
		}
		if _, err := up.Upload(t.Context(), "backups/full/bkp-empty/x.tar.gz.enc", runner.Archive{}); err == nil {
			t.Error("§25.11 violation: Upload accepted an empty archive; a zero-byte backup object must be refused")
		}
	})

	// spec: 25.11 — "s3:DeleteObject (for retention)". The Pruner removes a
	// pruned backup's object; a following download of that path fails.
	t.Run("prune deletes the object", func(t *testing.T) {
		up, err := runner.NewMinIOUploader(runner.MinIOUploaderConfig{
			Client: m.Client,
			Bucket: m.Bucket,
		})
		if err != nil {
			t.Fatalf("build MinIOUploader: %v", err)
		}
		const objectPath = "backups/full/bkp-prune/20260716T000000Z.tar.gz.enc"
		if _, err := up.Upload(t.Context(), objectPath, randArchive(t, 512)); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if err := up.DeleteBackupObject(t.Context(), objectPath); err != nil {
			t.Fatalf("DeleteBackupObject: %v", err)
		}
		if _, err := downloader.Download(t.Context(), objectPath); err == nil {
			t.Error("§25.11 violation: object still downloadable after DeleteBackupObject; retention prune did not remove it")
		}
		// A delete of an already-absent object is idempotent.
		if err := up.DeleteBackupObject(t.Context(), objectPath); err != nil {
			t.Errorf("§25.11 violation: repeated DeleteBackupObject not idempotent: %v", err)
		}
	})
}
