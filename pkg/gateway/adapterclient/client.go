// SPDX-License-Identifier: MIT

// Package adapterclient is the gateway-side client for a pod's §4.7
// runtime adapter. It wraps the generated adapterv1 gRPC client with
// connection lifecycle management and a session-oriented surface, so
// the gateway's session path drives a claimed pod without handling raw
// protobuf requests.
package adapterclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/credential"
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

// StartSessionParams carries the §15.4 adapter-manifest inputs the
// gateway delivers to a pod's runtime at session start. SessionID and
// Runtime are required; the rest populate the §4.7 manifest fields and may
// be zero.
type StartSessionParams struct {
	SessionID string
	Runtime   string
	// TaskID is the §4.7 manifest taskId. Empty in session mode, where the
	// adapter defaults it to the session id.
	TaskID string
	// ExperimentContext is the session's §8.3 / §10.7 experiment enrollment
	// (nil for an unenrolled session).
	ExperimentContext *adapterv1.ExperimentContext
	// TracingContext is the §8.3 opaque tracing identifiers (nil when none
	// is set).
	TracingContext map[string]string
	// AgentInterface is the runtime's §5.1 agentInterface descriptor as
	// JSON (nil when undeclared; the manifest field is then null).
	AgentInterface []byte
	// MinPlatformVersion is the runtime's §5.1 minPlatformVersion (empty
	// when none is specified).
	MinPlatformVersion string
}

// StartSession starts the runtime on a pod whose workspace is already
// materialized by FinalizeWorkspace and whose setup commands are already
// run by RunSetup (§4.7, the final session-assignment RPC). The params
// populate the §15.4 adapter manifest the runtime reads at startup.
func (c *Client) StartSession(ctx context.Context, p StartSessionParams) error {
	_, err := c.rpc.StartSession(ctx, &adapterv1.StartSessionRequest{
		SessionId:          &adapterv1.SessionId{Value: p.SessionID},
		Runtime:            p.Runtime,
		TaskId:             p.TaskID,
		ExperimentContext:  p.ExperimentContext,
		TracingContext:     p.TracingContext,
		AgentInterface:     p.AgentInterface,
		MinPlatformVersion: p.MinPlatformVersion,
	})
	return err
}

// AssignCredentials pushes the session's per-provider §4.9 credential
// leases to the pod's adapter ahead of StartSession (§4.7, the fourth
// session-assignment RPC). leases is keyed by provider; the adapter
// materializes them into the pod's credential file. The call replaces
// any previously assigned set. A nil or empty map assigns no
// credentials.
//
// The request carries credential material; per §4.7 item 6 the call
// site must keep it out of access logs and telemetry.
func (c *Client) AssignCredentials(ctx context.Context, sessionID string, leases map[string]*adapterv1.CredentialLease) error {
	_, err := c.rpc.AssignCredentials(ctx, &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Leases:    leases,
	})
	return err
}

// RotateCredentials replaces a session's previously assigned §4.9
// credential leases with rotated leases and pushes them to the pod's
// adapter (§4.7 `RotateCredentials` RPC). leases is keyed by provider;
// the adapter rewrites only the named providers' entries in the pod's
// credential file and retains the rest, then runs the §4.7
// `credentials_rotated` / `credentials_acknowledged` lifecycle
// handshake with the runtime so a Full-level runtime rebinds the
// rotated credential in place without a restart.
//
// It is the gateway-side driver for every §4.9 rotation path: the
// Fallback Flow steps 5–7, Emergency Credential Revocation step 5
// (direct-delivery mode), and the Proactive Lease Renewal loop's
// replacement-lease push. The §4.9 rotationTrigger that distinguishes
// those causes is internal gateway rotation context; the adapter wire
// contract carries only the session id and the rotated lease map.
//
// The request carries credential material; per §4.7 item 6 the call
// site must keep it out of access logs and telemetry. A nil or empty
// map rotates nothing.
func (c *Client) RotateCredentials(ctx context.Context, sessionID string, leases map[string]*adapterv1.CredentialLease) error {
	_, err := c.rpc.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Leases:    leases,
	})
	return err
}

// prepareWorkspaceChunkSize bounds each PrepareWorkspace upload frame.
const prepareWorkspaceChunkSize = 64 * 1024

// PrepareWorkspace streams uploaded workspace files into the pod's
// staging area (§4.7, the first session-assignment RPC). uploads maps
// each §14 WorkspaceSource upload_ref to its content; each upload is
// sent in frames bounded by prepareWorkspaceChunkSize. The response
// reports the staged byte and file totals the adapter persisted.
func (c *Client) PrepareWorkspace(ctx context.Context, sessionID string, uploads map[string][]byte) (*adapterv1.PrepareWorkspaceResponse, error) {
	stream, err := c.rpc.PrepareWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	sid := &adapterv1.SessionId{Value: sessionID}
	for ref, content := range uploads {
		if err := sendUpload(stream, sid, ref, content); err != nil {
			// io.EOF means the adapter closed the stream early with an
			// error; CloseAndRecv surfaces the real status. Any other
			// send error is a transport failure to return directly.
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}
	return stream.CloseAndRecv()
}

// sendUpload streams one upload as PrepareWorkspace frames. It always
// sends at least one frame so an empty upload still stages a file.
func sendUpload(stream adapterv1.Adapter_PrepareWorkspaceClient, sid *adapterv1.SessionId, ref string, content []byte) error {
	for off := 0; ; off += prepareWorkspaceChunkSize {
		end := off + prepareWorkspaceChunkSize
		if end > len(content) {
			end = len(content)
		}
		if err := stream.Send(&adapterv1.PrepareWorkspaceRequest{
			SessionId: sid,
			UploadRef: ref,
			Chunk:     content[off:end],
		}); err != nil {
			return err
		}
		if end >= len(content) {
			return nil
		}
	}
}

// FinalizeWorkspace materializes the §14 WorkspacePlan into the pod's
// workspace root (§4.7, the second session-assignment RPC). For a plan
// with uploadFile or uploadArchive sources, PrepareWorkspace must have
// staged their content first.
func (c *Client) FinalizeWorkspace(ctx context.Context, sessionID string, plan *adapterv1.WorkspacePlan) error {
	_, err := c.rpc.FinalizeWorkspace(ctx, &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: sessionID},
		WorkspacePlan: plan,
	})
	return err
}

// RunSetup executes the §14 WorkspacePlan setup commands in the pod's
// workspace (§4.7, the third session-assignment RPC). setupPolicy
// bounds the aggregate setup phase per §5.1; a nil policy applies no
// aggregate cap.
func (c *Client) RunSetup(ctx context.Context, sessionID string, setupCommands []*adapterv1.SetupCommand, setupPolicy *adapterv1.SetupPolicy) error {
	_, err := c.rpc.RunSetup(ctx, &adapterv1.RunSetupRequest{
		SessionId:     &adapterv1.SessionId{Value: sessionID},
		SetupCommands: setupCommands,
		SetupPolicy:   setupPolicy,
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

// InterruptStatus mirrors the §4.7 InterruptResponse.Status the adapter
// returns from the Interrupt RPC: STATUS_ACKNOWLEDGED when the runtime
// reached a safe stop point, STATUS_INTERRUPT_TIMEOUT when the deadline
// elapsed without an acknowledgement (§7.2 line 169: the gateway moves
// the session to suspended anyway), STATUS_BUSY when the adapter's per-
// session operation lock rejected the call so the gateway can retry, and
// STATUS_UNSPECIFIED for the zero value.
//
// spec: §4.7 InterruptResponse.Status.
type InterruptStatus int32

const (
	InterruptStatusUnspecified InterruptStatus = 0
	InterruptStatusAcknowledged InterruptStatus = 1
	InterruptStatusTimeout      InterruptStatus = 2
	InterruptStatusBusy         InterruptStatus = 3
)

// Interrupt asks the pod's runtime to pause (§4.7). A hard interrupt
// sends SIGKILL; a clean interrupt sends SIGTERM and grants the runtime
// deadline to pause and checkpoint. The returned status carries the
// §4.7 InterruptResponse.Status disposition so the caller can branch on
// ACKNOWLEDGED, INTERRUPT_TIMEOUT (still transitions to suspended per
// §7.2 line 169), or BUSY (retry).
func (c *Client) Interrupt(ctx context.Context, sessionID string, hard bool, deadline time.Duration) (InterruptStatus, error) {
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
		return InterruptStatusUnspecified, err
	}
	return InterruptStatus(resp.GetStatus()), nil
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

// ResumeParams carries the inputs to restore a session onto a replacement
// pod. SessionID, Runtime, and CheckpointID are required; the rest
// re-deliver the §15.4 adapter-manifest fields so the restored runtime
// reads the same manifest as before the resume.
type ResumeParams struct {
	SessionID          string
	Runtime            string
	CheckpointID       string
	TaskID             string
	ExperimentContext  *adapterv1.ExperimentContext
	TracingContext     map[string]string
	AgentInterface     []byte
	MinPlatformVersion string
}

// Resume asks the pod's adapter to restore the session workspace from the
// named §4.4 checkpoint and start the runtime (§4.7, §7.1) — the
// replacement-pod counterpart of StartSession. The params re-deliver the
// §15.4 manifest fields to the restored runtime. It returns the
// uncompressed workspace bytes restored.
func (c *Client) Resume(ctx context.Context, p ResumeParams) (int64, error) {
	resp, err := c.rpc.Resume(ctx, &adapterv1.ResumeRequest{
		SessionId:          &adapterv1.SessionId{Value: p.SessionID},
		Runtime:            p.Runtime,
		CheckpointId:       p.CheckpointID,
		TaskId:             p.TaskID,
		ExperimentContext:  p.ExperimentContext,
		TracingContext:     p.TracingContext,
		AgentInterface:     p.AgentInterface,
		MinPlatformVersion: p.MinPlatformVersion,
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
//
// spec: spec/04_system-components.md line 1468 — for sessions in proxy
// mode the gateway is the sole counter of record (the §4.9 LLM proxy
// extracts authoritative counts from the upstream response). The
// caller must filter proxy-mode sessions before reaching this RPC; the
// pod-reported counts are not accepted for them. ErrUsageReportProxyMode
// surfaces a misrouted proxy-mode call so a regression is observable
// rather than silently double-counting.
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

// ErrUsageReportProxyMode reports that ReportUsage was called for a
// session whose credential lease is proxy-mode. spec: §4.9 line 1468.
var ErrUsageReportProxyMode = errors.New("adapterclient: ReportUsage is not accepted for proxy-mode sessions (§4.9 line 1468)")

// ReportUsageForLease is the proxy-mode-safe wrapper around ReportUsage.
// It returns ErrUsageReportProxyMode when lease is the §4.9 proxy-mode
// lease for the session, so the caller does not double-count
// (proxy-extracted counts are already recorded by the §4.9 LLM proxy).
// A direct-mode lease, or an empty lease (the session has no §4.9
// credential pool), falls through to the underlying RPC.
//
// spec: spec/04_system-components.md line 1468.
func (c *Client) ReportUsageForLease(ctx context.Context, sessionID string, deliveryMode credential.DeliveryMode) (UsageReport, error) {
	if deliveryMode == credential.DeliveryProxy {
		return UsageReport{}, ErrUsageReportProxyMode
	}
	return c.ReportUsage(ctx, sessionID)
}

// AttachStream is a live §4.7 bidirectional content stream to a pod's
// adapter. Send forwards a client-to-agent envelope; Recv returns the
// next agent-to-gateway envelope.
type AttachStream struct {
	stream    grpc.BidiStreamingClient[adapterv1.AttachRequest, adapterv1.AttachResponse]
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
	if err := stream.Send(&adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}); err != nil {
		return nil, fmt.Errorf("adapterclient: bind attach stream: %w", err)
	}
	return &AttachStream{stream: stream, sessionID: sessionID}, nil
}

// Send forwards a §15.4.1 client-to-agent envelope to the agent.
func (a *AttachStream) Send(envelope []byte) error {
	return a.stream.Send(&adapterv1.AttachRequest{
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

// Terminate is the §4.7 `Terminate` RPC: it asks the pod's adapter to
// shut the runtime down gracefully, carrying the reason for the
// termination and a deadline for the graceful phase. The adapter sends
// SIGTERM to the agent, waits up to the deadline, then sends SIGKILL.
// reason is an opaque cause string surfaced to the adapter — the §11.4
// full_revoke fan-out passes `USER_REVOKED`. A zero deadline lets the
// adapter apply its default grace period. The returned bool reports
// whether the runtime exited cleanly.
//
// The §4.7 RPC table names this RPC `Terminate`; the wire contract in
// schemas/lenny-adapter.proto carries it as the `Shutdown` RPC, whose
// ShutdownRequest carries the reason and deadline fields the plain
// Shutdown helper above leaves unset.
func (c *Client) Terminate(ctx context.Context, sessionID, reason string, deadline time.Duration) (bool, error) {
	resp, err := c.rpc.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: sessionID},
		Reason:     reason,
		DeadlineMs: int32(deadline.Milliseconds()),
	})
	if err != nil {
		return false, err
	}
	return resp.GetExitedCleanly(), nil
}
