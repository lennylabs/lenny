// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §4.7 / §5.3 nonce-only acknowledgment gate.
// Nonce-only mode (requireSoPeercred: false on a pool's runtime) weakens
// the adapter-agent authentication boundary, so it is a deployer security
// opt-in of the same class as allowStandardIsolation and
// acknowledgeBestEffortScrub: a pool referencing a nonce-only runtime is
// admitted only when it carries acknowledgeNonceOnlyAuth: true. The check
// is unconditional and applies in every tenancy mode.
//
// This drives the gate through the live admin API on the Kind cluster (the
// real poolstore/admin admission path against a real lenny-postgres). The
// activation field is settable only through Runtime CRD registration (the
// admin runtime payload does not model it), so the test applies the two
// runtimes as Runtime CRs and waits for the §5.1 RuntimeReconciler to
// mirror requireSoPeercred into the gateway registry the gate reads. It
// then attempts each adversarial pool write and asserts it fails closed,
// with the acknowledged or enforcing control admitted.
//
// The gate does not branch on tenancy mode; the deployed cluster exercises
// one mode, and the dev/tenant-mode axis is covered by the in-process
// component tests (pkg/gateway/admin/pools_nonce_only_test.go). This file
// verifies the gate against the real cluster admission path and the real
// CRD-to-registry mirror.

package tier9_security_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

const (
	nonceOnlyRuntime = "t9-nonce-only-runtime"
	nonceEnforceRT   = "t9-nonce-enforce-runtime"
	nonceOnlyPoolImg = "ghcr.io/anthropic/claude-code@sha256:" +
		"0000000000000000000000000000000000000000000000000000000000000000"
)

// diagnosis: the §4.7 nonce-only acknowledgment gate did not fail closed at
// pool admission. The test applies a Runtime CR carrying requireSoPeercred:
// false (sidecar model), waits for the RuntimeReconciler to mirror it into
// the gateway registry, then drives three adversarial writes through the
// live admin API: an unacknowledged pool create, a runtimeRef swap from an
// enforcing runtime to the nonce-only runtime without the ack, and a PUT
// toggling the ack off on a nonce-only pool. Each must be rejected with 400
// VALIDATION_ERROR while the acknowledged create, the acknowledged swap,
// and an enforcing-runtime pool are admitted. An admitted unacknowledged
// pool means a deployer can put a pool into nonce-only mode without the
// security opt-in, weakening the adapter-agent authentication boundary.
// spec: 4.7, 5.3
func TestNonceOnlyAcknowledgmentGate_spec_4_7(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the admin API is the gateway", gatewayDeploymentName)
	}
	if !deploymentReadyT9(t, c, auditDeployment) {
		t.Skipf("precondition not met: %s is not Ready; the registry is Postgres-backed", auditDeployment)
	}

	probe := "t9-nonce-probe"
	gatewayIP := startGatewayProbe(t, c, probe)
	admin := platformAdmin()

	// Apply the two runtimes as Runtime CRs: a nonce-only sidecar runtime
	// (requireSoPeercred: false) and an SO_PEERCRED-enforcing one. The
	// reconciler mirrors requireSoPeercred CRD-to-store, where the gate reads
	// it; the admin runtime payload does not model the field, so a Runtime CR
	// is the only registration path that activates it.
	applyNonceRuntimeCR(t, c, nonceOnlyRuntime, false)
	applyNonceRuntimeCR(t, c, nonceEnforceRT, true)
	waitRuntimeMirrored(t, c, probe, gatewayIP, admin, nonceOnlyRuntime)
	waitRuntimeMirrored(t, c, probe, gatewayIP, admin, nonceEnforceRT)

	// Create: an unacknowledged nonce-only pool is rejected.
	t.Run("create-rejected-without-ack", func(t *testing.T) {
		body := noncePoolJSON("t9-nonce-noack", nonceOnlyRuntime, false)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, body)
		if res.statusCode != 400 {
			cleanupPool(t, c, probe, gatewayIP, admin, "t9-nonce-noack")
			t.Fatalf("§4.7: unacknowledged nonce-only pool admitted with status %d, want 400 (body %q)",
				res.statusCode, res.body)
		}
		if code := res.errorCode(); code != "VALIDATION_ERROR" {
			t.Errorf("§4.7: rejection code %q, want VALIDATION_ERROR (body %q)", code, res.body)
		}
		if !strings.Contains(res.body, "acknowledgeNonceOnlyAuth") {
			t.Errorf("§4.7: rejection does not mention acknowledgeNonceOnlyAuth (body %q)", res.body)
		}
	})

	// Create: an acknowledged nonce-only pool is admitted.
	t.Run("create-admitted-with-ack", func(t *testing.T) {
		body := noncePoolJSON("t9-nonce-ack", nonceOnlyRuntime, true)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, body)
		t.Cleanup(func() { cleanupPool(t, c, probe, gatewayIP, admin, "t9-nonce-ack") })
		if res.statusCode != 201 {
			t.Fatalf("§4.7: acknowledged nonce-only pool rejected with status %d, want 201 (body %q)",
				res.statusCode, res.body)
		}
	})

	// Create an enforcing-runtime pool, then swap its runtimeRef to the
	// nonce-only runtime without the ack: rejected. The same swap with the
	// ack is admitted.
	t.Run("runtimeref-swap-to-nonce-only", func(t *testing.T) {
		pool := "t9-nonce-swap"
		create := noncePoolJSON(pool, nonceEnforceRT, false)
		res := gatewayRequestRetry(t, c, probe, gatewayIP, "POST", "/v1/admin/pools", admin, create)
		t.Cleanup(func() { cleanupPool(t, c, probe, gatewayIP, admin, pool) })
		if res.statusCode != 201 {
			t.Fatalf("seed enforcing pool: status %d, want 201 (body %q)", res.statusCode, res.body)
		}
		etag := poolETag(t, c, probe, gatewayIP, admin, pool)

		// Swap to the nonce-only runtime without the ack: rejected.
		swap := fmt.Sprintf(`{"runtimeRef":%q}`, nonceOnlyRuntime)
		res = putPoolWithETag(t, c, probe, gatewayIP, admin, pool, swap, etag)
		if res.statusCode != 400 {
			t.Fatalf("§4.7: unacknowledged runtimeRef swap admitted with status %d, want 400 (body %q)",
				res.statusCode, res.body)
		}
		if !strings.Contains(res.body, "acknowledgeNonceOnlyAuth") {
			t.Errorf("§4.7: swap rejection does not mention acknowledgeNonceOnlyAuth (body %q)", res.body)
		}

		// The same swap with the ack set is admitted.
		swapAck := fmt.Sprintf(`{"runtimeRef":%q,"acknowledgeNonceOnlyAuth":true}`, nonceOnlyRuntime)
		res = putPoolWithETag(t, c, probe, gatewayIP, admin, pool, swapAck, etag)
		if res.statusCode != 200 {
			t.Fatalf("§4.7: acknowledged runtimeRef swap rejected with status %d, want 200 (body %q)",
				res.statusCode, res.body)
		}

		// Toggling the ack back off on the now-nonce-only pool is rejected.
		etag2 := poolETag(t, c, probe, gatewayIP, admin, pool)
		off := `{"acknowledgeNonceOnlyAuth":false}`
		res = putPoolWithETag(t, c, probe, gatewayIP, admin, pool, off, etag2)
		if res.statusCode != 400 {
			t.Fatalf("§4.7: acknowledgment toggle-off admitted with status %d, want 400 (body %q)",
				res.statusCode, res.body)
		}
	})
}

// applyNonceRuntimeCR applies a cluster-scoped sidecar Runtime CR with the
// §4.7 requireSoPeercred field set, and registers a t.Cleanup that deletes
// it. require=false is the nonce-only activation the gate trips on.
func applyNonceRuntimeCR(t *testing.T, c *kind.Cluster, name string, require bool) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: Runtime
metadata:
  name: %s
  labels:
    lenny.dev/test: tier9-nonce-only
spec:
  type: agent
  image: %s
  integrationLevel: full
  executionMode: session
  isolationProfile: sandboxed
  deploymentModel: sidecar
  requireSoPeercred: %t
`, name, nonceOnlyPoolImg, require)
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("apply Runtime CR %s: %v (output %q)", name, err, out)
	}
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
}

// waitRuntimeMirrored polls the gateway registry until the named runtime is
// resolvable, so the gate sees the CRD-to-store mirror the RuntimeReconciler
// performs. A runtime that never mirrors fails the test rather than leaving
// the gate cases to misreport an unmirrored runtime as a gate bug.
func waitRuntimeMirrored(t *testing.T, c *kind.Cluster, probe, gatewayIP string, admin gwRole, name string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		res := gatewayRequest(t, c, probe, gatewayIP, "GET", "/v1/admin/runtimes/"+name, admin, "")
		if res.curlExit == 0 && res.statusCode == 200 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime %s not mirrored into the gateway registry within 60s (last status %d, body %q)",
				name, res.statusCode, res.body)
		}
		time.Sleep(2 * time.Second)
	}
}

// noncePoolJSON renders a POST /v1/admin/pools body for a sandboxed
// session-mode pool, with the §5.3 acknowledgeNonceOnlyAuth opt-in set or
// cleared.
func noncePoolJSON(name, runtimeRef string, ack bool) string {
	return fmt.Sprintf(
		`{"name":%q,"runtimeRef":%q,"isolationProfile":"sandboxed","executionMode":"session","acknowledgeNonceOnlyAuth":%t}`,
		name, runtimeRef, ack,
	)
}

// poolETag reads the §15.1 optimistic-concurrency ETag a GET on the pool
// reports so a later PUT satisfies the If-Match precondition.
func poolETag(t *testing.T, c *kind.Cluster, probe, gatewayIP string, admin gwRole, name string) string {
	t.Helper()
	res := gatewayRequest(t, c, probe, gatewayIP, "GET", "/v1/admin/pools/"+name, admin, "")
	var env struct {
		ETag string `json:"etag"`
	}
	_ = json.Unmarshal([]byte(res.body), &env)
	return env.ETag
}

// putPoolWithETag runs a pool PUT carrying the §15.1 If-Match header from
// etag, using a curl invocation that adds the header the shared
// gatewayRequest helper does not set.
func putPoolWithETag(t *testing.T, c *kind.Cluster, probe, gatewayIP string, admin gwRole, name, body, etag string) gwResponse {
	t.Helper()
	if strings.Contains(body, "'") {
		t.Fatalf("putPoolWithETag body contains a single quote: %q", body)
	}
	url := fmt.Sprintf("http://%s:8080/v1/admin/pools/%s", gatewayIP, name)
	cmd := fmt.Sprintf(
		"curl -sS -m 10 -X PUT -H 'X-Lenny-Tenant-ID: %s' -H 'X-Lenny-Roles: %s' "+
			"-H 'X-Lenny-User-ID: %s' -H 'Content-Type: application/json' -H 'If-Match: %s' "+
			"--data '%s' -w '\\nLENNYPROBE status=%%{http_code} exit=%%{exitcode}\\n' %s 2>&1",
		admin.tenant, admin.roles, admin.user, etag, body, url,
	)
	out, _ := c.KubectlOut(t, "-n", lennySystemNS, "exec", probe, "--", "sh", "-c", cmd)
	return parseGatewayResponse(out)
}
