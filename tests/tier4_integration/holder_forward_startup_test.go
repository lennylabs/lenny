// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration tests for the §10.1.1 Replica address startup contract
// of the real cmd/lenny-gateway binary. A gateway configured with Redis has a
// coordination lease store, so it refuses to start without a valid forward
// advertise address. A dev-mode gateway with Redis and an advertise address
// but no gateway replica certificate starts, advertises the address, and
// serves no forward listener.
package tier4_integration_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
)

// holderForwardIsolatedEnv drops every LENNY_ variable from the test
// environment so the developer's own settings cannot change the posture.
func holderForwardIsolatedEnv() []string {
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LENNY_") {
			out = append(out, kv)
		}
	}
	return out
}

// runGatewayToExit runs the gateway binary with args and env and returns its
// combined output once it exits, failing when it is still running after 60s
// or exits zero.
func runGatewayToExit(t *testing.T, binary string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("lenny-gateway still running after 60s; want a startup refusal\n%s", out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("lenny-gateway exit = %v, want a non-zero exit\n%s", err, out)
	}
	return string(out)
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address
// diagnosis: a failure means a gateway configured with Redis, and therefore
// a coordination lease store, started without a forward advertise address,
// or with an invalid one. Its lease holder value would then carry no address
// a peer replica can reach, and every off-holder request for the sessions it
// coordinates would be refused as not delivered. Check
// resolveCoordinationHolder in cmd/lenny-gateway/redis_quota.go.
func TestGatewayWithLeaseStoreRefusesMissingAdvertiseAddress_spec_10_1_1(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	binary := gateway.Build(t)
	for name, addr := range map[string]string{"absent": "", "no port": "10.0.0.7", "port 0": "10.0.0.7:0"} {
		t.Run(name, func(t *testing.T) {
			env := append(holderForwardIsolatedEnv(),
				"LENNY_DEV_MODE=true", "LENNY_TLS_TERMINATED_UPSTREAM=true", "LENNY_REDIS_ALLOW_INSECURE=true",
				"LENNY_HOLDER_FORWARD_ADVERTISE_ADDR="+addr, stackkey.Env(t))
			out := runGatewayToExit(t, binary, env,
				"--addr", "127.0.0.1:0", "--no-environment-policy", "deny-all", "--redis-url=redis://127.0.0.1:1/0")
			if !strings.Contains(out, "Replica address") || !strings.Contains(out, "--holder-forward-advertise-addr") {
				t.Errorf("startup log does not name the §10.1.1 Replica address refusal\n%s", out)
			}
		})
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Replica address, 10.3 (mTLS PKI) NET-072
// diagnosis: a failure means a dev-mode gateway with Redis and an advertise
// address, but no gateway replica certificate, either refused to start or
// opened a forward listener. §10.3 NET-072 lets dev mode run without the
// material, and then the gateway serves no forward listener and every
// forward is not delivered. Check buildHolderForwardTransport and
// startHolderForwardListener in cmd/lenny-gateway.
func TestDevGatewayWithoutMaterialServesNoForwardListener_spec_10_3(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--dev-mode", "--redis-url=redis://"+rd.Addr+"/0")
	conn, err := net.DialTimeout("tcp", gw.ForwardAddr(), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("a forward listener accepted a connection on %s in dev mode without certificate material", gw.ForwardAddr())
	}
	if strings.Contains(gw.Logs(), "CH-HOLDERFORWARD listening") {
		t.Errorf("startup log reports a forward listener\n%s", gw.Logs())
	}
}
