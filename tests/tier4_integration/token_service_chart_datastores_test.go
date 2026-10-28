//go:build integration

// SPDX-License-Identifier: MIT

package tier4_integration_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/helm"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	"github.com/lennylabs/lenny/tests/testinfra/tlsredis"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// chartTokenServiceDatastoreConfig is chartDatastoreConfig for the
// lenny-token-service Deployment.
func chartTokenServiceDatastoreConfig(t *testing.T, set []string) (args, env []string) {
	t.Helper()
	return chartDatastoreConfig(t, "lenny-token-service", set)
}

// chartDatastoreConfig renders the chart with the given --set overrides
// and returns the first container's datastore configuration in the named
// Deployment as a process would receive it in the cluster: every
// LENNY_REDIS_*, LENNY_POSTGRES_*, and LENNY_PG_* env entry, with each
// secretKeyRef resolved against the rendered lenny-datastore-conn Secret,
// and every --redis-* or --postgres-* arg. The listener, KMS, mTLS, and
// other args and env stay with the test harness, which supplies its own.
func chartDatastoreConfig(t *testing.T, deployment string, set []string) (args, env []string) {
	t.Helper()
	root := schematest.RepoRoot(t)
	manifests := helm.Render(t, helm.Options{
		Chart:  filepath.Join(root, "charts/lenny"),
		Values: filepath.Join(root, "charts/lenny/presets/dev.yaml"),
		Set:    set,
	})
	secret := map[string]any{}
	if s, ok := manifests.Find("Secret", "lenny-datastore-conn"); ok {
		secret, _ = s.Raw["stringData"].(map[string]any)
	}
	dep := manifests.MustFind(t, "Deployment", deployment)
	spec, _ := dep.Raw["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	if len(containers) == 0 {
		t.Fatalf("the %s Deployment renders no container", deployment)
	}
	c, _ := containers[0].(map[string]any)
	for _, a := range asSlice(c["args"]) {
		s, _ := a.(string)
		if strings.HasPrefix(s, "--redis-") || strings.HasPrefix(s, "--postgres-") {
			args = append(args, s)
		}
	}
	for _, e := range asSlice(c["env"]) {
		entry, _ := e.(map[string]any)
		name, _ := entry["name"].(string)
		if !isDatastoreEnv(name) {
			continue
		}
		if v, ok := entry["value"].(string); ok {
			env = append(env, name+"="+v)
			continue
		}
		vf, _ := entry["valueFrom"].(map[string]any)
		ref, _ := vf["secretKeyRef"].(map[string]any)
		if ref["name"] != "lenny-datastore-conn" {
			continue
		}
		key, _ := ref["key"].(string)
		v, ok := secret[key].(string)
		if !ok {
			t.Fatalf("the %s env %s reads lenny-datastore-conn key %q, which the chart does not render", deployment, name, key)
		}
		env = append(env, name+"="+v)
	}
	return args, env
}

// isDatastoreEnv reports whether an env entry configures a datastore
// connection.
func isDatastoreEnv(name string) bool {
	for _, p := range []string{"LENNY_REDIS_", "LENNY_POSTGRES_", "LENNY_PG_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// asSlice returns v as a []any, or nil.
func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// spec: 4.3 (Token Service), 13.3 (Credential Flow) "Write-before-issue ordering", 13.3 (Credential Flow) "Token rotation and revocation", 12.4 (Redis HA and Failure Modes) "Logical separation of Redis concerns"
// diagnosis: no issued_tokens row for the exchanged jti means the chart
// does not hand the Token Service the Postgres DSN, so it issues tokens
// with no durable record, rotation, or source-revocation check. No jti on
// the Cache/Pub-Sub instance after a rotation means the chart passes no
// Redis connection, or the binary publishes on the base instance rather
// than the concern the gateway replicas and lenny-ops subscribe on. A
// failure naming a datastore Secret key means the Deployment reads a key
// the chart's lenny-datastore-conn Secret does not render.
func TestChartConfiguredTokenServiceWritesIssuedTokensAndPublishesRevocations_spec_4_3(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	tokensvc.SkipUnlessAvailable(t)
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	_, signer := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	pg := startKeySetPostgres(t)
	base := tlsredis.Start(t, "base-secret")
	cache := tlsredis.Start(t, "cache-secret")

	// The chart refuses a production render with Redis configured and no
	// gateway replica certificate (§10.3 NET-072), so mTLS stays on.
	chartArgs, chartEnv := chartTokenServiceDatastoreConfig(t, []string{
		"tokenService.enabled=true",
		"global.devMode=false",
		"mtls.enabled=true",
		"postgres.dsn=" + pg.DSN,
		"redis.provider=external",
		"redis.url=" + base.URL,
		"redis.concerns.cachePubSubUrl=" + cache.URL,
	})
	ts := tokensvc.StartOpts(t, tokensvc.Options{
		Args: append(append([]string{"--kms-provider=local"}, aliasArgs(refs)...), chartArgs...),
		Env:  chartEnv,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	onCache := cache.Client.Subscribe(ctx, revocationprop.Channel)
	t.Cleanup(func() { _ = onCache.Close() })
	onBase := base.Client.Subscribe(ctx, revocationprop.Channel)
	t.Cleanup(func() { _ = onBase.Close() })
	for _, sub := range []interface {
		Receive(context.Context) (any, error)
	}{onCache, onBase} {
		if _, err := sub.Receive(ctx); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}

	sign := func(jti string) string {
		tok, err := signer.Sign(ctx, rotationCallerClaims(jti))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return tok
	}
	tok := mintExchanged(t, ts.BaseURL(), sign("chart-caller"), sign("chart-subject"), "")
	jti := rotatedJTI(t, tok)
	var rows int
	if err := pg.Pool.QueryRow(ctx, `SELECT count(*) FROM issued_tokens WHERE jti = $1`, jti).Scan(&rows); err != nil {
		t.Fatalf("read issued_tokens: %v", err)
	}
	if rows != 1 {
		t.Fatalf("issued_tokens holds %d rows for the exchanged jti %s, want 1\n%s", rows, jti, ts.Logs())
	}

	// A self-rotation revokes tok in issued_tokens and publishes its jti.
	mintExchanged(t, ts.BaseURL(), tok, tok, "")
	var revoked bool
	if err := pg.Pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM issued_tokens WHERE jti = $1`, jti).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("the rotation did not revoke %s in issued_tokens (revoked=%v, err=%v)\n%s", jti, revoked, err, ts.Logs())
	}
	select {
	case msg := <-onCache.Channel():
		if msg.Payload != jti {
			t.Fatalf("the cache/pub-sub instance received %q, want the rotated jti %q", msg.Payload, jti)
		}
	case <-time.After(rotationPropagationBound):
		t.Fatalf("no jti reached the cache/pub-sub instance within %s\n%s", rotationPropagationBound, ts.Logs())
	}
	select {
	case msg := <-onBase.Channel():
		t.Fatalf("the rotation published %q on the base instance, where no subscriber listens", msg.Payload)
	case <-time.After(300 * time.Millisecond):
	}
}
