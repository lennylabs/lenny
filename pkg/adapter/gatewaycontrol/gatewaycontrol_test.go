// SPDX-License-Identifier: MIT

package gatewaycontrol_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// stubGatewayControl is a fake GatewayControl gRPC server. It returns a
// canned response or error so the adapter client can be exercised
// without a real gateway.
type stubGatewayControl struct {
	adapterv1.UnimplementedGatewayControlServer

	// §9.1 platform tool forwarding stubs. F-9.1.1.
	listResp   *adapterv1.ListPlatformToolsResponse
	listErr    error
	callResp   *adapterv1.CallPlatformToolResponse
	callErr    error
	gotListReq *adapterv1.ListPlatformToolsRequest
	gotCallReq *adapterv1.CallPlatformToolRequest

	// §9.3 per-connector tool forwarding stubs. F-9.1.2.
	connListResp    *adapterv1.ListSessionConnectorsResponse
	connListErr     error
	connToolsResp   *adapterv1.ListConnectorToolsResponse
	connToolsErr    error
	connCallResp    *adapterv1.CallConnectorToolResponse
	connCallErr     error
	gotConnListReq  *adapterv1.ListSessionConnectorsRequest
	gotConnToolsReq *adapterv1.ListConnectorToolsRequest
	gotConnCallReq  *adapterv1.CallConnectorToolRequest

	// §5.2 scrub-report stubs.
	sessionScrubErr    error
	podScrubErr        error
	gotSessionScrubReq *adapterv1.ReportSessionScrubRequest
	gotPodScrubReq     *adapterv1.ReportPodScrubRequest
}

func (s *stubGatewayControl) ListPlatformTools(_ context.Context, req *adapterv1.ListPlatformToolsRequest) (*adapterv1.ListPlatformToolsResponse, error) {
	s.gotListReq = req
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listResp, nil
}

func (s *stubGatewayControl) CallPlatformTool(_ context.Context, req *adapterv1.CallPlatformToolRequest) (*adapterv1.CallPlatformToolResponse, error) {
	s.gotCallReq = req
	if s.callErr != nil {
		return nil, s.callErr
	}
	return s.callResp, nil
}

func (s *stubGatewayControl) ListSessionConnectors(_ context.Context, req *adapterv1.ListSessionConnectorsRequest) (*adapterv1.ListSessionConnectorsResponse, error) {
	s.gotConnListReq = req
	if s.connListErr != nil {
		return nil, s.connListErr
	}
	return s.connListResp, nil
}

func (s *stubGatewayControl) ListConnectorTools(_ context.Context, req *adapterv1.ListConnectorToolsRequest) (*adapterv1.ListConnectorToolsResponse, error) {
	s.gotConnToolsReq = req
	if s.connToolsErr != nil {
		return nil, s.connToolsErr
	}
	return s.connToolsResp, nil
}

func (s *stubGatewayControl) CallConnectorTool(_ context.Context, req *adapterv1.CallConnectorToolRequest) (*adapterv1.CallConnectorToolResponse, error) {
	s.gotConnCallReq = req
	if s.connCallErr != nil {
		return nil, s.connCallErr
	}
	return s.connCallResp, nil
}

func (s *stubGatewayControl) ReportSessionScrub(_ context.Context, req *adapterv1.ReportSessionScrubRequest) (*adapterv1.ReportSessionScrubResponse, error) {
	s.gotSessionScrubReq = req
	if s.sessionScrubErr != nil {
		return nil, s.sessionScrubErr
	}
	return &adapterv1.ReportSessionScrubResponse{}, nil
}

func (s *stubGatewayControl) ReportPodScrub(_ context.Context, req *adapterv1.ReportPodScrubRequest) (*adapterv1.ReportPodScrubResponse, error) {
	s.gotPodScrubReq = req
	if s.podScrubErr != nil {
		return nil, s.podScrubErr
	}
	return &adapterv1.ReportPodScrubResponse{}, nil
}

// dialStub boots stub on an in-memory bufconn server and returns a
// connected adapter-side GatewayControl client.
func dialStub(t *testing.T, stub *stubGatewayControl) *gatewaycontrol.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterGatewayControlServer(gs, stub)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return gatewaycontrol.New(conn)
}

// TestDialBadTarget: Dial surfaces a wrapped error for an unusable
// target.
func TestDialBadTarget(t *testing.T) {
	_, err := gatewaycontrol.Dial("\x00bad-target")
	if err == nil {
		t.Fatal("Dial should reject an unusable target")
	}
}

// TestGatewayDNSNameMatchesSpec pins the §10.3 line 322 (NET-060)
// gateway Service DNS SAN the adapter must pin as tls.Config.ServerName.
func TestGatewayDNSNameMatchesSpec_spec_10_3_322(t *testing.T) {
	if gatewaycontrol.GatewayDNSName != "lenny-gateway.lenny-system.svc" {
		t.Errorf("GatewayDNSName = %q, want the NET-060 gateway Service DNS SAN", gatewaycontrol.GatewayDNSName)
	}
}
