// SPDX-License-Identifier: MIT

// Package adapterclient is the gateway-side client for a pod's §4.7
// runtime adapter. It wraps the generated adapterv1 gRPC client with
// connection lifecycle management and a session-oriented surface, so
// the gateway's session path drives a claimed pod without handling raw
// protobuf requests.
package adapterclient

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// Client is a gateway-side connection to one pod's adapter.
type Client struct {
	conn *grpc.ClientConn
	rpc  adapterv1.AdapterClient
}

// New wraps an established gRPC connection. The caller dials with the
// transport credentials appropriate to the deployment: mTLS against a
// pod's adapter in a cluster (§4.7), or insecure credentials for an
// in-process test.
func New(conn *grpc.ClientConn) *Client {
	return &Client{conn: conn, rpc: adapterv1.NewAdapterClient(conn)}
}

// Dial opens a gRPC connection to the adapter at target and wraps it.
// The caller supplies the transport-credential dial option.
func Dial(target string, opts ...grpc.DialOption) (*Client, error) {
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("adapterclient: dial %s: %w", target, err)
	}
	return New(conn), nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// NegotiateVersion performs the §4.7 / §15.5 handshake, advertising the
// protocol versions the gateway accepts.
func (c *Client) NegotiateVersion(ctx context.Context, acceptedVersions []string) (*adapterv1.NegotiateVersionResponse, error) {
	return c.rpc.NegotiateVersion(ctx, &adapterv1.NegotiateVersionRequest{
		AcceptedProtocolVersions: acceptedVersions,
	})
}

// StartSession assigns a session to the pod: the adapter materializes
// the workspace from plan, runs the setup commands, and starts the
// runtime (§4.7).
func (c *Client) StartSession(ctx context.Context, sessionID, runtimeName string, plan *adapterv1.WorkspacePlan) error {
	_, err := c.rpc.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId:     &adapterv1.SessionId{Value: sessionID},
		Runtime:       runtimeName,
		WorkspacePlan: plan,
	})
	return err
}

// SendMessage forwards a pre-encoded §15.4.1 message envelope to the
// pod's runtime.
func (c *Client) SendMessage(ctx context.Context, sessionID string, envelope []byte) error {
	_, err := c.rpc.SendMessage(ctx, &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: envelope,
	})
	return err
}

// Interrupt asks the pod's runtime to pause (§4.7). A hard interrupt
// sends SIGKILL; a clean interrupt sends SIGTERM and grants the runtime
// deadline to pause and checkpoint. The returned bool reports whether
// the adapter acknowledged the interrupt.
func (c *Client) Interrupt(ctx context.Context, sessionID string, hard bool, deadline time.Duration) (bool, error) {
	mode := adapterv1.InterruptRequest_MODE_CLEAN
	if hard {
		mode = adapterv1.InterruptRequest_MODE_HARD
	}
	resp, err := c.rpc.Interrupt(ctx, &adapterv1.InterruptRequest{
		SessionId:  &adapterv1.SessionId{Value: sessionID},
		Mode:       mode,
		DeadlineMs: int32(deadline.Milliseconds()),
	})
	if err != nil {
		return false, err
	}
	return resp.GetAcknowledged(), nil
}

// Shutdown terminates the pod's runtime and releases the session. The
// returned bool reports whether the runtime exited cleanly.
func (c *Client) Shutdown(ctx context.Context, sessionID string) (bool, error) {
	resp, err := c.rpc.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	})
	if err != nil {
		return false, err
	}
	return resp.GetExitedCleanly(), nil
}
