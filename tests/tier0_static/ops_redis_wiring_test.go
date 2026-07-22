// SPDX-License-Identifier: MIT

package tier0_static

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// The §25.5 operational-event read side (SSE, polling, webhook worker)
// reads the platform-scoped ops:events:stream Redis stream, and its
// Redis-down gateway-buffer fallback is reachable end to end only when the
// lenny-ops Deployment actually wires Redis. The chart previously rendered
// lenny-ops with no Redis connection at all, so a deployed lenny-ops never
// had a Redis source to lose and never switched to the gateway-buffer
// fall-back during a real outage. These tests pin the chart wiring that
// makes the read source, and therefore the fall-back, reachable.

// opsContainerEnv returns the env list of the lenny-ops Deployment's ops
// container as a name -> entry map. Each entry is the raw YAML map so a
// caller can assert either a literal value or a valueFrom.secretKeyRef.
func opsContainerEnv(t *testing.T, m helm.Manifests) map[string]map[string]any {
	t.Helper()
	dep := m.MustFind(t, "Deployment", "lenny-ops")
	out := map[string]map[string]any{}
	for _, c := range podContainers(dep) {
		cm, _ := c.(map[string]any)
		if name, _ := cm["name"].(string); name != "ops" {
			continue
		}
		env, _ := cm["env"].([]any)
		for _, e := range env {
			em, _ := e.(map[string]any)
			if n, _ := em["name"].(string); n != "" {
				out[n] = em
			}
		}
		return out
	}
	t.Fatalf("lenny-ops Deployment has no ops container")
	return nil
}

// secretKeyRefOf returns the secret name and key an env entry sources its
// value from, or false when the entry is not a secretKeyRef.
func secretKeyRefOf(entry map[string]any) (secret, key string, ok bool) {
	vf, _ := entry["valueFrom"].(map[string]any)
	if vf == nil {
		return "", "", false
	}
	skr, _ := vf["secretKeyRef"].(map[string]any)
	if skr == nil {
		return "", "", false
	}
	secret, _ = skr["name"].(string)
	key, _ = skr["key"].(string)
	return secret, key, true
}

// spec: §25.5 (spec/25_agent-operability.md lines 2558, 2602) "`lenny-ops`
// reads from the Redis stream" / "**Redis capped stream.** Key:
// `ops:events:stream`", and §25.4 line 1512 (StoreRouter: the event stream
// routes to `PlatformRedis()`). When redis.url is set, the lenny-ops
// Deployment must read LENNY_REDIS_URL from the lenny-datastore-conn Secret
// so the read side has a Redis source to consume and, on a Redis outage,
// to fall back from to the gateway buffer.
//
// This asserts the corrected outcome directly: against the pre-fix chart,
// which rendered the ops container with no Redis env at all, opsContainerEnv
// carries no LENNY_REDIS_URL and this test fails.
func TestOpsDeploymentWiresRedisURLFromDatastoreSecret_spec_25_5(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	m := helm.Render(t, helm.Options{
		Chart:   "../../charts/lenny",
		Release: "lenny",
		Set:     []string{"coredns.clusterIP=10.96.0.10", "redis.url=rediss://:pw@lenny-redis.lenny-system.svc:6380"},
	})

	env := opsContainerEnv(t, m)
	entry, ok := env["LENNY_REDIS_URL"]
	if !ok {
		t.Fatalf("§25.5: lenny-ops must wire LENNY_REDIS_URL when redis.url is set; the ops container " +
			"env carries no LENNY_REDIS_URL, so the deployed read side has no ops:events:stream source " +
			"and the Redis-down gateway-buffer fall-back is unreachable end to end")
	}
	secret, key, isRef := secretKeyRefOf(entry)
	if !isRef {
		t.Fatalf("§25.5: LENNY_REDIS_URL must be sourced from the lenny-datastore-conn Secret so no "+
			"credential lands in the pod spec; got a non-secretKeyRef entry %v", entry)
	}
	if secret != "lenny-datastore-conn" || key != "redis-url" {
		t.Fatalf("§25.5: LENNY_REDIS_URL must read Secret lenny-datastore-conn key redis-url; got "+
			"Secret %q key %q", secret, key)
	}
}

// spec: §25.5 cold-start (spec/25_agent-operability.md line 2784-2786) —
// without Redis the read side runs on lenny-ops's own per-replica in-memory
// ring buffer. A stock render that leaves redis.url empty must therefore
// wire no LENNY_REDIS_URL, so the binary takes the degraded single-process
// path rather than dialing a nonexistent Redis. This pins that the wiring
// is conditional on redis.url, not unconditional.
func TestOpsDeploymentOmitsRedisWhenUnset_spec_25_5(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	m := helm.Render(t, helm.Options{
		Chart:   "../../charts/lenny",
		Release: "lenny",
		Set:     []string{"coredns.clusterIP=10.96.0.10"},
	})
	env := opsContainerEnv(t, m)
	if _, ok := env["LENNY_REDIS_URL"]; ok {
		t.Fatalf("§25.5 cold-start: a stock render with redis.url empty must wire no LENNY_REDIS_URL on " +
			"lenny-ops (the read side falls back to its own in-memory buffer); the env carries one")
	}
}

// spec: §12.4 (Redis AUTH+TLS startup invariant) via §17.4 devMode — the
// e2e and dev installs back lenny-ops with a plaintext no-password Redis.
// lenny-ops fail-closes at startup on a plaintext redis:// URL without the
// insecure opt-out (cmd/lenny-ops/deps_setup.go), so a devMode render that
// wires Redis must also pass --redis-allow-insecure, matching the gateway.
// Without it the deployed lenny-ops crash-loops instead of reaching the
// read surface.
//
// Against the pre-fix chart, which rendered no --redis-allow-insecure arg
// on lenny-ops in any mode, this test fails.
func TestOpsDeploymentAcknowledgesInsecureRedisInDevMode_spec_12_4(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	m := helm.Render(t, helm.Options{
		Chart:   "../../charts/lenny",
		Release: "lenny",
		Set: []string{
			"coredns.clusterIP=10.96.0.10",
			"global.devMode=true",
			"redis.url=redis://lenny-redis.lenny-system.svc:6379",
		},
	})
	args := containerArgs(t, m, "lenny-ops")
	found := false
	for _, a := range args {
		if a == "--redis-allow-insecure" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("§12.4/§17.4 devMode: lenny-ops must pass --redis-allow-insecure when devMode wires a "+
			"plaintext dev Redis, or it fail-closes at startup; args=%v", args)
	}
}
