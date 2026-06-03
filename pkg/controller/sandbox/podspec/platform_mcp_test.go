// SPDX-License-Identifier: MIT

package podspec_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

// spec: §9.1 lines 14-31 — when the controller is configured with the
// gateway GatewayControl address, the adapter container binds the
// platform MCP socket and forwards a type:agent runtime's platform tool
// calls to that gateway. F-9.1.1.
func TestBuildWiresPlatformMCPWhenGatewayConfigured_spec_9_1(t *testing.T) {
	in := inputs()
	in.GatewayGRPCAddr = "lenny-gateway.lenny-system.svc:50051"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	args := container(t, pod, "adapter").Args
	if !hasArg(args, "--mcp-socket="+podspec.PlatformMCPSocketName) {
		t.Errorf("adapter args missing --mcp-socket; got %v", args)
	}
	if !hasArg(args, "--gateway-grpc-addr=lenny-gateway.lenny-system.svc:50051") {
		t.Errorf("adapter args missing --gateway-grpc-addr; got %v", args)
	}
}

// Without a configured gateway address the platform MCP server is not
// started (no gateway link to forward to), so neither flag is rendered.
func TestBuildOmitsPlatformMCPWhenGatewayUnset_spec_9_1(t *testing.T) {
	pod, err := podspec.Build(inputs()) // inputs() leaves GatewayGRPCAddr empty.
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, a := range container(t, pod, "adapter").Args {
		if strings.HasPrefix(a, "--mcp-socket") || strings.HasPrefix(a, "--gateway-grpc-addr") {
			t.Errorf("adapter arg %q rendered without a configured gateway address", a)
		}
	}
}
