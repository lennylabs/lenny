// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	miniocreds "github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// defaultMinIOImage is the MinIO image used by tests. MinIO publishes
// date-stamped RELEASE tags rather than semver; tests may override it
// via MinIOOptions.Image to pin a specific release.
const defaultMinIOImage = "minio/minio:latest"

// minioTestCredentials are the root credentials the test container
// runs with. They are test-only and never used outside the container.
const (
	minioTestAccessKey = "minioadmin"
	minioTestSecretKey = "minioadmin"
)

// MinIOOptions configures StartMinIO. The zero value is sensible.
type MinIOOptions struct {
	// Image is the container image. Defaults to defaultMinIOImage.
	Image string
	// Bucket is a bucket created before the handle is returned.
	// Defaults to "lenny-artifacts".
	Bucket string
	// StartupTimeout caps how long we wait for the container to become
	// ready. Defaults to 60 seconds.
	StartupTimeout time.Duration
	// Env supplies extra environment variables merged over the container's
	// default MINIO_ROOT_USER / MINIO_ROOT_PASSWORD. A caller that needs
	// server-side encryption (SSE-S3 / SSE-KMS) enables MinIO's built-in
	// KMS here, for example
	// Env: {"MINIO_KMS_SECRET_KEY": "key-name:<base64-32-byte-key>"}.
	Env map[string]string
}

// MinIO is the handle returned by StartMinIO.
type MinIO struct {
	// Endpoint is a host:port a MinIO client can dial.
	Endpoint string
	// AccessKey and SecretKey are the container's root credentials.
	AccessKey string
	SecretKey string
	// Bucket is the pre-created bucket.
	Bucket string
	// Client is a ready-to-use MinIO client.
	Client *minio.Client

	container testcontainers.Container
}

// StartMinIO provisions a MinIO container, creates a bucket, and
// returns a MinIO handle. The cleanup hook stops the container at test
// end.
func StartMinIO(t testing.TB, opts MinIOOptions) *MinIO {
	t.Helper()
	if opts.Image == "" {
		opts.Image = defaultMinIOImage
	}
	if opts.Bucket == "" {
		opts.Bucket = "lenny-artifacts"
	}
	if opts.StartupTimeout == 0 {
		opts.StartupTimeout = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.StartupTimeout)
	defer cancel()

	env := map[string]string{
		"MINIO_ROOT_USER":     minioTestAccessKey,
		"MINIO_ROOT_PASSWORD": minioTestSecretKey,
	}
	for k, v := range opts.Env {
		env[k] = v
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        opts.Image,
			Cmd:          []string{"server", "/data"},
			ExposedPorts: []string{"9000/tcp"},
			Env:          env,
			WaitingFor: wait.ForHTTP("/minio/health/live").
				WithPort("9000/tcp").
				WithStartupTimeout(opts.StartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("StartMinIO: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = container.Terminate(stopCtx)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("StartMinIO: host: %v", err)
	}
	port, err := container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("StartMinIO: port: %v", err)
	}
	endpoint := host + ":" + port.Port()

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  miniocreds.NewStaticV4(minioTestAccessKey, minioTestSecretKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("StartMinIO: client: %v", err)
	}
	if err := client.MakeBucket(ctx, opts.Bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("StartMinIO: create bucket %q: %v", opts.Bucket, err)
	}

	return &MinIO{
		Endpoint:  endpoint,
		AccessKey: minioTestAccessKey,
		SecretKey: minioTestSecretKey,
		Bucket:    opts.Bucket,
		Client:    client,
		container: container,
	}
}

// Stop terminates the MinIO container mid-test so a caller can inject a
// genuine MinIO outage: subsequent client operations fail to connect
// rather than returning success. It is the testcontainers analogue of the
// tier-8 "scale the MinIO Deployment to zero" injection, used by the
// §25.11 backup-degradation upload-failure test. The t.Cleanup registered
// by StartMinIO tolerates a double Terminate.
func (m *MinIO) Stop(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.container.Terminate(ctx); err != nil {
		t.Fatalf("MinIO.Stop: terminate: %v", err)
	}
}
