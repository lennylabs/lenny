// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for how the real cmd/lenny-gateway binary derives
// §17.9.1 production mode. The chart renders the deployment environment
// only as --environment, so a gateway started with --environment=prod must
// run every production-only gate even when LENNY_ENV is unset or names a
// non-production value: the §16.4 lenny_env_production gauge reads 1 and
// the §12.3 audit-batching-without-SIEM startup warning fires. An
// --environment value outside the §17.9.1 closed set must refuse to start
// rather than run outside production mode.
package tier9_security_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// gatewayAuditBatchingNoSIEMWarning is the §12.3 startup warning a
// production gateway logs when T2 audit batching runs with no SIEM endpoint.
const gatewayAuditBatchingNoSIEMWarning = "Audit batching is enabled for T2 events but no SIEM is configured"

// startProductionGateway starts the gateway with --environment=prod on the
// AWS KMS stub, since production mode refuses the local provider, with
// audit batching on and no SIEM endpoint. lennyEnv is appended to the
// harness environment so a case can set LENNY_ENV to a non-production value.
func startProductionGateway(t *testing.T, lennyEnv string) *gateway.Process {
	t.Helper()
	stub := kmsstub.NewAWSServer(t)
	args := append([]string{"--environment=prod", "--audit-batching-enabled"}, stub.Args()...)
	env := append(stub.Env(), "LENNY_ENV="+lennyEnv)
	return gateway.StartOpts(t, gateway.Options{Args: args, Env: env})
}

// gatewayMetricsBody returns the body of the gateway's /metrics endpoint.
func gatewayMetricsBody(t *testing.T, gw *gateway.Process) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gw.BaseURL()+"/metrics", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200\n%s", resp.StatusCode, body)
	}
	return string(body)
}

// spec: 17.9.1 (Composition Dimensions) "Production mode", 16.4 (Logging), 12.3 (Postgres HA and Failover), 11.7 (Audit Logging)
// diagnosis: a failure means a gateway started with --environment=prod, the
// only carrier the chart renders, ran outside production mode because a
// production gate read a second spelling (such as LENNY_ENV=production)
// instead of the finalized --environment value. Every production-only gate
// is then off on a chart install: the §11.7 regulated-tenant SIEM and pgaudit
// refusals, the §12.3 ledger-integrity refusal, the lenny_env_production
// gauge, and the AuditBatchingNoSIEM warning. Check that each gate in
// cmd/lenny-gateway derives production through deployenv.IsProduction on
// the finalized KMS options' Environment.
func TestGatewayEnvironmentProdIsProductionMode_spec_17_9_1(t *testing.T) {
	for _, lennyEnv := range []string{"", "dev"} {
		t.Run("LENNY_ENV="+lennyEnv, func(t *testing.T) {
			gw := startProductionGateway(t, lennyEnv)
			body := gatewayMetricsBody(t, gw)
			if !strings.Contains(body, "\nlenny_env_production 1\n") {
				t.Errorf("/metrics does not report lenny_env_production 1 under --environment=prod\n%s",
					grepLines(body, "lenny_env_production"))
			}
			if logs := gw.Logs(); !strings.Contains(logs, gatewayAuditBatchingNoSIEMWarning) {
				t.Errorf("startup log lacks the §12.3 audit-batching-without-SIEM warning under --environment=prod\n%s", logs)
			}
		})
	}
}

// spec: 17.9.1 (Composition Dimensions) "Production mode"
// diagnosis: a failure means the gateway started with an --environment value
// outside the §17.9.1 closed set. The retired spelling production, or a case
// variant such as PROD, would then run outside production mode silently with
// every production refusal off. The providerflags finalizer validates the
// value through deployenv.Validate before any subsystem is built.
func TestGatewayRefusesOffSetEnvironment_spec_17_9_1(t *testing.T) {
	binary := gateway.Build(t)
	for _, env := range []string{"production", "PROD"} {
		t.Run(env, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary,
				"--addr", opsLoopbackAddr(t), "--no-environment-policy", "deny-all", "--environment="+env)
			cmd.Env = append(opsIsolatedEnv(), "LENNY_DEV_MODE=true", "LENNY_TLS_TERMINATED_UPSTREAM=true")
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("lenny-gateway still running after 60s; want a startup refusal\n%s", out)
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
				t.Fatalf("lenny-gateway exit = %v, want a non-zero exit\n%s", err, out)
			}
			// The gateway logs JSON, so the quotes around the value are escaped.
			want := `environment \"` + env + `\" is not one of local, dev, staging, or prod`
			if !strings.Contains(string(out), want) {
				t.Errorf("log does not name the validation error %q\n%s", want, out)
			}
		})
	}
}

// grepLines returns the lines of s that contain sub, for a short failure
// message.
func grepLines(s, sub string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
