// SPDX-License-Identifier: MIT

package adapter_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// spec: §9.1 lines 8-31 — with no gateway address ConnectGateway binds
// the platform MCP socket but leaves the forwarders nil (the dev path
// with no gateway link), and returns a working no-op closer the caller
// can defer unconditionally. F-9.1.1.
func TestConnectGatewayNoAddrBindsSocketOnly_spec_9_1(t *testing.T) {
	s := adapter.New("gatewaylink-test")
	closer, err := s.ConnectGateway("@lenny-platform-mcp", "", "", "", "")
	if err != nil {
		t.Fatalf("ConnectGateway: %v", err)
	}
	if s.MCPSocket != "@lenny-platform-mcp" {
		t.Errorf("MCPSocket = %q, want the bound platform MCP socket", s.MCPSocket)
	}
	if s.PlatformForwarder != nil {
		t.Errorf("PlatformForwarder set without a gateway address: %#v", s.PlatformForwarder)
	}
	if s.ConnectorForwarder != nil {
		t.Errorf("ConnectorForwarder set without a gateway address: %#v", s.ConnectorForwarder)
	}
	if closer == nil {
		t.Fatal("ConnectGateway returned a nil closer; the caller cannot defer Close")
	}
	if err := closer.Close(); err != nil {
		t.Errorf("no-op closer Close: %v", err)
	}
}

// spec: §9.1 lines 14-31 — with a gateway address ConnectGateway dials
// the gateway's GatewayControl service and sets both forwarders to the
// gateway client, and the returned closer releases that client. The dial
// is non-blocking (grpc.NewClient), so this exercises the wiring and the
// closer contract without a live gateway. F-9.1.1 / F-9.1.2.
func TestConnectGatewayWithAddrWiresForwarders_spec_9_1(t *testing.T) {
	s := adapter.New("gatewaylink-test")
	// Empty cert/key/ca selects the plaintext dev dial path, so no live
	// gateway certificate is required to build the client.
	closer, err := s.ConnectGateway("@lenny-platform-mcp", "lenny-gateway.lenny-system.svc:50051", "", "", "")
	if err != nil {
		t.Fatalf("ConnectGateway: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	if s.MCPSocket != "@lenny-platform-mcp" {
		t.Errorf("MCPSocket = %q, want the bound platform MCP socket", s.MCPSocket)
	}
	if s.PlatformForwarder == nil {
		t.Error("PlatformForwarder nil after dialing the gateway")
	}
	if s.ConnectorForwarder == nil {
		t.Error("ConnectorForwarder nil after dialing the gateway")
	}
	// The §9.1/§9.3 forwarders are the same gateway-control client, so the
	// connector forwarder forwards over the channel the platform forwarder
	// dialed.
	if any(s.PlatformForwarder) != any(s.ConnectorForwarder) {
		t.Error("platform and connector forwarders are not the same gateway client")
	}
	if closer == nil {
		t.Fatal("ConnectGateway returned a nil closer; the caller cannot defer Close")
	}
}
