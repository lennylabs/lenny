// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §10.3 NET-072 startup refusal of the real
// cmd/lenny-gateway binary. Outside dev mode a gateway configured with a
// coordination lease store (Redis) refuses to start when the gateway replica
// certificate material is absent, so a production deployment cannot run its
// inter-replica forward listener without peer validation or silently lose
// its forward.
package tier9_security_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
)

// netRefusal is the text the NET-072 refusal names.
const netRefusal = "§10.3 NET-072"

// runProductionGatewayToExit runs the gateway outside dev mode with Redis
// pointing at a closed port and the extra args, and returns its output once
// it exits non-zero.
func runProductionGatewayToExit(t *testing.T, binary string, extra ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := append([]string{
		"--addr", opsLoopbackAddr(t), "--no-environment-policy", "deny-all",
		"--dev-mode=false", "--oidc-issuer-url=https://idp.acme.test", "--oidc-client-id=lenny-gateway",
		"--redis-url=redis://127.0.0.1:1/0",
	}, extra...)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(opsIsolatedEnv(),
		"LENNY_TLS_TERMINATED_UPSTREAM=true", "LENNY_REDIS_ALLOW_INSECURE=true",
		"LENNY_HOLDER_FORWARD_ADVERTISE_ADDR=127.0.0.1:50054", stackkey.Env(t))
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("lenny-gateway still running after 60s; want an exit\n%s", out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("lenny-gateway exit = %v, want a non-zero exit\n%s", err, out)
	}
	return string(out)
}

// spec: 10.3 (mTLS PKI) NET-072
// diagnosis: a failure means a gateway outside dev mode with a coordination
// lease store started without the gateway replica certificate, key, or CA.
// Its forward listener could then not validate a peer replica, and every
// off-holder request would be refused. The positive control shows that the
// same gateway with the material passes the check and stops later, at the
// unreachable Redis, so the refusal is the NET-072 check and not an earlier
// gate. Check validateHolderForwardMaterial in cmd/lenny-gateway.
func TestProductionGatewayRefusesLeaseStoreWithoutReplicaCertificate_spec_10_3(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	binary := gateway.Build(t)
	m := gatewaypki.Mint(t)
	cases := []struct {
		name string
		args []string
	}{
		{"no material", nil},
		{"no key", []string{"--adapter-tls-cert=" + m.CertFile, "--adapter-ca=" + m.CAFile}},
		{"no CA", []string{"--adapter-tls-cert=" + m.CertFile, "--adapter-tls-key=" + m.KeyFile}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := runProductionGatewayToExit(t, binary, c.args...)
			if !strings.Contains(out, netRefusal) {
				t.Errorf("startup log does not name the %s refusal\n%s", netRefusal, out)
			}
		})
	}
	t.Run("material present passes the check", func(t *testing.T) {
		out := runProductionGatewayToExit(t, binary,
			"--adapter-tls-cert="+m.CertFile, "--adapter-tls-key="+m.KeyFile, "--adapter-ca="+m.CAFile)
		if strings.Contains(out, netRefusal) {
			t.Errorf("a gateway with the material was refused under %s\n%s", netRefusal, out)
		}
		if !strings.Contains(strings.ToLower(out), "redis") {
			t.Errorf("the gateway with the material did not reach the Redis dial\n%s", out)
		}
	})
}
