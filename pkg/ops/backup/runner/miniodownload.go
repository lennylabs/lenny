// SPDX-License-Identifier: MIT

package runner

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

// MinIODownloader is the production Downloader. It reads a backup
// archive's bytes from the backup bucket for the §25.11 verify and
// restore-test read paths.
type MinIODownloader struct {
	client *minio.Client
	bucket string
}

var _ Downloader = (*MinIODownloader)(nil)

// NewMinIODownloader builds a Downloader for the backup bucket.
func NewMinIODownloader(client *minio.Client, bucket string) (*MinIODownloader, error) {
	if client == nil {
		return nil, fmt.Errorf("runner: MinIODownloader requires a client")
	}
	if bucket == "" {
		return nil, fmt.Errorf("runner: MinIODownloader requires a bucket")
	}
	return &MinIODownloader{client: client, bucket: bucket}, nil
}

// Download implements Downloader.
func (d *MinIODownloader) Download(ctx context.Context, objectPath string) ([]byte, error) {
	obj, err := d.client.GetObject(ctx, d.bucket, objectPath, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("runner: get backup object %s: %w", objectPath, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("runner: read backup object %s: %w", objectPath, err)
	}
	return data, nil
}

// ArtifactKeySource supplies the ArtifactStore object keys the §25.11
// sampled-HEAD check probes. The lenny-backup binary backs it with a
// query over the restored scratch Postgres artifact_store rows, drawing
// up to sampleSize keys.
type ArtifactKeySource func(ctx context.Context, sampleSize int) ([]string, error)

// MinIOArtifactSampler is the production ArtifactSampler. It draws up to
// sampleSize object keys from the restored artifact_store rows and runs
// a HEAD (StatObject) against the §25.11 replication-target bucket,
// reporting how many of the sampled keys exist there.
type MinIOArtifactSampler struct {
	client *minio.Client
	bucket string
	keys   ArtifactKeySource
}

var _ ArtifactSampler = (*MinIOArtifactSampler)(nil)

// NewMinIOArtifactSampler builds an ArtifactSampler that HEADs sampled
// keys against the replication-target bucket.
func NewMinIOArtifactSampler(client *minio.Client, bucket string, keys ArtifactKeySource) (*MinIOArtifactSampler, error) {
	if client == nil || bucket == "" || keys == nil {
		return nil, fmt.Errorf("runner: MinIOArtifactSampler requires a client, bucket, and key source")
	}
	return &MinIOArtifactSampler{client: client, bucket: bucket, keys: keys}, nil
}

// SampleHeads implements ArtifactSampler.
func (s *MinIOArtifactSampler) SampleHeads(ctx context.Context, sampleSize int) (present, sampled int, err error) {
	keys, err := s.keys(ctx, sampleSize)
	if err != nil {
		return 0, 0, fmt.Errorf("runner: draw artifact sample keys: %w", err)
	}
	for _, key := range keys {
		_, statErr := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
		if statErr == nil {
			present++
		}
		// A StatObject error means the object is absent at the
		// replication target; it counts against the sampled-HEAD success
		// rate rather than aborting the run.
	}
	return present, len(keys), nil
}
