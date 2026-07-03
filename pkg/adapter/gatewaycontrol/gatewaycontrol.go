// SPDX-License-Identifier: MIT

// Package gatewaycontrol is the adapter-side client for the gateway's
// GatewayControl service. It is the inverse-direction counterpart of
// pkg/gateway/adapterclient: where adapterclient is the gateway dialing
// a pod's Adapter service, this package is a pod's adapter dialing the
// gateway's GatewayControl service.
//
// The service forwards a type:agent runtime's platform-tool and
// per-connector-tool calls to the gateway (platformtools.go,
// connectortools.go) and delivers §5.2 scrub reports (scrubreport.go).
// The §8.6 lease-extension trigger is not part of this surface: it is
// driven by the gateway LLM Proxy in-process (see spec §8.6), so no
// adapter round-trip requests an extension.
package gatewaycontrol

import (
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// GatewayDNSName is the §10.3 line 322 (NET-060) DNS SAN that every
// gateway replica's certificate carries — the Service DNS under which
// all gateway replicas are reachable. The adapter pins it as
// tls.Config.ServerName on the outbound GatewayControl dial (via
// adapter.WithServerName) so Go's standard verification chain rejects
// any cluster-CA-signed certificate whose SAN does not cover it, in
// particular certificates issued to the Token Service, controller, or
// any other lenny-system workload. The gateway dial MUST pin this name
// rather than fall back to CA-only trust (spec line 324).
// spec: §10.3 line 322 (NET-060)
const GatewayDNSName = "lenny-gateway.lenny-system.svc"

// Client is an adapter-side connection to the gateway's GatewayControl
// service.
type Client struct {
	conn *grpc.ClientConn
	rpc  adapterv1.GatewayControlClient
}

// New wraps an established gRPC connection to the gateway. The caller
// dials with the transport credentials appropriate to the deployment:
// the §4.7 mTLS link to the gateway in a cluster, or insecure
// credentials for an in-process test.
func New(conn *grpc.ClientConn) *Client {
	return &Client{conn: conn, rpc: adapterv1.NewGatewayControlClient(conn)}
}

// Dial opens a gRPC connection to the gateway's GatewayControl service
// at target and wraps it. The caller supplies the transport-credential
// dial option.
//
// spec: §16.3 line 328 ("Pod → Gateway (delegation tool calls carry parent
// trace context)") — the OTel client stats handler injects the adapter's
// current trace context into outgoing gRPC metadata so the gateway's
// GatewayControl spans join the pod's trace. F-16.3.3.
func Dial(target string, opts ...grpc.DialOption) (*Client, error) {
	opts = append([]grpc.DialOption{grpc.WithStatsHandler(otelgrpc.NewClientHandler())}, opts...)
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("gatewaycontrol: dial %s: %w", target, err)
	}
	return New(conn), nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
