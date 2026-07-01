// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
	"github.com/lennylabs/lenny/pkg/redisconn"
)

// depsInput is the resolved flag set the lenny-backup run needs to
// build its dependencies.
type depsInput struct {
	minioEndpoint  string
	minioBucket    string
	minioAccessKey string
	minioSecretKey string
	minioTLS       bool
	kmsKeyID       string
	dataKeyFile    string
	reportDSN      string
	redisURL       string
	redisPassword  string
}

// deps holds the §25.11 backup-run dependencies: the MinIO uploader,
// the Postgres-backed reporter, and the unwrapped data key. close
// releases the Postgres pool. The MinIO client and bucket are retained
// so the verify and restore-test read paths can build a Downloader
// against the same backup bucket.
type deps struct {
	uploader     *runner.MinIOUploader
	reporter     *pgReporter
	audit        backup.AuditSink
	opsEmitter   events.EventEmitter
	dataKey      []byte
	minioClient  *minio.Client
	bucket       string
	configExport func(ctx context.Context) ([]byte, error)
	crdExport    func(ctx context.Context) ([]byte, error)
	closeFn      func()
}

// close releases the run's dependencies.
func (d *deps) close() {
	if d.closeFn != nil {
		d.closeFn()
	}
}

// resolveDeps builds the §25.11 backup-run dependencies from the
// resolved flags. It returns a usage error when a required dependency
// cannot be constructed.
func resolveDeps(ctx context.Context, in depsInput) (*deps, error) {
	client, err := minioClient(in.minioEndpoint, in.minioAccessKey, in.minioSecretKey, in.minioTLS)
	if err != nil {
		return nil, fmt.Errorf("build MinIO client: %w", err)
	}
	uploader, err := runner.NewMinIOUploader(runner.MinIOUploaderConfig{
		Client:   client,
		Bucket:   in.minioBucket,
		KMSKeyID: in.kmsKeyID,
	})
	if err != nil {
		return nil, fmt.Errorf("build MinIO uploader: %w", err)
	}

	// §25.11 client-side encryption: the data key is materialized by the
	// Job init container from the wrapped key. An absent --data-key-file
	// disables client-side encryption; the MinIO upload's SSE then
	// protects the archive at rest (§12.9 fallback).
	var dataKey []byte
	if in.dataKeyFile != "" {
		raw, err := os.ReadFile(in.dataKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read data key file %s: %w", in.dataKeyFile, err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("data key file %s must hold a 32-byte AES-256 key, has %d bytes",
				in.dataKeyFile, len(raw))
		}
		dataKey = raw
	}

	if in.reportDSN == "" {
		return nil, errors.New("--report-dsn is required to record the backup outcome")
	}
	pool, err := pgxpool.New(ctx, in.reportDSN)
	if err != nil {
		return nil, fmt.Errorf("connect to the lenny-ops Postgres: %w", err)
	}

	// §25.5: when --redis-url is set, the run's §16.6 backup_completed /
	// backup_failed events stream to the platform-scoped
	// ops:events:stream that lenny-ops consumes, mirroring the
	// lenny-controller pool_state_changed emitter. The Job exits after
	// one run, so the StreamEmitter's local ring buffer tee is discarded;
	// only the Redis XADD matters here. An unconfigured Redis leaves the
	// emitter nil and the run emits no operational event (the durable
	// audit row and ops_backups status update still land).
	closeFn := pool.Close
	var opsEmitter events.EventEmitter
	if in.redisURL != "" {
		redisClient, err := redisconn.NewClient(redisconn.Config{URL: in.redisURL, Password: in.redisPassword})
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("build Redis client for the §25.5 event stream: %w", err)
		}
		opsEmitter = eventbuffer.NewStreamEmitter(eventbuffer.StreamEmitterOptions{
			Client:    redisClient,
			Buffer:    eventbuffer.NewEventBuffer(0),
			Source:    "//lenny.dev/ops/backup",
			ReplicaID: "lenny-backup",
		})
		closeFn = func() {
			_ = redisClient.Close()
			pool.Close()
		}
	}

	return &deps{
		uploader:    uploader,
		reporter:    &pgReporter{pool: pool},
		audit:       buildBackupAuditSink(pool),
		opsEmitter:  opsEmitter,
		dataKey:     dataKey,
		minioClient: client,
		bucket:      in.minioBucket,
		// The config and CRD exports are wired when the gateway admin API
		// and a Kubernetes connection are available to the Job; until then
		// the run produces empty config/CRD components, which is the
		// correct behavior for a Postgres-only-reachable Job.
		configExport: nil,
		crdExport:    nil,
		closeFn:      closeFn,
	}, nil
}
