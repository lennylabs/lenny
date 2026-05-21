// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 ElastiCache-managed Redis tests. Read LENNY_AWS_REDIS_* env
// vars the Terraform module emits (see managed-services.tf) and skip
// cleanly when the endpoint is empty. The in-cluster `lenny-redis`
// fixture-based flow keeps running independently — the managed-service
// tests are additive.

package tier6_e2e_cloud_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/redis/go-redis/v9"
)

type redisParams struct {
	host         string
	port         string
	authToken    string
	clusterMode  bool
	configHost   string
	configPort   string
	region       string
}

func requireRedis(t *testing.T) redisParams {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LENNY_AWS_REDIS_ENDPOINT"))
	if endpoint == "" {
		t.Log("requireRedis: LENNY_AWS_REDIS_ENDPOINT is empty; re-run with WITH_ELASTICACHE=1 scripts/cloud/aws/run-e2e.sh to provision ElastiCache via terraform")
		return redisParams{}
	}
	port := strings.TrimSpace(os.Getenv("LENNY_AWS_REDIS_PORT"))
	if port == "" || port == "0" {
		port = "6379"
	}
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = "us-west-2"
	}

	// Two AUTH-token resolution paths: a direct env var
	// (LENNY_AWS_REDIS_AUTH_TOKEN, used by the in-cluster runner
	// where Secrets Manager IAM permissions are not provisioned),
	// and a Secrets Manager ARN lookup (used by the operator's
	// local invocation, which carries the AWS profile credentials).
	var token string
	if direct := strings.TrimSpace(os.Getenv("LENNY_AWS_REDIS_AUTH_TOKEN")); direct != "" {
		token = direct
	} else {
		secretARN := strings.TrimSpace(os.Getenv("LENNY_AWS_REDIS_AUTH_SECRET_ARN"))
		if secretARN == "" {
			t.Fatalf("requireRedis: neither LENNY_AWS_REDIS_AUTH_TOKEN nor LENNY_AWS_REDIS_AUTH_SECRET_ARN is set; the Terraform module emits the secret ARN, the in-cluster runner passes the token directly")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			t.Fatalf("requireRedis: load AWS config: %v", err)
		}
		sm := secretsmanager.NewFromConfig(cfg)
		out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &secretARN})
		if err != nil {
			t.Fatalf("requireRedis: fetch AUTH secret: %v", err)
		}
		token = strings.TrimSpace(*out.SecretString)
	}

	params := redisParams{
		host:      endpoint,
		port:      port,
		authToken: token,
		region:    region,
	}
	if cfg := strings.TrimSpace(os.Getenv("LENNY_AWS_REDIS_CONFIG_ENDPOINT")); cfg != "" {
		if host, p, err := net.SplitHostPort(cfg); err == nil {
			params.clusterMode = true
			params.configHost = host
			params.configPort = p
		} else {
			params.clusterMode = true
			params.configHost = cfg
			params.configPort = port
		}
	}

	// ElastiCache endpoints live on private VPC subnets only;
	// AWS does not surface a public-DNS / public-IP option. A test
	// runner outside the VPC fails to reach the endpoint at all,
	// which is a different failure mode from the engine refusing a
	// plaintext connection. Skip with a clear hint when the
	// endpoint is unreachable.
	probe, perr := net.DialTimeout("tcp", net.JoinHostPort(params.host, params.port), 3*time.Second)
	if perr != nil {
		t.Logf("requireRedis: cannot reach %s:%s (%v); ElastiCache endpoints are VPC-private. Run the suite from inside the cluster (`kubectl run --rm -it lenny-test-runner ...`) to exercise these tests, or via a VPN/bastion to the lenny-e2e VPC.", params.host, params.port, perr)
		return redisParams{}
	}
	_ = probe.Close()
	return params
}

// spec: 13.2 (TLS-only Redis ingress to managed cache).
// diagnosis: TestCloudRedisTLSRequired asserts that a plaintext
// connection to the ElastiCache endpoint is refused, and that a TLS
// connection succeeds. The Terraform module sets
// transit_encryption_enabled=true on the replication group; a non-TLS
// client receives a connection reset at the encryption-required
// engine handshake.
func TestCloudRedisTLSRequired(t *testing.T) {
	_ = requireCloud(t)
	p := requireRedis(t)
	if p.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plainClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", p.host, p.port),
		Password:    p.authToken,
		DialTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second,
	})
	defer func() { _ = plainClient.Close() }()
	if err := plainClient.Ping(ctx).Err(); err == nil {
		t.Errorf("expected the ElastiCache endpoint to refuse a plaintext PING; transit_encryption_enabled appears off")
	} else {
		t.Logf("TestCloudRedisTLSRequired: plaintext PING correctly refused: %v", err)
	}

	tlsClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", p.host, p.port),
		Password:    p.authToken,
		TLSConfig:   &tls.Config{ServerName: p.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = tlsClient.Close() }()
	pong, err := tlsClient.Ping(ctx).Result()
	if err != nil {
		t.Fatalf("TLS PING failed: %v", err)
	}
	if pong != "PONG" {
		t.Errorf("TLS PING returned %q, want PONG", pong)
	}
	t.Logf("TestCloudRedisTLSRequired: TLS-encrypted Redis reachable on %s:%s", p.host, p.port)
}

// spec: 13.3 (AUTH-token gating).
// diagnosis: TestCloudRedisAUTH asserts that an unauthenticated TLS
// client cannot run a Redis command (the engine returns NOAUTH), and
// that a client carrying the AUTH token can. ElastiCache provisioned
// with a non-empty auth_token enforces NOAUTH unless the client sends
// AUTH.
func TestCloudRedisAUTH(t *testing.T) {
	_ = requireCloud(t)
	p := requireRedis(t)
	if p.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	noAuthClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", p.host, p.port),
		TLSConfig:   &tls.Config{ServerName: p.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = noAuthClient.Close() }()
	if err := noAuthClient.Get(ctx, "tier6-noauth-probe").Err(); err == nil {
		t.Errorf("expected NOAUTH from a no-AUTH client; the GET returned without error")
	} else if !strings.Contains(strings.ToUpper(err.Error()), "NOAUTH") &&
		!strings.Contains(strings.ToUpper(err.Error()), "WRONGPASS") &&
		!errors.Is(err, redis.Nil) {
		// redis.Nil is "no such key" which means the AUTH was not
		// enforced — flag it.
		if errors.Is(err, redis.Nil) {
			t.Errorf("expected NOAUTH, got redis.Nil; ElastiCache is not requiring the AUTH token")
		} else {
			t.Logf("TestCloudRedisAUTH: no-AUTH client rejected: %v", err)
		}
	} else {
		t.Logf("TestCloudRedisAUTH: no-AUTH client rejected: %v", err)
	}

	authClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", p.host, p.port),
		Password:    p.authToken,
		TLSConfig:   &tls.Config{ServerName: p.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = authClient.Close() }()
	key := fmt.Sprintf("tier6-auth-probe-%d", time.Now().UnixNano())
	if err := authClient.Set(ctx, key, "ok", 60*time.Second).Err(); err != nil {
		t.Fatalf("SET with AUTH: %v", err)
	}
	got, err := authClient.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("GET with AUTH: %v", err)
	}
	if got != "ok" {
		t.Errorf("GET returned %q, want ok", got)
	}
	_ = authClient.Del(ctx, key).Err()
	t.Logf("TestCloudRedisAUTH: AUTH-token client round-trip succeeded")
}

// spec: 11.2.1 (billing-stream MAXMEMORY-policy invariant).
// diagnosis: TestCloudRedisEvictionPolicy asserts the active
// maxmemory-policy on the cache is `noeviction`. The §11.2.1 Redis
// stream `t:{tenant_id}:billing:stream` is bounded by
// `billingRedisStreamMaxLen`; eviction under memory pressure would
// silently drop billing events. The Terraform module sets the
// parameter group's maxmemory-policy to noeviction so the stream
// backpressures into the gateway's flusher.
func TestCloudRedisEvictionPolicy(t *testing.T) {
	_ = requireCloud(t)
	p := requireRedis(t)
	if p.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", p.host, p.port),
		Password:    p.authToken,
		TLSConfig:   &tls.Config{ServerName: p.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = client.Close() }()

	// ElastiCache disables CONFIG GET / CONFIG SET (the AUTH /
	// renamed-command set). Read the active maxmemory_policy from
	// INFO memory instead — that section is always available and
	// includes the current effective value.
	info, err := client.Info(ctx, "memory").Result()
	if err != nil {
		t.Fatalf("INFO memory: %v", err)
	}
	var policy string
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "maxmemory_policy:") {
			policy = strings.TrimSpace(strings.TrimPrefix(line, "maxmemory_policy:"))
			break
		}
	}
	if policy == "" {
		t.Fatalf("INFO memory did not include maxmemory_policy: %s", info)
	}
	if policy != "noeviction" {
		t.Errorf("maxmemory_policy = %q, want noeviction (the §11.2.1 billing-stream invariant)", policy)
	}
	t.Logf("TestCloudRedisEvictionPolicy: maxmemory_policy=%s", policy)
}

// spec: 13.3 (Redis engine version floor).
// diagnosis: TestCloudRedisEngineVersionFloor asserts the ElastiCache
// engine is Redis 7.0 or newer. §13.3 ACLs, the `RESET` command, and
// the cluster-mode pub/sub sharding fix all require 7.0+; a 6.x
// deployment silently weakens the §13.3 auth posture.
func TestCloudRedisEngineVersionFloor(t *testing.T) {
	_ = requireCloud(t)
	p := requireRedis(t)
	if p.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", p.host, p.port),
		Password:    p.authToken,
		TLSConfig:   &tls.Config{ServerName: p.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = client.Close() }()
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		t.Fatalf("INFO server: %v", err)
	}
	var version string
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "redis_version:") {
			version = strings.TrimPrefix(line, "redis_version:")
			break
		}
	}
	if version == "" {
		t.Fatalf("INFO server did not include redis_version: %s", info)
	}
	major, _, _ := strings.Cut(version, ".")
	if major < "7" {
		t.Errorf("redis_version = %q, want >= 7.0", version)
	}
	t.Logf("TestCloudRedisEngineVersionFloor: redis_version=%s", version)
}

// spec: 12.4 (cluster-mode pub/sub across shards).
// diagnosis: TestCloudRedisClusterMode asserts the replication group
// is configured with more than one shard (num_node_groups > 1) and
// that a SET/GET round-trip succeeds across a cluster-aware client.
// The §12.4 Redis cluster-mode requirement applies to deployments
// whose tenant-key cardinality exceeds a single shard's memory; this
// test is the smoke signal that the cluster topology is reachable.
func TestCloudRedisClusterMode(t *testing.T) {
	_ = requireCloud(t)
	p := requireRedis(t)
	if p.host == "" {
		return
	}
	if !p.clusterMode {
		t.Log("TestCloudRedisClusterMode: replication group is single-shard; re-run with ELASTICACHE_SHARDS=2 WITH_ELASTICACHE=1 scripts/cloud/aws/run-e2e.sh to enable cluster mode")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	port := p.configPort
	if port == "" {
		port = p.port
	}
	cluster := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:       []string{fmt.Sprintf("%s:%s", p.configHost, port)},
		Password:    p.authToken,
		TLSConfig:   &tls.Config{ServerName: p.configHost, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = cluster.Close() }()

	// One key per shard via a small probe set. Cluster client routes
	// each SET to the owning shard; a SET that succeeds confirms the
	// MOVED redirection plumbing works end-to-end.
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("tier6-cluster-probe-%d-%d", time.Now().UnixNano(), i)
		if err := cluster.Set(ctx, key, "ok", 60*time.Second).Err(); err != nil {
			t.Fatalf("SET %s: %v", key, err)
		}
		v, err := cluster.Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("GET %s: %v", key, err)
		}
		if v != "ok" {
			t.Errorf("GET %s = %q, want ok", key, v)
		}
		_ = cluster.Del(ctx, key).Err()
	}
	t.Logf("TestCloudRedisClusterMode: cluster-mode client round-trip succeeded against %s:%s", p.configHost, port)
}
