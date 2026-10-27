// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the lenny-ops production startup refusals. The
// real cmd/lenny-ops binary in §17.9.1 production mode (--environment=prod)
// must refuse to start on the local KMS provider, whose key is derived in
// process, and must refuse the dev embedded-OIDC trust hook, which would
// admit tokens signed with a shared HMAC key. An --environment value outside
// the §17.9.1 closed set, such as PROD, must refuse to start rather than run
// outside production mode. Each refusal exits non-zero before the listener
// starts.
package tier9_security_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
)

// opsLoopbackAddr returns a loopback address nothing listens on.
func opsLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// opsIsolatedEnv drops every LENNY_ and AWS_ variable from the test
// environment so the developer's own settings cannot change the posture.
func opsIsolatedEnv() []string {
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LENNY_") || strings.HasPrefix(kv, "AWS_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// runOpsToExit runs the lenny-ops binary with args and returns its output
// once it exits, failing the test when it is still running after 60 s.
func runOpsToExit(t *testing.T, binary, addr string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append([]string{"--addr", addr}, args...)...)
	cmd.Env = opsIsolatedEnv()
	out, err := cmd.CombinedOutput()
	logs := string(out)
	if ctx.Err() != nil {
		t.Fatalf("lenny-ops still running after 60s; want a startup refusal\n%s", logs)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("lenny-ops exit = %v, want a non-zero exit\n%s", err, logs)
	}
	return logs
}

// assertNotListening fails when anything accepts a connection on addr.
func assertNotListening(t *testing.T, addr string) {
	t.Helper()
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = conn.Close()
		t.Errorf("something accepted a connection on the lenny-ops address %s after exit", addr)
	}
}

// spec: 17.9.1 (Composition Dimensions) "Production mode", 17.4 (Dev mode guard rails), 25.4 (The lenny-ops Service: Startup and Readiness), 10.2 (Authentication)
// diagnosis: a failure means a lenny-ops in production mode (--environment=prod)
// started on the local KMS provider, whose signing key is derived in process
// from a master key file, so the production refusal of local that the gateway
// and Token Service apply does not hold on lenny-ops. loadOpsKeySet calls
// providerflags.Resolve, which returns ErrLocalForbidden in production mode.
func TestOpsProductionRefusesLocalKMSProvider(t *testing.T) {
	binary := opsprocess.Build(t)
	for _, provider := range []string{"", "--kms-provider=", "--kms-provider=local"} {
		addr := opsLoopbackAddr(t)
		args := []string{"--environment=prod"}
		if provider != "" {
			args = append(args, provider)
		}
		logs := runOpsToExit(t, binary, addr, args...)
		if !strings.Contains(logs, "--kms-provider=local is forbidden when --environment=prod") {
			t.Errorf("args %v: log does not name the local-provider refusal\n%s", args, logs)
		}
		assertNotListening(t, addr)
	}
}

// spec: 17.9.1 (Composition Dimensions) "Production mode", 17.4 (Dev mode guard rails), 25.4 (The lenny-ops Service: Authentication)
// diagnosis: a failure means a lenny-ops in production mode accepted the dev
// embedded-OIDC trust hook, so a holder of that shared HMAC key could mint
// tokens the production operability surface accepts. Check checkDevTrustPosture,
// which runs before the key set loads.
func TestOpsProductionRefusesDevTrustHook(t *testing.T) {
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
	// A cloud provider rules out the local-provider refusal, and the
	// trust-hook refusal fires before any KMS call, so no KMS is needed.
	logs := runOpsToExit(
		t, binary, addr,
		"--environment=prod",
		"--dev-mode",
		"--kms-provider=aws",
		"--kms-aws-region=us-east-1",
		"--bearer-trust-hmac-key-file="+key,
	)
	if !strings.Contains(logs, "--bearer-trust-hmac-key-file is the dev embedded-OIDC trust hook and is refused in production mode (--environment=prod)") {
		t.Errorf("log does not name the trust-hook refusal\n%s", logs)
	}
	assertNotListening(t, addr)
}

// spec: 17.9.1 (Composition Dimensions) "Production mode"
// diagnosis: a failure means lenny-ops started with an --environment value
// outside the §17.9.1 closed set. A case variant such as PROD would then run
// outside production mode silently, with every production refusal off, while
// the operator believed the deployment was production. The providerflags
// finalizer validates the value through deployenv.Validate before any
// subsystem is built.
func TestOpsRefusesOffSetEnvironment_spec_17_9_1(t *testing.T) {
	binary := opsprocess.Build(t)
	for _, env := range []string{"PROD", "production"} {
		addr := opsLoopbackAddr(t)
		logs := runOpsToExit(t, binary, addr, "--environment="+env)
		want := `environment "` + env + `" is not one of local, dev, staging, or prod`
		if !strings.Contains(logs, want) {
			t.Errorf("--environment=%s: log does not name the validation error %q\n%s", env, want, logs)
		}
		assertNotListening(t, addr)
	}
}

// spec: 16.1.1 (Attribute Naming) deployment_tier, 25.4 (The lenny-ops Service) Idempotency-Key
// diagnosis: a failure means lenny-ops started with a --platform-tier value
// outside tier1, tier2, and tier3. The value would read as Tier 1, so a Tier 2
// or Tier 3 install with a typo in global.deploymentTier would silently drop the
// §25.4 required Idempotency-Key and surface an off-set platform.tier. main
// calls validatePlatformTier before runOps builds any subsystem.
func TestOpsRefusesOffSetPlatformTier_spec_25_4(t *testing.T) {
	binary := opsprocess.Build(t)
	for _, tier := range []string{"TIER2", "tier4", ""} {
		addr := opsLoopbackAddr(t)
		logs := runOpsToExit(t, binary, addr, "--platform-tier="+tier)
		want := `--platform-tier "` + tier + `" is not one of tier1, tier2, or tier3`
		if !strings.Contains(logs, want) {
			t.Errorf("--platform-tier=%q: log does not name the refusal %q\n%s", tier, want, logs)
		}
		assertNotListening(t, addr)
	}
}
