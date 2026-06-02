// SPDX-License-Identifier: MIT

package infra

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
	miniocreds "github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
)

// RealProbers returns the production Probers that dial Postgres, Redis,
// and MinIO over the network. The binaries (the standalone CLI and the
// gateway) wire these; tests inject fakes through the Probers seam.
//
// spec: §15.1 line 890 (active outbound connectivity probes).
func RealProbers() Probers {
	return Probers{
		Postgres: RealPostgresProber{},
		Redis:    RealRedisProber{},
		MinIO:    RealMinIOProber{},
	}
}

// RealPostgresProber dials Postgres with pgx, pings, and reads the
// applied golang-migrate schema version (best-effort).
type RealPostgresProber struct{}

// ProbePostgres connects to dsn, verifies the connection with a ping,
// and reads the current schema-migration version. A missing
// schema_migrations table (a fresh, un-migrated database) is not an
// error: connectivity is the hard guarantee and the version read is
// advisory.
func (RealPostgresProber) ProbePostgres(ctx context.Context, dsn string) (string, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.Ping(ctx); err != nil {
		return "", err
	}
	// golang-migrate stores a single row {version bigint, dirty bool}.
	// A missing table or no row leaves version empty (fresh database).
	var version string
	var dirty bool
	row := conn.QueryRow(ctx, "SELECT version::text, dirty FROM schema_migrations LIMIT 1")
	if err := row.Scan(&version, &dirty); err != nil {
		return "", nil
	}
	if dirty {
		return version + " (dirty)", nil
	}
	return version, nil
}

// RealRedisProber dials Redis with go-redis and runs PING.
type RealRedisProber struct{}

// ProbeRedis parses the redis:// or rediss:// URL, opens a client, and
// runs PING.
func (RealRedisProber) ProbeRedis(ctx context.Context, dsn string) error {
	opt, err := redis.ParseURL(dsn)
	if err != nil {
		return err
	}
	client := redis.NewClient(opt)
	defer func() { _ = client.Close() }()
	return client.Ping(ctx).Err()
}

// RealMinIOProber dials MinIO with minio-go.
type RealMinIOProber struct{}

// ProbeMinIO opens a MinIO client and verifies connectivity and
// credentials. A non-empty bucket is checked for existence; an empty
// bucket falls back to ListBuckets so the credentials are still
// exercised against the server.
func (RealMinIOProber) ProbeMinIO(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) error {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  miniocreds.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return err
	}
	if bucket != "" {
		exists, err := client.BucketExists(ctx, bucket)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("bucket %q does not exist", bucket)
		}
		return nil
	}
	if _, err := client.ListBuckets(ctx); err != nil {
		return err
	}
	return nil
}
