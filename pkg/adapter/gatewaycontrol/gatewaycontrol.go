// SPDX-License-Identifier: MIT

// Package gatewaycontrol is the adapter-side client for the gateway's
// §8.6 GatewayControl service. It is the inverse-direction counterpart
// of pkg/gateway/adapterclient: where adapterclient is the gateway
// dialing a pod's Adapter service, this package is a pod's adapter
// dialing the gateway's GatewayControl service.
//
// The only GatewayControl RPC is ExtendLease. Per spec §8.6 the
// adapter calls it when its LLM proxy rejects a request for budget
// exhaustion, then retries the rejected LLM call on GRANTED or
// PARTIALLY_GRANTED. The §8.6 budget reasoning lives entirely on the
// gateway; the adapter only forwards the request and acts on the
// status.
package gatewaycontrol

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

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
func Dial(target string, opts ...grpc.DialOption) (*Client, error) {
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

// ExtensionStatus is the §8.6 outcome of an ExtendLease request, lifted
// from the proto enum into a value the adapter's retry logic switches
// on.
type ExtensionStatus int

const (
	// StatusUnspecified is the zero value; an ExtendLease response
	// should never carry it.
	StatusUnspecified ExtensionStatus = iota
	// StatusGranted — the full requested budget was granted. The
	// adapter retries the rejected LLM call.
	StatusGranted
	// StatusPartiallyGranted — a non-zero amount below the request was
	// granted. The adapter still retries the rejected LLM call.
	StatusPartiallyGranted
	// StatusCeilingReached — zero grant, the §8.6 ceiling is reached.
	// The adapter MUST NOT retry the extension and propagates
	// BUDGET_EXHAUSTED to the runtime.
	StatusCeilingReached
	// StatusRejected — the user denied the extension. The adapter MUST
	// NOT retry and propagates BUDGET_EXHAUSTED to the runtime.
	StatusRejected
)

// ShouldRetryLLMCall reports whether the §8.6 status means the adapter
// should transparently retry the LLM call that the proxy rejected for
// budget exhaustion. It is true for GRANTED and PARTIALLY_GRANTED.
func (s ExtensionStatus) ShouldRetryLLMCall() bool {
	return s == StatusGranted || s == StatusPartiallyGranted
}

// Terminal reports whether the §8.6 status is a terminal outcome the
// adapter MUST NOT retry — CEILING_REACHED or REJECTED. On a terminal
// status the adapter propagates BUDGET_EXHAUSTED to the runtime.
func (s ExtensionStatus) Terminal() bool {
	return s == StatusCeilingReached || s == StatusRejected
}

func (s ExtensionStatus) String() string {
	switch s {
	case StatusGranted:
		return "GRANTED"
	case StatusPartiallyGranted:
		return "PARTIALLY_GRANTED"
	case StatusCeilingReached:
		return "CEILING_REACHED"
	case StatusRejected:
		return "REJECTED"
	default:
		return "UNSPECIFIED"
	}
}

// ExtensionResult is the §8.6 ExtendLease outcome the adapter acts on.
type ExtensionResult struct {
	// Status is the §8.6 extension status.
	Status ExtensionStatus
	// GrantedTokens is the additional token budget the gateway
	// granted. Zero on CEILING_REACHED and REJECTED.
	GrantedTokens int64
	// CoolOffExpiryUnixMs is the rejection cool-off expiry in Unix
	// milliseconds, set when Status is REJECTED. Zero otherwise.
	CoolOffExpiryUnixMs int64
}

// ErrUnknownStatus — the gateway returned a §8.6 status the adapter
// does not recognise. The adapter treats it as terminal (no retry) to
// avoid a budget-exhaustion retry loop.
var ErrUnknownStatus = errors.New("gatewaycontrol: gateway returned an unrecognised ExtendLease status")

// ExtendLease issues the §8.6 extension request to the gateway. The
// adapter calls it when its LLM proxy rejects a request for budget
// exhaustion; sessionID is the session whose lease is being extended
// and requestedTokens is the additional token budget asked for.
//
// On GRANTED or PARTIALLY_GRANTED the caller retries the rejected LLM
// call. On CEILING_REACHED or REJECTED the caller MUST NOT retry the
// extension and propagates BUDGET_EXHAUSTED to the runtime.
func (c *Client) ExtendLease(ctx context.Context, sessionID string, requestedTokens int64) (ExtensionResult, error) {
	resp, err := c.rpc.ExtendLease(ctx, &adapterv1.ExtendLeaseRequest{
		SessionId:       &adapterv1.SessionId{Value: sessionID},
		RequestedTokens: requestedTokens,
	})
	if err != nil {
		return ExtensionResult{}, fmt.Errorf("gatewaycontrol: ExtendLease for session %s: %w", sessionID, err)
	}
	status, ok := statusFromProto(resp.GetStatus())
	if !ok {
		return ExtensionResult{}, ErrUnknownStatus
	}
	return ExtensionResult{
		Status:              status,
		GrantedTokens:       resp.GetGrantedTokens(),
		CoolOffExpiryUnixMs: resp.GetCoolOffExpiryUnixMs(),
	}, nil
}

// statusFromProto maps the proto §8.6 status enum to ExtensionStatus.
// It returns ok=false for the unspecified zero value or an unknown
// code, so the caller treats an unrecognised status as terminal.
func statusFromProto(s adapterv1.ExtendLeaseResponse_Status) (ExtensionStatus, bool) {
	switch s {
	case adapterv1.ExtendLeaseResponse_STATUS_GRANTED:
		return StatusGranted, true
	case adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED:
		return StatusPartiallyGranted, true
	case adapterv1.ExtendLeaseResponse_STATUS_CEILING_REACHED:
		return StatusCeilingReached, true
	case adapterv1.ExtendLeaseResponse_STATUS_REJECTED:
		return StatusRejected, true
	default:
		return StatusUnspecified, false
	}
}
