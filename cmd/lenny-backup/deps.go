// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

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
	// configDSN is the §25.11 step-2 shard Postgres DSN the config export
	// reads tenants and quotas from, through the read-only lenny-backup
	// role. Empty (a retention/verify run, or a Job with no shard) leaves
	// the config export without a Postgres source. It is the first
	// --postgres-shard on a full or config backup.
	configDSN string
	// namespace is the release namespace the §17.6 lenny-bootstrap-values
	// ConfigMap lives in; the config export reads it there.
	namespace string
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

	// §25.11 step-2/3 config and CRD export. The exporters read only from
	// the sources the backup Job can reach: the shard Postgres via the
	// read-only lenny-backup role for tenants and quotas, and the K8s API
	// for the runtime/pool CRDs and the bootstrap ConfigMap (§25.11 line
	// 3982/3984; no gateway egress). Off-cluster (local dev, a run with no
	// shard) the exporters stay nil and the run produces explicit empty
	// config/CRD components, the correct behavior for a Postgres-only Job.
	configExport, crdExport, exportClose := buildExporters(ctx, in)
	prevClose := closeFn
	closeFn = func() {
		exportClose()
		prevClose()
	}

	return &deps{
		uploader:     uploader,
		reporter:     &pgReporter{pool: pool},
		audit:        buildBackupAuditSink(pool),
		opsEmitter:   opsEmitter,
		dataKey:      dataKey,
		minioClient:  client,
		bucket:       in.minioBucket,
		configExport: configExport,
		crdExport:    crdExport,
		closeFn:      closeFn,
	}, nil
}

// buildExporters wires the §25.11 config and CRD exporters from the
// in-cluster Kubernetes config and the shard Postgres DSN. It fails soft:
// when the binary is not running inside a cluster, or the K8s clients or
// the config-DSN pool cannot be built, the corresponding exporter is left
// nil so the run falls back to an explicit empty component rather than
// aborting. The returned closeExport releases the config-DSN pool.
func buildExporters(ctx context.Context, in depsInput) (
	configExport, crdExport func(ctx context.Context) ([]byte, error), closeExport func(),
) {
	closeExport = func() {}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		// Not running in a cluster (local dev or a unit test). The exports
		// fall back to empty components. spec: §25.11 line 3984.
		return nil, nil, closeExport
	}

	if crdCS, err := apiextensionsclientset.NewForConfig(restCfg); err == nil {
		crdExport = runner.NewCRDExporter(crdCS)
	} else {
		slog.WarnContext(ctx, "lenny-backup: CRD export disabled: build apiextensions client", "error", err)
	}

	coreCS, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		slog.WarnContext(ctx, "lenny-backup: config export disabled: build kubernetes client", "error", err)
		return nil, crdExport, closeExport
	}
	// The config export reads runtimes and pools from the lenny.dev Runtime
	// and SandboxWarmPool custom resources through the K8s API (§25.11 C4:
	// the K8s API for the runtime and pool CRDs), using the lenny-backup-sa
	// get/list-on-CRDs grant. A failed CRD-reader build disables the config
	// export rather than silently omitting runtimes/pools.
	crdReader, err := runner.NewCRDReader(restCfg)
	if err != nil {
		slog.WarnContext(ctx, "lenny-backup: config export disabled: build lenny.dev CRD reader", "error", err)
		return nil, crdExport, closeExport
	}
	if in.configDSN == "" {
		// Only a retention or verify run legitimately lacks a shard, and so
		// a Postgres config source; those modes export no config. A full,
		// postgres, or config run always carries --postgres-shard (the chart
		// renders it for config mode too, so a config-only backup does not
		// silently produce an empty config archive — SEC-BACKUP-1). The CRD
		// export still runs.
		return nil, crdExport, closeExport
	}
	cfgPool, err := pgxpool.New(ctx, in.configDSN)
	if err != nil {
		slog.WarnContext(ctx, "lenny-backup: config export disabled: connect to the shard Postgres", "error", err)
		return nil, crdExport, closeExport
	}
	configExport = runner.NewConfigExporter(cfgPool, crdReader, coreCS, in.namespace)
	closeExport = cfgPool.Close
	return configExport, crdExport, closeExport
}
