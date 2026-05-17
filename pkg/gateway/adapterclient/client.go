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
// runtime (§4.7). experimentContext carries the session's §8.3 / §10.7
// experiment enrollment for the adapter manifest (nil for an unenrolled
// session); tracingContext carries the §8.3 opaque tracing identifiers
// (nil when none is set); setupPolicy carries the §5.1 runtime
// setup-phase aggregate cap (nil for no cap).
func (c *Client) StartSession(ctx context.Context, sessionID, runtimeName string, plan *adapterv1.WorkspacePlan, experimentContext *adapterv1.ExperimentContext, tracingContext map[string]string, setupPolicy *adapterv1.SetupPolicy) error {
	_, err := c.rpc.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId:         &adapterv1.SessionId{Value: sessionID},
		Runtime:           runtimeName,
		WorkspacePlan:     plan,
		ExperimentContext: experimentContext,
		TracingContext:    tracingContext,
		SetupPolicy:       setupPolicy,
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

// CheckpointResult reports the §4.4 checkpoint a pod's adapter stored.
type CheckpointResult struct {
	// CheckpointID identifies the stored checkpoint.
	CheckpointID string
	// SizeBytes is the compressed size of the stored checkpoint.
	SizeBytes int64
}

// Checkpoint asks the pod's adapter to snapshot the session workspace
// and store it as a §4.4 checkpoint. deadline bounds the checkpoint; a
// zero deadline lets the adapter apply its default.
func (c *Client) Checkpoint(ctx context.Context, sessionID string, deadline time.Duration) (CheckpointResult, error) {
	resp, err := c.rpc.Checkpoint(ctx, &adapterv1.CheckpointRequest{
		SessionId:  &adapterv1.SessionId{Value: sessionID},
		DeadlineMs: int32(deadline.Milliseconds()),
	})
	if err != nil {
		return CheckpointResult{}, err
	}
	return CheckpointResult{
		CheckpointID: resp.GetCheckpointId(),
		SizeBytes:    resp.GetSizeBytes(),
	}, nil
}

// Resume asks the pod's adapter to restore the session workspace from
// the named §4.4 checkpoint and start the runtime (§4.7, §7.1) — the
// replacement-pod counterpart of StartSession. experimentContext and
// tracingContext are re-delivered to the restored runtime in the
// adapter manifest. It returns the uncompressed workspace bytes
// restored.
func (c *Client) Resume(ctx context.Context, sessionID, runtimeName, checkpointID string, experimentContext *adapterv1.ExperimentContext, tracingContext map[string]string) (int64, error) {
	resp, err := c.rpc.Resume(ctx, &adapterv1.ResumeRequest{
		SessionId:         &adapterv1.SessionId{Value: sessionID},
		Runtime:           runtimeName,
		CheckpointId:      checkpointID,
		ExperimentContext: experimentContext,
		TracingContext:    tracingContext,
	})
	if err != nil {
		return 0, err
	}
	return resp.GetRestoredBytes(), nil
}

// UsageReport is the §4.7 token and wall-clock accounting a pod's
// adapter returned for a session.
type UsageReport struct {
	// InputTokens is the prompt-token count.
	InputTokens int64
	// OutputTokens is the completion-token count.
	OutputTokens int64
	// WallClockMS is the elapsed runtime wall-clock time in
	// milliseconds.
	WallClockMS int64
}

// ReportUsage pulls the session's token and time accounting from the
// pod's adapter (§4.7). The accounting is incremental — each call
// returns usage accumulated since the previous one — so the gateway
// sums it for §11.2 budget enforcement and billing.
func (c *Client) ReportUsage(ctx context.Context, sessionID string) (UsageReport, error) {
	resp, err := c.rpc.ReportUsage(ctx, &adapterv1.ReportUsageRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	})
	if err != nil {
		return UsageReport{}, err
	}
	return UsageReport{
		InputTokens:  resp.GetInputTokens(),
		OutputTokens: resp.GetOutputTokens(),
		WallClockMS:  resp.GetWallClockMs(),
	}, nil
}

// AttachStream is a live §4.7 bidirectional content stream to a pod's
// adapter. Send forwards a client-to-agent envelope; Recv returns the
// next agent-to-gateway envelope.
type AttachStream struct {
	stream    grpc.BidiStreamingClient[adapterv1.AttachClientMessage, adapterv1.AttachServerMessage]
	sessionID string
}

// Attach opens the §4.7 content stream for sessionID and binds it with
// an envelope-free first message, so the returned stream is ready to
// carry content. The caller closes the stream by cancelling ctx.
func (c *Client) Attach(ctx context.Context, sessionID string) (*AttachStream, error) {
	stream, err := c.rpc.Attach(ctx)
	if err != nil {
		return nil, fmt.Errorf("adapterclient: open attach stream: %w", err)
	}
	if err := stream.Send(&adapterv1.AttachClientMessage{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}); err != nil {
		return nil, fmt.Errorf("adapterclient: bind attach stream: %w", err)
	}
	return &AttachStream{stream: stream, sessionID: sessionID}, nil
}

// Send forwards a §15.4.1 client-to-agent envelope to the agent.
func (a *AttachStream) Send(envelope []byte) error {
	return a.stream.Send(&adapterv1.AttachClientMessage{
		SessionId:    &adapterv1.SessionId{Value: a.sessionID},
		EnvelopeJson: envelope,
	})
}

// Recv returns the next §15.4.1 agent-to-gateway envelope. It returns
// io.EOF once the runtime's output stream ends.
func (a *AttachStream) Recv() ([]byte, error) {
	msg, err := a.stream.Recv()
	if err != nil {
		return nil, err
	}
	return msg.GetEnvelopeJson(), nil
}

// CloseSend signals that the gateway will send no further client
// envelopes; the adapter keeps streaming runtime output until it ends.
func (a *AttachStream) CloseSend() error {
	return a.stream.CloseSend()
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
