// SPDX-License-Identifier: MIT

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/encrypt"
)

// MinIOUploader is the production §25.11 Uploader and Pruner. It writes
// the §25.11 step-7 encrypted archive to the backup bucket and removes
// pruned backup objects during retention enforcement. It applies
// §12.9 server-side encryption on every PutObject: SSE-KMS when a KMS
// key is configured, otherwise SSE-S3, so a backup archive is encrypted
// at rest even when client-side encryption was skipped.
type MinIOUploader struct {
	// client is the MinIO client for the backup bucket.
	client *minio.Client
	// bucket is the §25.11 backup bucket the lenny-backup access key is
	// scoped to.
	bucket string
	// sse is the §12.9 server-side encryption applied to every upload.
	sse encrypt.ServerSide
}

var (
	_ Uploader = (*MinIOUploader)(nil)
	_ Pruner   = (*MinIOUploader)(nil)
)

// MinIOUploaderConfig configures a MinIOUploader.
type MinIOUploaderConfig struct {
	// Client is the MinIO client for the backup bucket. Required.
	Client *minio.Client
	// Bucket is the §25.11 backup bucket. Required.
	Bucket string
	// KMSKeyID, when set, selects §12.9 SSE-KMS server-side encryption
	// under that key. When empty the uploader uses SSE-S3, MinIO's
	// internal key management — the §12.9 minimum for a backup target.
	KMSKeyID string
}

// NewMinIOUploader builds a §25.11 MinIOUploader. It returns an error
// when a required dependency is missing or the SSE-KMS configuration is
// invalid. Server-side encryption is never disabled: a backup carries
// tenant data and §12.9 requires encryption at rest.
func NewMinIOUploader(cfg MinIOUploaderConfig) (*MinIOUploader, error) {
	if cfg.Client == nil {
		return nil, errors.New("runner: MinIOUploader requires a client")
	}
	if cfg.Bucket == "" {
		return nil, errors.New("runner: MinIOUploader requires a bucket")
	}
	var sse encrypt.ServerSide
	if cfg.KMSKeyID != "" {
		s, err := encrypt.NewSSEKMS(cfg.KMSKeyID, nil)
		if err != nil {
			return nil, fmt.Errorf("runner: build SSE-KMS for key %q: %w", cfg.KMSKeyID, err)
		}
		sse = s
	} else {
		// §12.9: SSE-S3 is the minimum. The uploader never writes a backup
		// object without server-side encryption.
		sse = encrypt.NewSSE()
	}
	return &MinIOUploader{client: cfg.Client, bucket: cfg.Bucket, sse: sse}, nil
}

// Upload implements Uploader. It writes the encrypted archive to the
// backup bucket at objectPath with §12.9 server-side encryption and
// returns the object path written.
func (u *MinIOUploader) Upload(ctx context.Context, objectPath string, archive Archive) (string, error) {
	if len(archive.Data) == 0 {
		return "", errors.New("runner: refusing to upload an empty archive")
	}
	_, err := u.client.PutObject(ctx, u.bucket, objectPath,
		bytes.NewReader(archive.Data), int64(len(archive.Data)), minio.PutObjectOptions{
			ContentType:          "application/octet-stream",
			ServerSideEncryption: u.sse,
		})
	if err != nil {
		return "", fmt.Errorf("runner: put backup object %s: %w", objectPath, err)
	}
	return objectPath, nil
}

// DeleteBackupObject implements Pruner. It removes a pruned backup's
// object from the backup bucket. A delete of an already-absent object
// is a no-op, so retention enforcement is idempotent.
func (u *MinIOUploader) DeleteBackupObject(ctx context.Context, objectPath string) error {
	if objectPath == "" {
		return nil
	}
	if err := u.client.RemoveObject(ctx, u.bucket, objectPath, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("runner: delete backup object %s: %w", objectPath, err)
	}
	return nil
}
