//go:build component

// SPDX-License-Identifier: MIT

// Component test for the lenny-ops revocation set's event path (§25.4
// "Authentication", rule 2): the real cmd/lenny-ops binary runs against a
// migrated Postgres and two distinct Redis instances. lenny-ops must take
// token.revoked events from the Cache/Pub-Sub concern the gateway publishes
// on (§12.4): from --redis-cache-pubsub-url when it is set, and from the base
// --redis-url client when it is not.
package opsrevocation_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const signingRef = "local-key-1"

// opsStack is a lenny-ops process and a signer sharing its key set.
type opsStack struct {
	ops    *opsprocess.Process
	signer jwt.Signer
}

// startOps starts lenny-ops against pg, with --redis-url on base and, when
// cache is non-nil, --redis-cache-pubsub-url on cache. lenny-ops runs outside
// production with the dev trust hook, which makes it authenticate, and on the
// local KMS provider over a master key file the test shares, so the test's
// signer mints tokens the lenny-ops key set verifies.
func startOps(t *testing.T, pg *containers.Postgres, base, cache *containers.Redis) opsStack {
	t.Helper()
	dir := t.TempDir()
	masterKey := filepath.Join(dir, "master.key")
	opts := providerflags.Options{
		Provider:      providerflags.ProviderLocal,
		MasterKeyFile: masterKey,
		AliasToKey:    map[string]string{kms.AliasJWTSigning: signingRef},
	}
	provider, err := providerflags.Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("resolve local provider: %v", err)
	}
	_, signer, err := jwt.LoadSigning(context.Background(), provider, opts.AliasToKey, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	devKey := filepath.Join(dir, "dev-key.json")
	raw, err := json.Marshal(map[string]any{"keyId": "dev", "secret": []byte("ops-dev-trust-secret")})
	if err != nil {
		t.Fatalf("marshal dev trust key: %v", err)
	}
	if err := os.WriteFile(devKey, raw, 0o600); err != nil {
		t.Fatalf("write dev trust key: %v", err)
	}
	args := []string{
		"--postgres-dsn=" + pg.DSN,
		"--redis-url=redis://" + base.Addr + "/0",
		"--redis-allow-insecure",
		"--kms-provider=local",
		"--kms-master-key-file=" + masterKey,
		"--kms-alias=" + kms.AliasJWTSigning + "=" + signingRef,
		"--bearer-trust-hmac-key-file=" + devKey,
	}
	if cache != nil {
		args = append(args, "--redis-cache-pubsub-url=redis://"+cache.Addr+"/0")
	}
	return opsStack{ops: opsprocess.StartWith(t, args...), signer: signer}
}

// mint signs a platform-admin user bearer for jti.
func (s opsStack) mint(t *testing.T, jti string) string {
	t.Helper()
	now := time.Now()
	tok, err := s.signer.Sign(context.Background(), jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: auth.DefaultTenantID,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(time.Hour).Unix(),
		JWTID:    jti,
		Typ:      auth.TokenUserBearer,
		Roles:    []auth.Role{auth.RolePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

// status returns the status lenny-ops answers GET /v1/admin/me with for tok.
func (s opsStack) status(t *testing.T, tok string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.ops.BaseURL()+"/v1/admin/me", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/admin/me: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// accepted reports whether the authentication chain admitted tok: a
// refusal is 401 (revoked or invalid) or 503 (no successful read yet).
func (s opsStack) accepted(t *testing.T, tok string) bool {
	code := s.status(t, tok)
	return code != http.StatusUnauthorized && code != http.StatusServiceUnavailable
}

// waitAccepted polls until lenny-ops admits tok, which happens once the
// startup rule-1 read has succeeded.
func (s opsStack) waitAccepted(t *testing.T, tok string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !s.accepted(t, tok) {
		if time.Now().After(deadline) {
			t.Fatalf("lenny-ops never admitted a key-set token; last status %d", s.status(t, tok))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// publishUntilRevoked publishes a token.revoked event for jti on client
// until lenny-ops refuses tok with 401, within a window shorter than the
// 30 s read interval. The row is never written to Postgres, so only the
// event can revoke it.
func (s opsStack) publishUntilRevoked(t *testing.T, client *redis.Client, jti, tok string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := client.Publish(context.Background(), propagator.Channel, jti).Err(); err != nil {
			t.Fatalf("publish: %v", err)
		}
		if s.status(t, tok) == http.StatusUnauthorized {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lenny-ops still admits %s 10s after publishing its revocation", jti)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func startPostgres(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
}

// spec: 25.4 (The lenny-ops Service: Authentication), 12.4 (Redis HA and Failure Modes)
// diagnosis: a failure means lenny-ops does not subscribe to token.revoked on
// the Cache/Pub-Sub Redis concern named by --redis-cache-pubsub-url, so a
// revocation the gateway publishes there reaches lenny-ops only at its next
// 30 s Postgres read. Check that startRevocationSet builds the bus from the
// per-concern client rather than the base client.
func TestOpsRevocationSetFollowsCachePubSubRedis(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)
	pg := startPostgres(t)
	base := containers.StartRedis(t, containers.RedisOptions{})
	cache := containers.StartRedis(t, containers.RedisOptions{})
	stack := startOps(t, pg, base, cache)

	tok := stack.mint(t, "jti-cache-concern")
	stack.waitAccepted(t, tok)

	// An event on the base instance is not where the gateway publishes when
	// the concern is split, so lenny-ops must not take it from there.
	if err := base.Client.Publish(context.Background(), propagator.Channel, "jti-cache-concern").Err(); err != nil {
		t.Fatalf("publish on base: %v", err)
	}
	time.Sleep(time.Second)
	if !stack.accepted(t, tok) {
		t.Fatal("lenny-ops took a revocation from the base Redis while --redis-cache-pubsub-url names another instance")
	}
	stack.publishUntilRevoked(t, cache.Client, "jti-cache-concern", tok)
}

// spec: 25.4 (The lenny-ops Service: Authentication), 12.4 (Redis HA and Failure Modes)
// diagnosis: a failure means lenny-ops, with no dedicated Cache/Pub-Sub URL,
// does not subscribe to token.revoked on the base Redis client, so a
// revocation reaches it only at its next 30 s Postgres read. Check the
// redistopology fallback in startRevocationSet.
func TestOpsRevocationSetUsesBaseRedisWithoutCacheURL(t *testing.T) {
	opsprocess.SkipUnlessAvailable(t)
	pg := startPostgres(t)
	base := containers.StartRedis(t, containers.RedisOptions{})
	stack := startOps(t, pg, base, nil)

	tok := stack.mint(t, "jti-base")
	stack.waitAccepted(t, tok)
	stack.publishUntilRevoked(t, base.Client, "jti-base", tok)
}
