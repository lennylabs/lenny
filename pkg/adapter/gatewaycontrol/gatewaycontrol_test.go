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
}

func (s *stubGatewayControl) ExtendLease(_ context.Context, req *adapterv1.ExtendLeaseRequest) (*adapterv1.ExtendLeaseResponse, error) {
	s.gotReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
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

	res, err := client.ExtendLease(context.Background(), "sess-1", 200_000)
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

	res, err := client.ExtendLease(context.Background(), "sess-1", 200_000)
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

	res, err := client.ExtendLease(context.Background(), "sess-1", 200_000)
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

	res, err := client.ExtendLease(context.Background(), "sess-1", 200_000)
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

	_, err := client.ExtendLease(context.Background(), "sess-1", 200_000)
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

	_, err := client.ExtendLease(context.Background(), "sess-1", 200_000)
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
