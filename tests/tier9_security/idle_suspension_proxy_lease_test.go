// SPDX-License-Identifier: MIT

//go:build security

package tier9_security_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// proxyIdleRuntimeEnv names a runtime on a proxy-delivery credential pool
// whose sessionPolicy.maxClientIdleSeconds is short enough for a test to wait
// out (at most proxyIdleMaxWait).
const proxyIdleRuntimeEnv = "LENNY_T9_PROXY_IDLE_RUNTIME"

// proxyIdleMaxWait bounds the wait for the idle suspension and the resume.
const proxyIdleMaxWait = 5 * time.Minute

// sessionCredentialsFile is the per-session credential file the adapter
// writes on the session's pod.
func sessionCredentialsFile(sessionID string) string {
	return "/run/lenny/slots/" + sessionID + "/credentials.json"
}

// spec: 7.3 (Credential leases on the replacement pod), 4.9 (Credential
// Leasing Service), 6.2 (Pod State Machine)
//
// diagnosis: an idle suspension released the session's pod without retiring
// its proxy lease, so the lease token the released pod held still
// authenticates at the LLM proxy, or the session resumed on a replacement pod
// holds no lease whose token authenticates. Either a credential outlives the
// pod it was minted for, or a resumed session cannot reach its provider.
func TestIdleSuspendedProxyLeaseRetiredAndReplaced_spec_7_3(t *testing.T) {
	runtime := os.Getenv(proxyIdleRuntimeEnv)
	if runtime == "" {
		t.Skipf("precondition not met: %s names no runtime on a proxy-delivery credential pool with a short maxClientIdleSeconds", proxyIdleRuntimeEnv)
	}
	d := sessiondriver.New(t)
	args, err := d.Cluster().KubectlOut(t, "-n", "lenny-system", "get", "deploy", "lenny-gateway",
		"-o", "jsonpath={.spec.template.spec.containers[0].args}")
	if err != nil || !strings.Contains(args, "--max-suspended-session-seconds") {
		t.Skipf("precondition not met: the deployed gateway predates the idle suspension (no --max-suspended-session-seconds); redeploy images built from this tree: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*proxyIdleMaxWait)
	defer cancel()
	tenant := fmt.Sprintf("t9-idle-proxy-%d", time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	if err := d.AllowSessionsWithNoEnvironment(ctx, tenant); err != nil {
		t.Fatalf("allow sessions with no environment: %v", err)
	}
	sess, err := d.CreateAndStart(ctx, tenant, runtime)
	if err != nil {
		t.Skipf("precondition not met: no session on the proxy-delivery pool: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_ = d.Terminate(cctx, tenant, sess.ID)
	})
	oldPod := sess.PodAssignment
	oldToken, proxyURL := readProxyLease(t, d, oldPod, sess.ID)

	suspended, ok := d.WaitForState(ctx, tenant, sess.ID, proxyIdleMaxWait, "suspended")
	if !ok {
		t.Fatalf("session %s did not reach suspended within %s", sess.ID, proxyIdleMaxWait)
	}
	if suspended.PodAssignment != "" {
		t.Fatalf("idle-suspended session names pod %q, want none", suspended.PodAssignment)
	}

	if _, err := d.SendMessage(ctx, tenant, sess.ID, "wake"); err != nil {
		t.Fatalf("send to the suspended session: %v", err)
	}
	resumed, ok := d.WaitForState(ctx, tenant, sess.ID, proxyIdleMaxWait, "running")
	if !ok {
		t.Fatalf("session %s did not resume within %s", sess.ID, proxyIdleMaxWait)
	}
	newToken, _ := readProxyLease(t, d, resumed.PodAssignment, sess.ID)
	if newToken == oldToken {
		t.Fatal("the resumed session holds the released pod's lease token")
	}
	if code := proxyStatus(t, d, resumed.PodAssignment, proxyURL, oldToken); code != "401" {
		t.Errorf("the released pod's lease token answered %s at the proxy, want 401", code)
	}
	if code := proxyStatus(t, d, resumed.PodAssignment, proxyURL, newToken); code == "401" {
		t.Errorf("the replacement lease token was refused at the proxy")
	}
}

// readProxyLease reads the proxy lease token and proxy URL from the
// session's credential file on pod.
func readProxyLease(t *testing.T, d *sessiondriver.Driver, pod, sessionID string) (token, proxyURL string) {
	t.Helper()
	path := sessionCredentialsFile(sessionID)
	out, err := d.Cluster().KubectlOut(t, "-n", "lenny-agents", "exec", pod, "--", "cat", path)
	if err != nil {
		t.Fatalf("read %s on %s: %v", path, pod, err)
	}
	var doc struct {
		Providers []struct {
			MaterializedConfig struct {
				ProxyURL   string `json:"proxyUrl"`
				LeaseToken string `json:"leaseToken"`
			} `json:"materializedConfig"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Providers) == 0 {
		t.Fatalf("decode %s on %s: %v", path, pod, err)
	}
	cfg := doc.Providers[0].MaterializedConfig
	if cfg.LeaseToken == "" {
		t.Skipf("precondition not met: pod %s holds no proxy lease token", pod)
	}
	return cfg.LeaseToken, cfg.ProxyURL
}

// proxyStatus issues a proxy request with token from inside pod and returns
// the HTTP status code.
func proxyStatus(t *testing.T, d *sessiondriver.Driver, pod, proxyURL, token string) string {
	t.Helper()
	script := fmt.Sprintf(`curl -s -o /dev/null -w '%%{http_code}' -X POST -H 'x-api-key: %s' -H 'content-type: application/json' -d '{"model":"m","messages":[]}' %s/v1/messages`, token, proxyURL)
	out, err := d.Cluster().KubectlOut(t, "-n", "lenny-agents", "exec", pod, "--", "sh", "-c", script)
	if err != nil {
		t.Skipf("precondition not met: pod %s cannot issue a proxy request: %v", pod, err)
	}
	return strings.TrimSpace(out)
}
