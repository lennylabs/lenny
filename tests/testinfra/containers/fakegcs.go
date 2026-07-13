// SPDX-License-Identifier: MIT

package containers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/api/option"
)

// defaultFakeGCSImage is the fake-gcs-server image used by tests. It
// speaks the GCS JSON+XML API so the production cloud.google.com/go/storage
// client exercises the same code path it uses against real GCS.
const defaultFakeGCSImage = "fsouza/fake-gcs-server:1.49"

// FakeGCSOptions configures StartFakeGCS. The zero value is sensible.
type FakeGCSOptions struct {
	// Image is the container image. Defaults to defaultFakeGCSImage.
	Image string
	// Bucket is a bucket created before the handle is returned.
	// Defaults to "lenny-artifacts".
	Bucket string
	// StartupTimeout caps how long we wait for the container to become
	// ready. Defaults to 60 seconds.
	StartupTimeout time.Duration
}

// FakeGCS is the handle returned by StartFakeGCS.
type FakeGCS struct {
	// Endpoint is the GCS JSON API base a storage client dials, of the
	// form "http://host:port/storage/v1/".
	Endpoint string
	// PublicHost is the "host:port" the emulator advertises.
	PublicHost string
	// Bucket is the pre-created bucket.
	Bucket string
	// ClientOptions produce a storage client bound to the emulator with
	// no authentication, ready to pass to gcs.New via Config.ClientOptions.
	ClientOptions []option.ClientOption

	container testcontainers.Container
}

// StartFakeGCS provisions a fake-gcs-server container, creates a bucket,
// and returns a FakeGCS handle. The cleanup hook stops the container at
// test end.
func StartFakeGCS(t testing.TB, opts FakeGCSOptions) *FakeGCS {
	t.Helper()
	if opts.Image == "" {
		opts.Image = defaultFakeGCSImage
	}
	if opts.Bucket == "" {
		opts.Bucket = "lenny-artifacts"
	}
	if opts.StartupTimeout == 0 {
		opts.StartupTimeout = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.StartupTimeout)
	defer cancel()

	// -scheme http keeps the emulator on plain HTTP; -backend memory
	// avoids disk I/O. The public host is patched after start via the
	// /_internal/config endpoint once the mapped port is known, so that
	// resumable-upload redirects point back at the mapped address.
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        opts.Image,
			Cmd:          []string{"-scheme", "http", "-backend", "memory", "-port", "4443"},
			ExposedPorts: []string{"4443/tcp"},
			WaitingFor: wait.ForHTTP("/storage/v1/b").
				WithPort("4443/tcp").
				WithStartupTimeout(opts.StartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("StartFakeGCS: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = container.Terminate(stopCtx)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("StartFakeGCS: host: %v", err)
	}
	port, err := container.MappedPort(ctx, "4443/tcp")
	if err != nil {
		t.Fatalf("StartFakeGCS: port: %v", err)
	}
	publicHost := host + ":" + port.Port()
	base := "http://" + publicHost

	// Patch the advertised external URL so upload redirects resolve to
	// the mapped port rather than the in-container 4443.
	patchFakeGCSPublicHost(ctx, t, base, publicHost)

	clientOpts := []option.ClientOption{
		option.WithEndpoint(base + "/storage/v1/"),
		option.WithoutAuthentication(),
	}

	// Create the bucket up front; the production Store never creates
	// buckets.
	sc, err := storage.NewClient(ctx, clientOpts...)
	if err != nil {
		t.Fatalf("StartFakeGCS: storage client: %v", err)
	}
	defer sc.Close()
	if err := sc.Bucket(opts.Bucket).Create(ctx, "lenny-test", nil); err != nil {
		t.Fatalf("StartFakeGCS: create bucket %q: %v", opts.Bucket, err)
	}

	return &FakeGCS{
		Endpoint:      base + "/storage/v1/",
		PublicHost:    publicHost,
		Bucket:        opts.Bucket,
		ClientOptions: clientOpts,
		container:     container,
	}
}

// patchFakeGCSPublicHost updates the emulator's advertised external URL
// so that resumable-upload location headers point at the mapped port.
func patchFakeGCSPublicHost(ctx context.Context, t testing.TB, base, publicHost string) {
	t.Helper()
	body := fmt.Sprintf(`{"externalUrl":%q,"publicHost":%q}`, base, publicHost)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/_internal/config", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("StartFakeGCS: build config request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("StartFakeGCS: patch public host: %v", err)
	}
	_ = resp.Body.Close()
}
