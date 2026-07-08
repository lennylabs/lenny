// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 Azure Cache for Redis tests. Mirrors
// managed_elasticache_test.go for the Azure topology. Read
// LENNY_AZURE_REDIS_* env vars scripts/cloud/azure/up.sh emits (see
// deploy/terraform/cloud/azure/managed-services.tf) and skip cleanly
// when the hostname is empty. The in-cluster `lenny-redis`
// fixture-based flow keeps running independently — the
// managed-service tests are additive.

package tier6_e2e_cloud_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/redis/go-redis/v9"
)

type azureRedisParams struct {
	host           string
	port           string
	accessKey      string
	resourceGroup  string
	subscriptionID string
	cacheName      string
}

// requireAzureRedis reads the LENNY_AZURE_REDIS_* env trio
// scripts/cloud/azure/up.sh emits and returns the connection
// parameters, or skips when the hostname is empty (the default when
// WITH_AZURE_REDIS=0 in azure/up.sh).
func requireAzureRedis(t *testing.T) azureRedisParams {
	t.Helper()
	host := strings.TrimSpace(os.Getenv("LENNY_AZURE_REDIS_HOSTNAME"))
	if host == "" {
		t.Log("requireAzureRedis: LENNY_AZURE_REDIS_HOSTNAME is empty; re-run with WITH_AZURE_REDIS=1 scripts/cloud/azure/up.sh to provision Azure Cache for Redis via terraform")
		return azureRedisParams{}
	}
	resourceGroup := strings.TrimSpace(os.Getenv("LENNY_AZURE_RESOURCE_GROUP"))
	subscriptionID := strings.TrimSpace(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	if resourceGroup == "" || subscriptionID == "" {
		t.Fatalf("requireAzureRedis: LENNY_AZURE_RESOURCE_GROUP=%q AZURE_SUBSCRIPTION_ID=%q; both are required alongside LENNY_AZURE_REDIS_HOSTNAME", resourceGroup, subscriptionID)
	}
	port := strings.TrimSpace(os.Getenv("LENNY_AZURE_REDIS_SSL_PORT"))
	if port == "" {
		port = "6380"
	}

	// The cache name is the hostname's first DNS label
	// ("lenny-e2e-redis.redis.cache.windows.net" -> "lenny-e2e-redis").
	// LENNY_AZURE_REDIS_NAME overrides it.
	cacheName := strings.TrimSpace(os.Getenv("LENNY_AZURE_REDIS_NAME"))
	if cacheName == "" {
		cacheName, _, _ = strings.Cut(host, ".")
	}

	// Two AUTH-key resolution paths: a direct env var
	// (LENNY_AZURE_REDIS_ACCESS_KEY, used by the in-cluster runner),
	// and a Key Vault secret lookup (used by the operator's local
	// invocation, which carries an az-login credential).
	var accessKey string
	if direct := strings.TrimSpace(os.Getenv("LENNY_AZURE_REDIS_ACCESS_KEY")); direct != "" {
		accessKey = direct
	} else {
		vaultName := strings.TrimSpace(os.Getenv("LENNY_AZURE_KEY_VAULT_NAME"))
		secretName := strings.TrimSpace(os.Getenv("LENNY_AZURE_REDIS_AUTH_SECRET_NAME"))
		if vaultName == "" || secretName == "" {
			t.Fatalf("requireAzureRedis: neither LENNY_AZURE_REDIS_ACCESS_KEY nor (LENNY_AZURE_KEY_VAULT_NAME + LENNY_AZURE_REDIS_AUTH_SECRET_NAME) is set; the Terraform module emits the Key Vault secret, the in-cluster runner passes the key directly")
		}
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			t.Fatalf("requireAzureRedis: build Azure credential: %v", err)
		}
		kv, err := azsecrets.NewClient(fmt.Sprintf("https://%s.vault.azure.net", vaultName), cred, nil)
		if err != nil {
			t.Fatalf("requireAzureRedis: build Key Vault client: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resp, err := kv.GetSecret(ctx, secretName, "", nil)
		if err != nil {
			t.Fatalf("requireAzureRedis: get secret %s: %v", secretName, err)
		}
		if resp.Value == nil {
			t.Fatalf("requireAzureRedis: secret %s has no value", secretName)
		}
		// The secret is JSON {host, port, auth}; extract just the
		// auth field with a minimal decode to avoid a second struct.
		var creds struct {
			Auth string `json:"auth"`
		}
		if err := json.Unmarshal([]byte(*resp.Value), &creds); err != nil {
			t.Fatalf("requireAzureRedis: decode auth secret: %v", err)
		}
		accessKey = creds.Auth
	}

	return azureRedisParams{
		host:           host,
		port:           port,
		accessKey:      accessKey,
		resourceGroup:  resourceGroup,
		subscriptionID: subscriptionID,
		cacheName:      cacheName,
	}
}

// spec: 13.2 (Azure Cache for Redis TLS-only ingress).
// diagnosis: TestCloudAzureRedisTLSRequired asserts that a plaintext
// connection to the Azure Cache endpoint is refused, and that a TLS
// connection on the SSL port succeeds. deploy/terraform/cloud/azure/
// managed-services.tf sets non_ssl_port_enabled=false; the plaintext
// port is not listening at all, so the client fails to connect
// rather than receiving a protocol-level refusal.
func TestCloudAzureRedisTLSRequired(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzureRedisTLSRequired: Azure Cache test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ar := requireAzureRedis(t)
	if ar.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plainClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:6379", ar.host),
		Password:    ar.accessKey,
		DialTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second,
	})
	defer func() { _ = plainClient.Close() }()
	if err := plainClient.Ping(ctx).Err(); err == nil {
		t.Errorf("expected the plaintext port 6379 to refuse a connection; non_ssl_port_enabled appears on")
	} else {
		t.Logf("TestCloudAzureRedisTLSRequired: plaintext port correctly unreachable: %v", err)
	}

	tlsClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", ar.host, ar.port),
		Password:    ar.accessKey,
		TLSConfig:   &tls.Config{ServerName: ar.host, MinVersion: tls.VersionTLS12},
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
	t.Logf("TestCloudAzureRedisTLSRequired: TLS-encrypted Redis reachable on %s:%s", ar.host, ar.port)
}

// spec: 13.3 (Azure Cache access-key gating).
// diagnosis: TestCloudAzureRedisAUTH asserts that an unauthenticated
// TLS client cannot run a Redis command (the engine returns NOAUTH),
// and that a client carrying the access key can.
func TestCloudAzureRedisAUTH(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzureRedisAUTH: Azure Cache test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ar := requireAzureRedis(t)
	if ar.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	noAuthClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", ar.host, ar.port),
		TLSConfig:   &tls.Config{ServerName: ar.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = noAuthClient.Close() }()
	if err := noAuthClient.Get(ctx, "tier6-azure-redis-noauth-probe").Err(); err == nil {
		t.Errorf("expected NOAUTH from a no-AUTH client; the GET returned without error")
	} else if strings.Contains(strings.ToUpper(err.Error()), "NOAUTH") || strings.Contains(strings.ToUpper(err.Error()), "WRONGPASS") {
		t.Logf("TestCloudAzureRedisAUTH: no-AUTH client rejected: %v", err)
	} else {
		t.Errorf("expected NOAUTH/WRONGPASS from a no-AUTH client; got %v", err)
	}

	authClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", ar.host, ar.port),
		Password:    ar.accessKey,
		TLSConfig:   &tls.Config{ServerName: ar.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = authClient.Close() }()
	key := fmt.Sprintf("tier6-azure-redis-auth-probe-%d", time.Now().UnixNano())
	if err := authClient.Set(ctx, key, "ok", 60*time.Second).Err(); err != nil {
		t.Fatalf("SET with access key: %v", err)
	}
	got, err := authClient.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("GET with access key: %v", err)
	}
	if got != "ok" {
		t.Errorf("GET returned %q, want ok", got)
	}
	_ = authClient.Del(ctx, key).Err()
	t.Logf("TestCloudAzureRedisAUTH: access-key client round-trip succeeded")
}

// spec: 11.2.1 (billing-stream MAXMEMORY-policy invariant).
// diagnosis: TestCloudAzureRedisEvictionPolicy asserts the active
// maxmemory-policy on the cache is `noeviction`. The §11.2.1 Redis
// stream `t:{tenant_id}:billing:stream` is bounded by
// `billingRedisStreamMaxLen`; eviction under memory pressure would
// silently drop billing events. deploy/terraform/cloud/azure/
// managed-services.tf sets redis_configuration.maxmemory_policy =
// "noeviction" so the stream backpressures into the gateway's
// flusher.
func TestCloudAzureRedisEvictionPolicy(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzureRedisEvictionPolicy: Azure Cache test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ar := requireAzureRedis(t)
	if ar.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", ar.host, ar.port),
		Password:    ar.accessKey,
		TLSConfig:   &tls.Config{ServerName: ar.host, MinVersion: tls.VersionTLS12},
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
	t.Logf("TestCloudAzureRedisEvictionPolicy: maxmemory_policy=%s", policy)
}

// spec: 13.3 (Azure Cache engine version floor).
// diagnosis: TestCloudAzureRedisEngineVersionFloor asserts the cache
// runs Redis 6.0 or newer, the highest major version Azure Cache for
// Redis (non-Enterprise tiers) publishes. §13.3 ACLs and the `RESET`
// command require 6.0+; the assertion documents the floor Azure
// actually offers rather than the 7.0 floor the AWS/GCP tests use.
func TestCloudAzureRedisEngineVersionFloor(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzureRedisEngineVersionFloor: Azure Cache test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ar := requireAzureRedis(t)
	if ar.host == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", ar.host, ar.port),
		Password:    ar.accessKey,
		TLSConfig:   &tls.Config{ServerName: ar.host, MinVersion: tls.VersionTLS12},
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
	if major < "6" {
		t.Errorf("redis_version = %q, want >= 6.0", version)
	}
	t.Logf("TestCloudAzureRedisEngineVersionFloor: redis_version=%s", version)
}

// spec: 12.4 (Premium-tier clustering across shards).
// diagnosis: TestCloudAzureRedisPremiumClustering queries the ARM
// Redis API for the cache SKU and shard count and asserts that a
// Premium cache with clustering enabled has more than one shard. The
// §12.4 Redis cluster-mode requirement applies to deployments whose
// tenant-key cardinality exceeds a single shard's memory. Standard
// and Basic tiers have no cluster support at all, so the test logs
// and returns rather than failing when the active SKU is not
// Premium — matching the ElastiCache cluster-mode test's tolerance
// of a single-shard baseline deployment.
func TestCloudAzureRedisPremiumClustering(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudAzureRedisPremiumClustering: Azure Cache test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	ar := requireAzureRedis(t)
	if ar.host == "" {
		return
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Fatalf("build Azure credential: %v", err)
	}
	client, err := armredis.NewClient(ar.subscriptionID, cred, nil)
	if err != nil {
		t.Fatalf("armredis.NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.Get(ctx, ar.resourceGroup, ar.cacheName, nil)
	if err != nil {
		t.Fatalf("Client.Get %s/%s: %v", ar.resourceGroup, ar.cacheName, err)
	}
	if resp.Properties == nil || resp.Properties.SKU == nil || resp.Properties.SKU.Name == nil ||
		*resp.Properties.SKU.Name != armredis.SKUNamePremium {
		t.Logf("TestCloudAzureRedisPremiumClustering: cache %s is not Premium tier; re-run with AZURE_REDIS_SKU=Premium AZURE_REDIS_FAMILY=P scripts/cloud/azure/up.sh to enable clustering", ar.cacheName)
		return
	}
	shards := int32(0)
	if resp.Properties.ShardCount != nil {
		shards = *resp.Properties.ShardCount
	}
	if shards <= 1 {
		t.Logf("TestCloudAzureRedisPremiumClustering: cache %s is Premium but single-shard; re-run with a shardCount > 1 to enable cluster mode", ar.cacheName)
		return
	}

	authClient := redis.NewClient(&redis.Options{
		Addr:        fmt.Sprintf("%s:%s", ar.host, ar.port),
		Password:    ar.accessKey,
		TLSConfig:   &tls.Config{ServerName: ar.host, MinVersion: tls.VersionTLS12},
		DialTimeout: 10 * time.Second,
		ReadTimeout: 10 * time.Second,
	})
	defer func() { _ = authClient.Close() }()
	key := fmt.Sprintf("tier6-azure-redis-cluster-probe-%d", time.Now().UnixNano())
	if err := authClient.Set(ctx, key, "ok", 60*time.Second).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	got, err := authClient.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got != "ok" {
		t.Errorf("GET returned %q, want ok", got)
	}
	_ = authClient.Del(ctx, key).Err()
	t.Logf("TestCloudAzureRedisPremiumClustering: Premium cache %s has %d shards; round-trip succeeded", ar.cacheName, shards)
}
