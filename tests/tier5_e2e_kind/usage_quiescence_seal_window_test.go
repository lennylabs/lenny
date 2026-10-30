// SPDX-License-Identifier: MIT

//go:build e2e_kind

package tier5_e2e_kind_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 8.3 (Usage quiescence before budget return), 7.1 (Terminal pipeline
// context), 11.3
// diagnosis: the e2e chart install does not run a gateway whose seal window
// exceeds the usage quiescence timeout. Either the rendered gateway
// Deployment does not carry --workspace-seal-max-duration-seconds=6 from
// tests/testinfra/kind/e2e-values.yaml, or the gateway did not become Ready,
// which with that flag present means the startup check that refuses a
// usage quiescence timeout at or above the seal window rejected the
// configuration (for example the overlay was lowered back to 5 against the
// 5 s default timeout).
func TestGatewayStartsWithTheRaisedSealWindow_spec_8_3(t *testing.T) {
	c := kind.InstallLenny(t)
	args, err := c.KubectlOut(t, "-n", t5SystemNS, "get", "deployment", "lenny-gateway",
		"-o", "jsonpath={.spec.template.spec.containers[*].args}")
	if err != nil {
		t.Fatalf("read the gateway Deployment args: %v", err)
	}
	if !strings.Contains(args, "--workspace-seal-max-duration-seconds=6") {
		t.Fatalf("gateway args %s do not carry --workspace-seal-max-duration-seconds=6", args)
	}
	if strings.Contains(args, "--delegation-usage-quiescence-timeout-seconds=") &&
		!strings.Contains(args, "--delegation-usage-quiescence-timeout-seconds=5") {
		t.Fatalf("gateway args %s override the usage quiescence timeout; this test assumes the 5 s default", args)
	}
	if !t5DeploymentReady(t, c, "lenny-gateway") {
		t.Fatal("lenny-gateway is not Ready with a 6 s seal window and the 5 s default usage quiescence timeout")
	}
}
