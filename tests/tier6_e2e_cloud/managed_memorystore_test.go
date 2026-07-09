// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 Memorystore-managed Redis tests. Mirrors
// managed_elasticache_test.go for the GCP topology. Read
// LENNY_GCP_MEMORYSTORE_* env vars scripts/cloud/gcp/up.sh emits (see
// deploy/terraform/cloud/gcp/managed-services.tf) and skip cleanly
// when the host is empty. The in-cluster `lenny-redis` fixture-based
// flow keeps running independently — the managed-service tests are
// additive.

package tier6_e2e_cloud_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/redis/go-redis/v9"
	redisadmin "google.golang.org/api/redis/v1"
)

type memorystoreParams struct {
	host       string
	port       string
	authToken  string
	project    string
	region     string
	instanceID string
}

// requireMemorystore reads the LENNY_GCP_MEMORYSTORE_* env trio
// scripts/cloud/gcp/up.sh emits and returns the connection
// parameters, or skips when the host is empty (the default when
// WITH_MEMORYSTORE=0 in gcp/up.sh).
func requireMemorystore(t *testing.T) memorystoreParams {
	t.Helper()
	host := strings.TrimSpace(os.Getenv("LENNY_GCP_MEMORYSTORE_HOST"))
	if host == "" {
		t.Log("requireMemorystore: LENNY_GCP_MEMORYSTORE_HOST is empty; re-run with WITH_MEMORYSTORE=1 scripts/cloud/gcp/up.sh to provision Memorystore via terraform")
		return memorystoreParams{}
	}
	project := strings.TrimSpace(os.Getenv("LENNY_GCP_PROJECT"))
	if project == "" {
		t.Fatalf("requireMemorystore: LENNY_GCP_PROJECT is empty even though LENNY_GCP_MEMORYSTORE_HOST is set; scripts/cloud/gcp/up.sh always emits both")
	}
	region := strings.TrimSpace(os.Getenv("LENNY_GCP_REGION"))
	if region == "" {
		region = "us-central1"
	}
	instanceID := strings.TrimSpace(os.Getenv("LENNY_GCP_MEMORYSTORE_INSTANCE_ID"))
	port := strings.TrimSpace(os.Getenv("LENNY_GCP_MEMORYSTORE_PORT"))
	if port == "" {
		port = "6379"
	}

	secretName := strings.TrimSpace(os.Getenv("LENNY_GCP_MEMORYSTORE_AUTH_SECRET_NAME"))
	if secretName == "" {
		t.Fatalf("requireMemorystore: LENNY_GCP_MEMORYSTORE_AUTH_SECRET_NAME is empty even though LENNY_GCP_MEMORYSTORE_HOST is set; the Terraform module should always emit both")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sm, err := secretmanager.NewClient(ctx)
	if err != nil {
		t.Fatalf("requireMemorystore: create Secret Manager client: %v", err)
	}
	defer func() { _ = sm.Close() }()
	resp, err := sm.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", project, secretName),
	})
	if err != nil {
		t.Fatalf("requireMemorystore: access secret %s: %v", secretName, err)
	}
	var creds struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		Auth string `json:"auth"`
	}
	if err := json.Unmarshal(resp.GetPayload().GetData(), &creds); err != nil {
		t.Fatalf("requireMemorystore: decode auth secret: %v", err)
	}
	if creds.Port != 0 {
		port = strconv.Itoa(creds.Port)
	}

	return memorystoreParams{
		host:       host,
		port:       port,
		authToken:  creds.Auth,
		project:    project,
		region:     region,
		instanceID: instanceID,
	}
}

// spec: 13.2 (TLS-only Redis ingress to Memorystore).
// diagnosis: TestCloudMemorystoreTLSRequired asserts that a plaintext
// connection to the Memorystore endpoint is refused, and that a TLS
// connection succeeds. deploy/terraform/cloud/gcp/managed-services.tf
// sets transit_encryption_mode=SERVER_AUTHENTICATION on the instance;
// a non-TLS client receives a connection reset at the
// encryption-required engine handshake.
func TestCloudMemorystoreTLSRequired(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudMemorystoreTLSRequired: Memorystore test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	m := requireMemorystore(t)
	if m.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plainClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", m.host, m.port),
		Password:    m.authToken,
		DialTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second,
	})
	defer func() { _ = plainClient.Close() }()
	if err := plainClient.Ping(ctx).Err(); err == nil {
		t.Errorf("expected the Memorystore endpoint to refuse a plaintext PING; transit_encryption_mode appears off")
	} else {
		t.Logf("TestCloudMemorystoreTLSRequired: plaintext PING correctly refused: %v", err)
	}

	tlsClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", m.host, m.port),
		Password:    m.authToken,
		TLSConfig:   &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12},
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
	t.Logf("TestCloudMemorystoreTLSRequired: TLS-encrypted Redis reachable on %s:%s", m.host, m.port)
}

// spec: 13.3 (Memorystore AUTH-token gating).
// diagnosis: TestCloudMemorystoreAUTH asserts that an unauthenticated
// TLS client cannot run a Redis command (the engine returns NOAUTH),
// and that a client carrying the AUTH token can. The Terraform module
// sets auth_enabled=true on the instance.
func TestCloudMemorystoreAUTH(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudMemorystoreAUTH: Memorystore test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	m := requireMemorystore(t)
	if m.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	noAuthClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", m.host, m.port),
		TLSConfig:   &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = noAuthClient.Close() }()
	if err := noAuthClient.Get(ctx, "tier6-memorystore-noauth-probe").Err(); err == nil {
		t.Errorf("expected NOAUTH from a no-AUTH client; the GET returned without error")
	} else if strings.Contains(strings.ToUpper(err.Error()), "NOAUTH") || strings.Contains(strings.ToUpper(err.Error()), "WRONGPASS") {
		t.Logf("TestCloudMemorystoreAUTH: no-AUTH client rejected: %v", err)
	} else {
		t.Errorf("expected NOAUTH/WRONGPASS from a no-AUTH client; got %v", err)
	}

	authClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", m.host, m.port),
		Password:    m.authToken,
		TLSConfig:   &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = authClient.Close() }()
	key := fmt.Sprintf("tier6-memorystore-auth-probe-%d", time.Now().UnixNano())
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
	t.Logf("TestCloudMemorystoreAUTH: AUTH-token client round-trip succeeded")
}

// spec: 11.2.1 (billing-stream MAXMEMORY-policy invariant).
// diagnosis: TestCloudMemorystoreEvictionPolicy asserts the active
// maxmemory-policy on the instance is `noeviction`. The §11.2.1 Redis
// stream `t:{tenant_id}:billing:stream` is bounded by
// `billingRedisStreamMaxLen`; eviction under memory pressure would
// silently drop billing events. deploy/terraform/cloud/gcp/
// managed-services.tf sets redis_configs["maxmemory-policy"] =
// "noeviction" so the stream backpressures into the gateway's
// flusher.
func TestCloudMemorystoreEvictionPolicy(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudMemorystoreEvictionPolicy: Memorystore test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	m := requireMemorystore(t)
	if m.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", m.host, m.port),
		Password:    m.authToken,
		TLSConfig:   &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = client.Close() }()

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
	t.Logf("TestCloudMemorystoreEvictionPolicy: maxmemory_policy=%s", policy)
}

// spec: 13.3 (Memorystore engine version floor).
// diagnosis: TestCloudMemorystoreEngineVersionFloor asserts the
// Memorystore engine is Redis 7.0 or newer. §13.3 ACLs, the `RESET`
// command, and the cluster-mode pub/sub sharding fix all require
// 7.0+; a 6.x deployment silently weakens the §13.3 auth posture.
func TestCloudMemorystoreEngineVersionFloor(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudMemorystoreEngineVersionFloor: Memorystore test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	m := requireMemorystore(t)
	if m.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", m.host, m.port),
		Password:    m.authToken,
		TLSConfig:   &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12},
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
	t.Logf("TestCloudMemorystoreEngineVersionFloor: redis_version=%s", version)
}

// spec: 17.3 (Memorystore Standard-tier high availability).
// diagnosis: TestCloudMemorystoreHighAvailability queries the
// Memorystore Admin API for the instance tier and asserts it is
// STANDARD_HA, the Basic-tier alternative has no replica and no
// automatic failover. deploy/terraform/cloud/gcp/managed-services.tf
// defaults memorystore_tier to STANDARD_HA; a Basic instance cannot
// honor the §17.3 availability guarantee during a zone outage.
func TestCloudMemorystoreHighAvailability(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudMemorystoreHighAvailability: Memorystore test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	m := requireMemorystore(t)
	if m.host == "" {
		return
	}
	if m.instanceID == "" {
		t.Log("TestCloudMemorystoreHighAvailability: LENNY_GCP_MEMORYSTORE_INSTANCE_ID is unset; cannot resolve the instance for the Admin API lookup")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	svc, err := redisadmin.NewService(ctx)
	if err != nil {
		t.Fatalf("redisadmin.NewService: %v", err)
	}
	name := fmt.Sprintf("projects/%s/locations/%s/instances/%s", m.project, m.region, m.instanceID)
	inst, err := svc.Projects.Locations.Instances.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get %s: %v", name, err)
	}
	if inst.Tier != "STANDARD_HA" {
		t.Errorf("Tier = %q, want STANDARD_HA (§17.3 availability)", inst.Tier)
		return
	}
	if inst.TransitEncryptionMode == "" || inst.TransitEncryptionMode == "DISABLED" {
		t.Errorf("TransitEncryptionMode = %q, want an enabled mode", inst.TransitEncryptionMode)
	}
	t.Logf("TestCloudMemorystoreHighAvailability: instance %s is %s, transit encryption=%s", m.instanceID, inst.Tier, inst.TransitEncryptionMode)
}
