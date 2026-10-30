//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §10.2 gateway key-set startup rule: the real
// cmd/lenny-gateway binary, configured for the AWS KMS provider with a
// pinned key-ID reference for platform:jwt-signing, is pointed at a KMS
// endpoint that refuses connections. The process must exit non-zero
// before it listens, and its log must name the key-set load failure.
package tokensigning_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// refusingAddr returns a loopback address on which nothing listens: the
// kernel hands out the port and the listener is closed at once, so a
// dial to it is refused.
func refusingAddr(t *testing.T) string {
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

// isolatedEnv returns the test's environment without any AWS_ or LENNY_
// variable, so the developer's own credentials, profile, region, or
// endpoint overrides cannot change which KMS the gateway dials.
func isolatedEnv() []string {
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "AWS_") || strings.HasPrefix(kv, "LENNY_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// spec: 10.2 (Authentication) "Verifier key set and rotation", 4.3 (Token Service)
// diagnosis: a failure means a gateway that cannot fetch the public key of
// platform:jwt-signing at startup either kept running (it listened, or it
// exited zero), which leaves a replica with no verifier and no signer for
// Lenny-issued tokens, or it died for a reason other than the key-set load
// (the log does not name the load failure), which hides the cause from an
// operator reading the crashloop.
func TestGatewayExitsBeforeListenWhenKeySetCannotLoad_spec_10_2(t *testing.T) {
	binary := gateway.Build(t)
	kmsEndpoint := refusingAddr(t)
	listenAddr := refusingAddr(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx, binary,
		"--addr", listenAddr,
		"--no-environment-policy", "deny-all",
		"--kms-provider=aws",
		"--kms-aws-region=us-east-1",
		"--kms-alias=platform:jwt-signing=1234abcd-12ab-34cd-56ef-1234567890ab",
	)
	cmd.Env = append(
		isolatedEnv(),
		"AWS_ACCESS_KEY_ID=dummy-access-key",
		"AWS_SECRET_ACCESS_KEY=dummy-secret-key",
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_ENDPOINT_URL_KMS=http://"+kmsEndpoint,
		// §17.4: the dev-mode posture keeps the OIDC and TLS startup
		// gates out of the way, so the key-set load is the first
		// startup step that can fail.
		"LENNY_DEV_MODE=true",
		"LENNY_TLS_TERMINATED_UPSTREAM=true",
	)
	out, err := cmd.CombinedOutput()
	logs := string(out)
	if ctx.Err() != nil {
		t.Fatalf("gateway still running after %s; want a startup exit\n%s", 90*time.Second, logs)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("gateway exit = %v, want a non-zero exit\n%s", err, logs)
	}
	if strings.Contains(logs, "listening on") {
		t.Errorf("gateway logged a listen before exiting\n%s", logs)
	}
	for _, want := range []string{"JWT key set", "platform:jwt-signing"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log does not name the key-set load failure (missing %q)\n%s", want, logs)
		}
	}
	if conn, derr := net.DialTimeout("tcp", listenAddr, time.Second); derr == nil {
		_ = conn.Close()
		t.Errorf("something accepted a connection on the gateway address %s after exit", listenAddr)
	}
}
