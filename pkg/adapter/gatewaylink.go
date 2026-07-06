// SPDX-License-Identifier: MIT

package adapter

import (
	"fmt"
	"io"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
)

// noopCloser is the io.Closer ConnectGateway returns when no gateway
// address is configured: there is no gateway client to release, so Close
// is a no-op. A concrete type keeps the returned value non-nil so every
// caller can defer Close unconditionally.
type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// ConnectGateway wires the §9.1 platform MCP socket and the §9.1/§8.6
// GatewayControl forwarders onto the Server. It binds mcpSocket as the
// abstract socket the platform MCP server listens on (the
// @lenny-platform-mcp socket the manifest advertises). When gatewayAddr
// is non-empty it dials the gateway's GatewayControl service over the
// §4.7/§10.3 mTLS link and sets the platform and connector forwarders so
// a type:agent runtime's platform and per-connector tool calls reach the
// gateway. An empty gatewayAddr leaves the platform MCP server serving an
// empty catalog (the dev path with no gateway link). The returned
// io.Closer is the gateway client; close it on shutdown.
//
// The dial reuses the adapter's mesh identity (its server cert/key) as
// the client certificate and pins the gateway's DNS SAN (§10.3 NET-060).
// In the embedded model the runtime process is the adapter, so the same
// wiring applies to a first-party runtime that links this package.
// spec: §9.1 lines 14-31. F-9.1.1.
func (s *Server) ConnectGateway(mcpSocket, gatewayAddr, certFile, keyFile, clientCAFile string) (io.Closer, error) {
	// §9.1 lines 8-31: the platform MCP server binds the abstract socket
	// the manifest advertises (@lenny-platform-mcp) so a type:agent runtime
	// can dial it. F-9.1.1.
	s.MCPSocket = mcpSocket
	if gatewayAddr == "" {
		return noopCloser{}, nil
	}
	// §9.1 lines 14-31: dial the gateway's GatewayControl service so the
	// platform MCP server can forward a runtime's tools/list and tools/call
	// to the gateway platform tool surface. The dial reuses the adapter's
	// mesh identity (its server cert/key) as the client certificate and
	// pins the gateway's DNS SAN (§10.3 NET-060). F-9.1.1.
	dialOpt, err := TLSClientOption(certFile, keyFile, clientCAFile,
		WithServerName(gatewaycontrol.GatewayDNSName))
	if err != nil {
		return nil, fmt.Errorf("§9.1 gateway dial TLS: %w", err)
	}
	gwClient, err := gatewaycontrol.Dial(gatewayAddr, dialOpt)
	if err != nil {
		return nil, fmt.Errorf("§9.1 dial gateway %s: %w", gatewayAddr, err)
	}
	s.PlatformForwarder = gwClient
	// §9.3 lines 142-164 — the same gateway-control channel forwards a
	// type:agent runtime's per-connector tool calls (against the intra-pod
	// @lenny-connector-<id> sockets) to the gateway, which dials the external
	// endpoint with the gateway-held credential. F-9.1.2.
	s.ConnectorForwarder = gwClient
	// §5.2 whole-pod scrub — the same gateway-control channel carries the
	// recycle-boundary ReportPodScrub the adapter emits after running the
	// whole-pod scrub. Retain the dialed client as the PodScrubReporter so the
	// recycle-scrub driver can report the outcome. spec: §4.7 (ReportPodScrub);
	// §5.2. F-5.2.15.
	s.PodScrubReporter = gwClient
	// §5.2 per-slot cleanup — the same gateway-control channel carries the
	// per-session-release ReportSessionScrub the adapter emits on every slot
	// release, so the gateway advances sessions_served (feeding the
	// maxSessionsPerPod retirement) and feeds a leaked outcome into the
	// unhealthy-threshold ledger. Retain the dialed client as the
	// SessionScrubReporter so the slot-release path can report the outcome.
	// spec: §4.7 (ReportSessionScrub); §5.2 (maxSessionsPerPod). F-5.2.31.
	s.SessionScrubReporter = gwClient
	return gwClient, nil
}
