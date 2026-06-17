// SPDX-License-Identifier: MIT

package containers

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// defaultRedisImage is the Redis image used by tests. Pinned by major.
const defaultRedisImage = "redis:7-alpine"

// RedisOptions configures StartRedis. The zero value is sensible.
type RedisOptions struct {
	// Image is the container image. Defaults to defaultRedisImage.
	Image string
	// StartupTimeout caps how long we wait for the container to accept
	// connections. Defaults to 30 seconds.
	StartupTimeout time.Duration
}

// Redis is the handle returned by StartRedis.
type Redis struct {
	// Addr is a host:port the go-redis client can dial.
	Addr string
	// Client is a ready-to-use go-redis client. Closed on cleanup.
	Client *redis.Client
	// container is the underlying testcontainers handle.
	container testcontainers.Container
}

// StartRedis provisions a Redis container and returns a Redis handle.
// The cleanup hook stops the container at test end.
func StartRedis(t testing.TB, opts RedisOptions) *Redis {
	t.Helper()
	if opts.Image == "" {
		opts.Image = defaultRedisImage
	}
	if opts.StartupTimeout == 0 {
		opts.StartupTimeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.StartupTimeout)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        opts.Image,
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor: wait.ForLog("Ready to accept connections").
				WithStartupTimeout(opts.StartupTimeout),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("StartRedis: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = container.Terminate(stopCtx)
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("StartRedis: host: %v", err)
	}
	port, err := container.MappedPort(ctx, "6379/tcp")
	if err != nil {
		t.Fatalf("StartRedis: port: %v", err)
	}
	addr := host + ":" + port.Port()

	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("StartRedis: ping: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return &Redis{Addr: addr, Client: client, container: container}
}

// Stop terminates the Redis container mid-test so a caller can inject a
// genuine Redis outage: subsequent client operations fail to connect rather
// than returning a miss. It is the testcontainers analogue of the tier-8
// "scale the Redis Deployment to zero" injection, used by the §12.4
// slot-counter Postgres-fallback failure/recovery tests. The t.Cleanup
// registered by StartRedis tolerates a double Terminate.
func (r *Redis) Stop(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.container.Terminate(ctx); err != nil {
		t.Fatalf("Redis.Stop: terminate: %v", err)
	}
}
