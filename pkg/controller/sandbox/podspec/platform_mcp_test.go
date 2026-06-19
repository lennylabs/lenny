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

// spec: §9.1 lines 14-31 — in the §4.7 embedded model the runtime process
// is the adapter, so the platform MCP flags render onto the single
// `runtime` container when the controller is configured with the gateway
// address. The embedded main accepts the same flags as the sidecar
// adapter, so a §4.7 embedded pool serves the platform tool surface.
// F-9.1.1.
func TestBuildEmbeddedWiresPlatformMCPWhenGatewayConfigured_spec_9_1(t *testing.T) {
	in := inputs()
	in.DeploymentModel = "embedded"
	in.GatewayGRPCAddr = "lenny-gateway.lenny-system.svc:50051"
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	args := container(t, pod, "runtime").Args
	if !hasArg(args, "--mcp-socket="+podspec.PlatformMCPSocketName) {
		t.Errorf("embedded runtime args missing --mcp-socket; got %v", args)
	}
	if !hasArg(args, "--gateway-grpc-addr=lenny-gateway.lenny-system.svc:50051") {
		t.Errorf("embedded runtime args missing --gateway-grpc-addr; got %v", args)
	}
}

// Without a configured gateway address the embedded runtime container
// carries neither platform MCP flag, matching the sidecar behavior.
func TestBuildEmbeddedOmitsPlatformMCPWhenGatewayUnset_spec_9_1(t *testing.T) {
	in := inputs()
	in.DeploymentModel = "embedded" // GatewayGRPCAddr left empty.
	pod, err := podspec.Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, a := range container(t, pod, "runtime").Args {
		if strings.HasPrefix(a, "--mcp-socket") || strings.HasPrefix(a, "--gateway-grpc-addr") {
			t.Errorf("embedded runtime arg %q rendered without a configured gateway address", a)
		}
	}
}
