// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the lenny-ops dev-mode gate. §17.4 "Dev mode
// guard rails" item 3 makes LENNY_DEV_MODE the single gate for every
// dev-mode security relaxation. The real cmd/lenny-ops binary without
// dev mode must refuse the dev bearer-trust hook at startup and must
// refuse a request that claims an identity through the X-Lenny-* headers,
// even with --production false, which is the posture of a chart install
// that leaves ops.production unset (F-17.4.35).
package tier9_security_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
)

// spec: 17.4 (Dev mode guard rails), 25.4 (The lenny-ops Service: Authentication)
// diagnosis: a failure means a lenny-ops without LENNY_DEV_MODE and without
// --production accepted the dev embedded-OIDC trust hook, so a holder of that
// shared HMAC key could mint tokens a non-dev operability surface accepts.
// Check checkDevTrustPosture in cmd/lenny-ops, which runs before the key set
// loads, and that --dev-mode defaults from LENNY_DEV_MODE.
func TestOpsRefusesDevTrustHookWithoutDevMode(t *testing.T) {
	binary := opsprocess.Build(t)
	key := filepath.Join(t.TempDir(), "dev-key.json")
	raw, err := json.Marshal(map[string]any{"keyId": "dev", "secret": []byte("dev-trust-secret")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(key, raw, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	addr := opsLoopbackAddr(t)
	// opsIsolatedEnv drops LENNY_DEV_MODE, so dev mode is off, and
	// --production is left false. The trust-hook refusal fires before any
	// KMS call, so no KMS is needed.
	logs := runOpsToExit(t, binary, addr, "--bearer-trust-hmac-key-file="+key)
	if !strings.Contains(logs, "--bearer-trust-hmac-key-file is the dev embedded-OIDC trust hook and requires --dev-mode") {
		t.Errorf("log does not name the dev-mode trust-hook refusal\n%s", logs)
	}
	assertNotListening(t, addr)
}

// spec: 17.4 (Dev mode guard rails), 25.4 (The lenny-ops Service: Authentication)
// diagnosis: a failure means a lenny-ops without LENNY_DEV_MODE and without
// --production served a request that named a tenant, a user, and the
// platform-admin role through the X-Lenny-* headers and carried no bearer, so
// any caller that reaches lenny-ops on a default chart install can claim
// platform-admin. Check buildAuthConfig in cmd/lenny-ops: outside dev mode it
// must return an AuthConfig with AllowDevHeaders false.
func TestOpsWithoutDevModeRefusesDevIdentityHeaders(t *testing.T) {
	ops := opsprocess.StartOpts(t, opsprocess.Options{Env: []string{"LENNY_DEV_MODE=false"}})

	client := &http.Client{Timeout: 10 * time.Second}
	for _, path := range []string{"/v1/admin/me", "/v1/admin/remediation-locks"} {
		req, err := http.NewRequest(http.MethodGet, ops.BaseURL()+path, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s with dev identity headers and no bearer: status %d, want 401; body=%s", path, resp.StatusCode, body)
		}
	}
	if strings.Contains(ops.Logs(), "UNAUTHENTICATED") {
		t.Errorf("lenny-ops logged an unauthenticated surface outside dev mode\n%s", ops.Logs())
	}
}
