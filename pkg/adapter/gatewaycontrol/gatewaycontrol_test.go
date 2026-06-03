// SPDX-License-Identifier: MIT

package gatewaycontrol_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// stubGatewayControl is a fake GatewayControl gRPC server. It returns a
// canned response or error so the adapter client can be exercised
// without a real gateway.
type stubGatewayControl struct {
	adapterv1.UnimplementedGatewayControlServer

	resp *adapterv1.ExtendLeaseResponse
	err  error

	// gotReq captures the last request the server received.
	gotReq *adapterv1.ExtendLeaseRequest

	// §9.1 platform tool forwarding stubs. F-9.1.1.
	listResp   *adapterv1.ListPlatformToolsResponse
	listErr    error
	callResp   *adapterv1.CallPlatformToolResponse
	callErr    error
	gotListReq *adapterv1.ListPlatformToolsRequest
	gotCallReq *adapterv1.CallPlatformToolRequest
}

func (s *stubGatewayControl) ExtendLease(_ context.Context, req *adapterv1.ExtendLeaseRequest) (*adapterv1.ExtendLeaseResponse, error) {
	s.gotReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
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

// TestExtendLeaseGranted: a GRANTED response surfaces the granted
// amount and asks the adapter to retry the LLM call.
func TestExtendLeaseGranted(t *testing.T) {
	stub := &stubGatewayControl{resp: &adapterv1.ExtendLeaseResponse{
		Status:        adapterv1.ExtendLeaseResponse_STATUS_GRANTED,
		GrantedTokens: 200_000,
	}}
	client := dialStub(t, stub)

	res, err := client.ExtendLease(context.Background(), "sess-1", gatewaycontrol.Extension{AdditionalTokens: 200_000})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if res.Status != gatewaycontrol.StatusGranted {
		t.Errorf("status = %v, want StatusGranted", res.Status)
	}
	if res.GrantedTokens != 200_000 {
		t.Errorf("granted = %d, want 200000", res.GrantedTokens)
	}
	if !res.Status.ShouldRetryLLMCall() {
		t.Error("GRANTED should ask the adapter to retry the LLM call")
	}
	if res.Status.Terminal() {
		t.Error("GRANTED is not terminal")
	}
	// The request carried the session id and requested amount.
	if stub.gotReq.GetSessionId().GetValue() != "sess-1" {
		t.Errorf("request session id = %q, want sess-1", stub.gotReq.GetSessionId().GetValue())
	}
	if stub.gotReq.GetRequestedTokens() != 200_000 {
		t.Errorf("request tokens = %d, want 200000", stub.gotReq.GetRequestedTokens())
	}
}

// TestExtendLeasePartiallyGranted: a PARTIALLY_GRANTED response still
// asks the adapter to retry the LLM call.
func TestExtendLeasePartiallyGranted(t *testing.T) {
	stub := &stubGatewayControl{resp: &adapterv1.ExtendLeaseResponse{
		Status:        adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED,
		GrantedTokens: 50_000,
	}}
	client := dialStub(t, stub)

	res, err := client.ExtendLease(context.Background(), "sess-1", gatewaycontrol.Extension{AdditionalTokens: 200_000})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if res.Status != gatewaycontrol.StatusPartiallyGranted {
		t.Errorf("status = %v, want StatusPartiallyGranted", res.Status)
	}
	if !res.Status.ShouldRetryLLMCall() {
		t.Error("PARTIALLY_GRANTED should ask the adapter to retry the LLM call")
	}
	if res.Status.Terminal() {
		t.Error("PARTIALLY_GRANTED is not terminal")
	}
}

// TestExtendLeaseCeilingReached: a CEILING_REACHED response is terminal
// — the adapter must not retry.
func TestExtendLeaseCeilingReached(t *testing.T) {
	stub := &stubGatewayControl{resp: &adapterv1.ExtendLeaseResponse{
		Status: adapterv1.ExtendLeaseResponse_STATUS_CEILING_REACHED,
	}}
	client := dialStub(t, stub)

	res, err := client.ExtendLease(context.Background(), "sess-1", gatewaycontrol.Extension{AdditionalTokens: 200_000})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if res.Status != gatewaycontrol.StatusCeilingReached {
		t.Errorf("status = %v, want StatusCeilingReached", res.Status)
	}
	if res.Status.ShouldRetryLLMCall() {
		t.Error("CEILING_REACHED must NOT ask the adapter to retry the LLM call")
	}
	if !res.Status.Terminal() {
		t.Error("CEILING_REACHED is terminal")
	}
}

// TestExtendLeaseRejected: a REJECTED response is terminal and carries
// the cool-off expiry.
func TestExtendLeaseRejected(t *testing.T) {
	stub := &stubGatewayControl{resp: &adapterv1.ExtendLeaseResponse{
		Status:              adapterv1.ExtendLeaseResponse_STATUS_REJECTED,
		CoolOffExpiryUnixMs: 1_799_999_999_000,
	}}
	client := dialStub(t, stub)

	res, err := client.ExtendLease(context.Background(), "sess-1", gatewaycontrol.Extension{AdditionalTokens: 200_000})
	if err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if res.Status != gatewaycontrol.StatusRejected {
		t.Errorf("status = %v, want StatusRejected", res.Status)
	}
	if res.Status.ShouldRetryLLMCall() {
		t.Error("REJECTED must NOT ask the adapter to retry the LLM call")
	}
	if !res.Status.Terminal() {
		t.Error("REJECTED is terminal")
	}
	if res.CoolOffExpiryUnixMs != 1_799_999_999_000 {
		t.Errorf("cool-off expiry = %d, want 1799999999000", res.CoolOffExpiryUnixMs)
	}
}

// TestExtendLeaseTransportError: a gRPC error from the gateway surfaces
// as an error rather than a grant.
func TestExtendLeaseTransportError(t *testing.T) {
	stub := &stubGatewayControl{err: status.Error(codes.Unavailable, "gateway down")}
	client := dialStub(t, stub)

	_, err := client.ExtendLease(context.Background(), "sess-1", gatewaycontrol.Extension{AdditionalTokens: 200_000})
	if err == nil {
		t.Fatal("ExtendLease should return the gateway error")
	}
	if status.Code(err) != codes.Unavailable {
		t.Errorf("error code = %v, want Unavailable", status.Code(err))
	}
}

// TestExtendLeaseUnknownStatus: an unrecognised status from the gateway
// returns ErrUnknownStatus so the adapter does not loop retrying.
func TestExtendLeaseUnknownStatus(t *testing.T) {
	stub := &stubGatewayControl{resp: &adapterv1.ExtendLeaseResponse{
		Status: adapterv1.ExtendLeaseResponse_STATUS_UNSPECIFIED,
	}}
	client := dialStub(t, stub)

	_, err := client.ExtendLease(context.Background(), "sess-1", gatewaycontrol.Extension{AdditionalTokens: 200_000})
	if !errors.Is(err, gatewaycontrol.ErrUnknownStatus) {
		t.Errorf("error = %v, want ErrUnknownStatus", err)
	}
}

// TestExtensionStatusString confirms each §8.6 status renders its
// canonical name.
func TestExtensionStatusString(t *testing.T) {
	cases := map[gatewaycontrol.ExtensionStatus]string{
		gatewaycontrol.StatusGranted:          "GRANTED",
		gatewaycontrol.StatusPartiallyGranted: "PARTIALLY_GRANTED",
		gatewaycontrol.StatusCeilingReached:   "CEILING_REACHED",
		gatewaycontrol.StatusRejected:         "REJECTED",
		gatewaycontrol.StatusUnspecified:      "UNSPECIFIED",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("status %d String() = %q, want %q", s, got, want)
		}
	}
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

// TestExtendLeaseCarriesAllDimensions confirms the §8.6 extensions
// block is wire-complete: every extendable dimension the Extension
// struct exposes lands on the ExtendLeaseRequest, including the
// FileExportLimitsDelta sub-message. spec: §8.6 lines 633-643; F-15.3.4.
func TestExtendLeaseCarriesAllDimensions_spec_8_6(t *testing.T) {
	stub := &stubGatewayControl{resp: &adapterv1.ExtendLeaseResponse{
		Status: adapterv1.ExtendLeaseResponse_STATUS_GRANTED,
	}}
	client := dialStub(t, stub)

	ext := gatewaycontrol.Extension{
		AdditionalTokens:           200_000,
		AdditionalSeconds:          1800,
		AdditionalChildren:         5,
		AdditionalParallelChildren: 2,
		AdditionalTreeSize:         10,
		AdditionalMaxFiles:         3,
		AdditionalMaxBytes:         4096,
	}
	if _, err := client.ExtendLease(context.Background(), "sess-1", ext); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	got := stub.gotReq
	if got.GetRequestedTokens() != 200_000 {
		t.Errorf("RequestedTokens = %d, want 200000", got.GetRequestedTokens())
	}
	if got.GetRequestedSeconds() != 1800 {
		t.Errorf("RequestedSeconds = %d, want 1800", got.GetRequestedSeconds())
	}
	if got.GetRequestedChildren() != 5 {
		t.Errorf("RequestedChildren = %d, want 5", got.GetRequestedChildren())
	}
	if got.GetRequestedParallelChildren() != 2 {
		t.Errorf("RequestedParallelChildren = %d, want 2", got.GetRequestedParallelChildren())
	}
	if got.GetRequestedTreeSize() != 10 {
		t.Errorf("RequestedTreeSize = %d, want 10", got.GetRequestedTreeSize())
	}
	fe := got.GetRequestedFileExportLimits()
	if fe == nil || fe.GetAdditionalMaxFiles() != 3 || fe.GetAdditionalMaxBytes() != 4096 {
		t.Errorf("RequestedFileExportLimits = %+v, want {3, 4096}", fe)
	}
}

// TestExtendLeaseOmitsFileExportDeltaWhenZero confirms the optional
// FileExportLimitsDelta sub-message is absent when neither file-export
// dimension is requested, so a zero extension does not send an empty
// delta. spec: §8.6 line 643.
func TestExtendLeaseOmitsFileExportDeltaWhenZero(t *testing.T) {
	stub := &stubGatewayControl{resp: &adapterv1.ExtendLeaseResponse{
		Status: adapterv1.ExtendLeaseResponse_STATUS_GRANTED,
	}}
	client := dialStub(t, stub)
	if _, err := client.ExtendLease(context.Background(), "sess-1",
		gatewaycontrol.Extension{AdditionalTokens: 1}); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
	if stub.gotReq.GetRequestedFileExportLimits() != nil {
		t.Errorf("RequestedFileExportLimits = %+v, want nil for a tokens-only extension",
			stub.gotReq.GetRequestedFileExportLimits())
	}
}
