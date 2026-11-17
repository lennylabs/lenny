//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §10.2 Token Service key-set startup rule: the
// real cmd/lenny-token-service binary, configured for the AWS KMS
// provider with a pinned key-ID reference for platform:jwt-signing, is
// pointed at a KMS endpoint that refuses connections. The process must
// exit non-zero before it listens, and its log must name the key-set
// load failure.
package tokensigning_test

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// spec: 10.2 (Authentication) "Verifier key set and rotation", 4.3 (Token Service)
// diagnosis: a failure means a Token Service replica that cannot fetch the
// public key of platform:jwt-signing at startup either kept running (it
// listened, or it exited zero), which leaves a replica with no verifier
// and no signer for POST /v1/oauth/token, or it died for a reason other
// than the key-set load (the log does not name the load failure), which
// hides the cause from an operator reading the crashloop.
func TestTokenServiceExitsBeforeListenWhenKeySetCannotLoad_spec_10_2(t *testing.T) {
	binary := tokenservice.Build(t)
	kmsEndpoint := refusingAddr(t)
	httpAddr := refusingAddr(t)
	grpcAddr := refusingAddr(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx, binary,
		"--addr", httpAddr,
		"--grpc-addr", grpcAddr,
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
	)
	out, err := cmd.CombinedOutput()
	logs := string(out)
	if ctx.Err() != nil {
		t.Fatalf("token service still running after %s; want a startup exit\n%s", 90*time.Second, logs)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("token service exit = %v, want a non-zero exit\n%s", err, logs)
	}
	if strings.Contains(logs, "listening on") {
		t.Errorf("token service logged a listen before exiting\n%s", logs)
	}
	for _, want := range []string{"JWT key set", "platform:jwt-signing"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log does not name the key-set load failure (missing %q)\n%s", want, logs)
		}
	}
	for _, addr := range []string{httpAddr, grpcAddr} {
		if conn, derr := net.DialTimeout("tcp", addr, time.Second); derr == nil {
			_ = conn.Close()
			t.Errorf("something accepted a connection on the token service address %s after exit", addr)
		}
	}
}
