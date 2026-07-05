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

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

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
//
// spec: §16.3 line 327 ("Gateway → Pod (gRPC metadata)") — the OTel client
// stats handler injects the current trace context into outgoing gRPC
// metadata so the pod adapter's spans become children of the gateway span
// that issued the call. F-16.3.3.
func Dial(target string, opts ...grpc.DialOption) (*Client, error) {
	opts = append([]grpc.DialOption{grpc.WithStatsHandler(otelgrpc.NewClientHandler())}, opts...)
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

// GetObservedIntegrationLevel asks the adapter for the §5.1 / §15.4.3
// integration level it observed the runtime implement, waiting up to
// waitMs for the runtime's first §4.7 lifecycle handshake. The gateway
// compares the returned level ("basic", "standard", or "full") against
// the runtime's declared integrationLevel on the first session assignment.
// An adapter on an older protocol returns codes.Unimplemented, which the
// caller treats as "not reported" and skips the check.
func (c *Client) GetObservedIntegrationLevel(ctx context.Context, waitMs int32) (string, error) {
	resp, err := c.rpc.GetObservedIntegrationLevel(ctx, &adapterv1.GetObservedIntegrationLevelRequest{
		WaitMs: waitMs,
	})
	if err != nil {
		return "", err
	}
	return resp.GetObservedLevel(), nil
}

// StartSessionParams carries the §15.4 adapter-manifest inputs the
// gateway delivers to a pod's runtime at session start. SessionID and
// Runtime are required; the rest populate the §4.7 manifest fields and may
// be zero.
type StartSessionParams struct {
	SessionID string
	Runtime   string
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
	// SlotID, when set, claims a §6.4 concurrent-workspace slot rather than
	// the whole pod: the adapter starts the slot's runtime against
	// /workspace/slots/{slotId}/current. Empty in session mode (maxConcurrentSessions=1).
	// spec: §6.4 lines 385-405.
	SlotID string
}

// StartSession starts the runtime on a pod whose workspace is already
// materialized by FinalizeWorkspace and whose setup commands are already
// run by RunSetup (§4.7, the final session-assignment RPC). The params
// populate the §15.4 adapter manifest the runtime reads at startup.
func (c *Client) StartSession(ctx context.Context, p StartSessionParams) error {
	req := &adapterv1.StartSessionRequest{
		SessionId:          &adapterv1.SessionId{Value: p.SessionID},
		Runtime:            p.Runtime,
		ExperimentContext:  p.ExperimentContext,
		TracingContext:     p.TracingContext,
		AgentInterface:     p.AgentInterface,
		MinPlatformVersion: p.MinPlatformVersion,
	}
	if p.SlotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: p.SlotID}
	}
	_, err := c.rpc.StartSession(ctx, req)
	return err
}

// ConfigureWorkspace is the §6.1 SDK-warm counterpart of StartSession: on
// a preConnect pod it points the already-connected SDK at the finalized
// workspace cwd rather than starting the runtime from cold (§4.7
// ConfigureWorkspace RPC). It is idempotent. A pod-warm adapter returns
// Unimplemented, which the binder treats as a signal to fall back to
// StartSession.
func (c *Client) ConfigureWorkspace(ctx context.Context, sessionID, cwd string, experiment *adapterv1.ExperimentContext, tracing map[string]string) error {
	_, err := c.rpc.ConfigureWorkspace(ctx, &adapterv1.ConfigureWorkspaceRequest{
		SessionId:         &adapterv1.SessionId{Value: sessionID},
		Cwd:               cwd,
		ExperimentContext: experiment,
		TracingContext:    tracing,
	})
	return err
}

// DemoteSDK tears down a preConnect pod's pre-connected SDK so the pod
// falls back to pod-warm before the workspace is materialized (§6.1 line
// 34 / §4.7 DemoteSDK RPC). reason rides the request for the adapter's
// observability. A pod-warm adapter returns Unimplemented; per §6.1 line
// 40 the binder surfaces that as SDK_DEMOTION_NOT_SUPPORTED rather than
// silently proceeding with stale SDK state.
func (c *Client) DemoteSDK(ctx context.Context, reason string) error {
	_, err := c.rpc.DemoteSDK(ctx, &adapterv1.DemoteSDKRequest{Reason: reason})
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
	return c.assignCredentials(ctx, sessionID, "", leases)
}

// AssignCredentialsSlot is the §6.4 concurrent-workspace counterpart of
// AssignCredentials: the leases are written to the slot's own per-slot
// credential file /run/lenny/slots/{slotId}/credentials.json so a sibling
// slot's file is untouched. spec: §6.1 line 28.
func (c *Client) AssignCredentialsSlot(ctx context.Context, sessionID, slotID string, leases map[string]*adapterv1.CredentialLease) error {
	return c.assignCredentials(ctx, sessionID, slotID, leases)
}

func (c *Client) assignCredentials(ctx context.Context, sessionID, slotID string, leases map[string]*adapterv1.CredentialLease) error {
	req := &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Leases:    leases,
	}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
	_, err := c.rpc.AssignCredentials(ctx, req)
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
// those causes rides the request so the adapter applies the §4.7
// in-flight gate ceiling for fault-driven rotations and the unbounded
// wait for proactive_renewal. An empty trigger is treated by the adapter
// as a fault trigger (ceiling applies) for fail-closed safety.
//
// The request carries credential material; per §4.7 item 6 the call
// site must keep it out of access logs and telemetry. A nil or empty
// map rotates nothing.
// spec: §4.9 line 1413 / §4.7 line 822. F-13.3.10.
func (c *Client) RotateCredentials(ctx context.Context, sessionID string, leases map[string]*adapterv1.CredentialLease, trigger credential.RotationTrigger) error {
	_, err := c.rpc.RotateCredentials(ctx, &adapterv1.RotateCredentialsRequest{
		SessionId:       &adapterv1.SessionId{Value: sessionID},
		Leases:          leases,
		RotationTrigger: string(trigger),
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
	return c.prepareWorkspace(ctx, sessionID, "", uploads)
}

// PrepareWorkspaceSlot is the §6.4 concurrent-workspace counterpart of
// PrepareWorkspace: the uploads stage into the slot's own
// /workspace/slots/{slotId}/staging area. spec: §6.4 lines 401-405.
func (c *Client) PrepareWorkspaceSlot(ctx context.Context, sessionID, slotID string, uploads map[string][]byte) (*adapterv1.PrepareWorkspaceResponse, error) {
	return c.prepareWorkspace(ctx, sessionID, slotID, uploads)
}

func (c *Client) prepareWorkspace(ctx context.Context, sessionID, slotID string, uploads map[string][]byte) (*adapterv1.PrepareWorkspaceResponse, error) {
	stream, err := c.rpc.PrepareWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	sid := &adapterv1.SessionId{Value: sessionID}
	var slot *adapterv1.SlotId
	if slotID != "" {
		slot = &adapterv1.SlotId{Value: slotID}
	}
	for ref, content := range uploads {
		if err := sendUpload(stream, sid, slot, ref, content); err != nil {
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
func sendUpload(stream adapterv1.Adapter_PrepareWorkspaceClient, sid *adapterv1.SessionId, slot *adapterv1.SlotId, ref string, content []byte) error {
	for off := 0; ; off += prepareWorkspaceChunkSize {
		end := off + prepareWorkspaceChunkSize
		if end > len(content) {
			end = len(content)
		}
		if err := stream.Send(&adapterv1.PrepareWorkspaceRequest{
			SessionId: sid,
			SlotId:    slot,
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
//
// archive is the §13.4 per-Runtime archive-extraction opt-in surface
// (AllowSymlinks toggle, workspace root for symlink-target validation).
// Nil delivers the platform defaults (symlinks rejected) on the wire.
// spec: §7.4 lines 458, 462; §13.4 — F-7.4.4.
//
// The returned []*WorkspacePlanWarning carries any §14 non-fatal
// advisories the adapter raised during materialization (per §7.4 line
// 459 `workspace_plan_strip_components_skip`). The slice is nil when
// the materialization had nothing to report. F-7.4.15.
//
// midSession marks a §7.4 line 433 mid-session upload: the adapter overlays
// the plan's sources onto the running session's existing /workspace/current
// and signals the runtime once promotion completes, rather than replacing
// the whole tree. The §4.7 assignment-sequence callers pass false. F-7.4.6.
func (c *Client) FinalizeWorkspace(ctx context.Context, sessionID string, plan *adapterv1.WorkspacePlan, archive *adapterv1.ArchivePolicy, midSession bool) ([]*adapterv1.WorkspacePlanWarning, error) {
	return c.finalizeWorkspace(ctx, sessionID, "", plan, archive, midSession)
}

// FinalizeWorkspaceSlot is the §6.4 concurrent-workspace counterpart of
// FinalizeWorkspace: it materializes the plan into the slot's own
// /workspace/slots/{slotId}/current. spec: §6.4 lines 401-405.
func (c *Client) FinalizeWorkspaceSlot(ctx context.Context, sessionID, slotID string, plan *adapterv1.WorkspacePlan, archive *adapterv1.ArchivePolicy, midSession bool) ([]*adapterv1.WorkspacePlanWarning, error) {
	return c.finalizeWorkspace(ctx, sessionID, slotID, plan, archive, midSession)
}

func (c *Client) finalizeWorkspace(ctx context.Context, sessionID, slotID string, plan *adapterv1.WorkspacePlan, archive *adapterv1.ArchivePolicy, midSession bool) ([]*adapterv1.WorkspacePlanWarning, error) {
	req := &adapterv1.FinalizeWorkspaceRequest{
		SessionId:     &adapterv1.SessionId{Value: sessionID},
		WorkspacePlan: plan,
		ArchivePolicy: archive,
		MidSession:    midSession,
	}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
	resp, err := c.rpc.FinalizeWorkspace(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.GetWorkspacePlanWarnings(), nil
}

// RunSetup executes the §14 WorkspacePlan setup commands in the pod's
// workspace (§4.7, the third session-assignment RPC). setupPolicy
// bounds the aggregate setup phase per §5.1; a nil policy applies no
// aggregate cap. The returned outputs carry the §7.5 line 475 captured
// stdout / stderr / exit code for each executed command in submission
// order; on a hard failure the adapter may attach partial outputs in
// the gRPC status details — the caller can extract them with
// `status.FromError(err).Details()`. F-7.5.4.
func (c *Client) RunSetup(ctx context.Context, sessionID string, setupCommands []*adapterv1.SetupCommand, setupPolicy *adapterv1.SetupPolicy) ([]*adapterv1.SetupCommandOutput, error) {
	return c.runSetup(ctx, sessionID, "", setupCommands, setupPolicy)
}

// RunSetupSlot is the §6.4 concurrent-workspace counterpart of RunSetup:
// it runs the setup commands against the slot's own
// /workspace/slots/{slotId}/current cwd. spec: §6.4 line 404.
func (c *Client) RunSetupSlot(ctx context.Context, sessionID, slotID string, setupCommands []*adapterv1.SetupCommand, setupPolicy *adapterv1.SetupPolicy) ([]*adapterv1.SetupCommandOutput, error) {
	return c.runSetup(ctx, sessionID, slotID, setupCommands, setupPolicy)
}

func (c *Client) runSetup(ctx context.Context, sessionID, slotID string, setupCommands []*adapterv1.SetupCommand, setupPolicy *adapterv1.SetupPolicy) ([]*adapterv1.SetupCommandOutput, error) {
	req := &adapterv1.RunSetupRequest{
		SessionId:     &adapterv1.SessionId{Value: sessionID},
		SetupCommands: setupCommands,
		SetupPolicy:   setupPolicy,
	}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
	resp, err := c.rpc.RunSetup(ctx, req)
	if err != nil {
		// Try to recover partial outputs from the gRPC status details.
		if st, ok := status.FromError(err); ok {
			for _, d := range st.Details() {
				if r, isResp := d.(*adapterv1.RunSetupResponse); isResp {
					return r.GetOutputs(), err
				}
			}
		}
		return nil, err
	}
	return resp.GetOutputs(), nil
}

// SendMessage forwards a pre-encoded §15.4.1 message envelope to the
// pod's runtime. slotID stamps the §6.4 slot the gateway resolved for
// the session onto the request so the reconciled adapter routes the
// envelope to that slot's runtime stream; an empty slotID (the exclusive
// session-mode bind) tags none. spec: §7.2 (per-slot routing), §15.4.1
// (slotId multiplexing).
func (c *Client) SendMessage(ctx context.Context, sessionID, slotID string, envelope []byte) error {
	req := &adapterv1.SendMessageRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		EnvelopeJson: envelope,
	}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
	_, err := c.rpc.SendMessage(ctx, req)
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
	InterruptStatusUnspecified  InterruptStatus = 0
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

// SignalDeadline delivers the §11.3 line 240 pre-expiry warning to the
// pod's runtime over the lifecycle channel. remainingMs is the wall-clock
// left before the session's deadline; trigger names which deadline is
// approaching ("session_age", "budget", or "idle"). The returned bool
// reports whether the adapter forwarded the warning (false when the
// runtime has no lifecycle channel — Basic/Standard integration level,
// §15 line 2141). The call is one-way; the adapter does not wait for a
// runtime acknowledgement.
func (c *Client) SignalDeadline(ctx context.Context, sessionID string, remainingMs int32, trigger string) (bool, error) {
	resp, err := c.rpc.SignalDeadline(ctx, &adapterv1.SignalDeadlineRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		RemainingMs: remainingMs,
		Trigger:     trigger,
	})
	if err != nil {
		return false, err
	}
	return resp.GetDelivered(), nil
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

// CheckpointBarrierResult mirrors the §10.1 CheckpointBarrierAck the
// pod's adapter emits in response to a graceful-drain barrier: the
// echoed barrier id, the best-effort checkpoint ref (empty when the
// flush produced none), and the wall-clock the adapter spent quiescing.
type CheckpointBarrierResult struct {
	BarrierID     string
	CheckpointRef string
	QuiescedMs    int64
}

// CheckpointBarrier dispatches the §10.1 lines 163-181 graceful-drain
// barrier to the pod's adapter. The adapter validates generation
// against its last fenced value (rejecting with FailedPrecondition
// when stale), quiesces tool-call dispatch, flushes a best-effort
// checkpoint, and acknowledges. The synchronous return mirrors the
// CheckpointBarrierAck the adapter also emits on the LifecycleChannel
// control stream.
//
// spec: §10.1 lines 163-181.
func (c *Client) CheckpointBarrier(ctx context.Context, sessionID string, coordinationGeneration int64, barrierID string) (CheckpointBarrierResult, error) {
	resp, err := c.rpc.CheckpointBarrier(ctx, &adapterv1.CheckpointBarrierRequest{
		SessionId:              &adapterv1.SessionId{Value: sessionID},
		CoordinationGeneration: coordinationGeneration,
		BarrierId:              barrierID,
	})
	if err != nil {
		return CheckpointBarrierResult{}, err
	}
	return CheckpointBarrierResult{
		BarrierID:     resp.GetBarrierId(),
		CheckpointRef: resp.GetCheckpointRef(),
		QuiescedMs:    resp.GetQuiescedMs(),
	}, nil
}

// ExportSpec is one §8.7 fileExport entry passed to the adapter's
// ExportPaths RPC: a source glob resolved inside the parent pod's
// /workspace/current and the relative destPrefix the matched files are
// rebased under in the child workspace.
type ExportSpec struct {
	Source     string
	DestPrefix string
}

// ExportedFile is one rebased file the parent pod's adapter packaged
// for a delegation export (§4.7 line 633 / §8.7). Path is the
// child-workspace-relative destination after the §8.7 rebasing rule.
type ExportedFile struct {
	Path    string
	Content []byte
	SHA256  string
	Size    int64
}

// ExportPaths runs the §4.7 / §8.7 ExportPaths RPC against the parent
// session's pod adapter, asking it to resolve the export specs inside
// /workspace/current, reject symlink-escaping matches, strip each
// glob's base path, and re-root the matched files under each spec's
// destPrefix. The gateway applies the lease fileExportLimits and the
// optional content scan to the returned set before persisting it for
// the child (§8.2 steps 3, 4).
func (c *Client) ExportPaths(ctx context.Context, sessionID string, specs []ExportSpec) ([]ExportedFile, error) {
	pb := make([]*adapterv1.ExportSpec, 0, len(specs))
	for _, s := range specs {
		pb = append(pb, &adapterv1.ExportSpec{Source: s.Source, DestPrefix: s.DestPrefix})
	}
	resp, err := c.rpc.ExportPaths(ctx, &adapterv1.ExportPathsRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Exports:   pb,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ExportedFile, 0, len(resp.GetFiles()))
	for _, f := range resp.GetFiles() {
		out = append(out, ExportedFile{
			Path:    f.GetPath(),
			Content: f.GetContent(),
			SHA256:  f.GetSha256(),
			Size:    f.GetSizeBytes(),
		})
	}
	return out, nil
}

// ResumeParams carries the inputs to restore a session onto a replacement
// pod. SessionID, Runtime, and CheckpointID are required; the rest
// re-deliver the §15.4 adapter-manifest fields so the restored runtime
// reads the same manifest as before the resume.
type ResumeParams struct {
	SessionID          string
	Runtime            string
	CheckpointID       string
	ExperimentContext  *adapterv1.ExperimentContext
	TracingContext     map[string]string
	AgentInterface     []byte
	MinPlatformVersion string
	// RecoveryGeneration is the session's §4.2 / §7.3 pod-recovery
	// counter at issue time. Re-delivered to the adapter so per-pod
	// telemetry and any partial-manifest the adapter writes carry the
	// (coordination, recovery) tuple that authored the replay.
	// F-7.3.22.
	RecoveryGeneration int64
	// ExpectedWorkspaceBytes is the session's
	// last_checkpoint_workspace_bytes — the size the adapter is
	// expected to extract. Zero leaves the adapter without a hint and
	// the kubelet emptyDir guard is the only backstop. F-7.3.26.
	ExpectedWorkspaceBytes int64
	// WorkspaceSizeLimitBytes is the §4.4 hard workspace size cap
	// resolved from the SandboxTemplate. Zero disables the
	// gateway-supplied limit. F-7.3.26.
	WorkspaceSizeLimitBytes int64
	// ExpectedWorkspaceRoot is the §7.3 line 408 "same absolute cwd
	// path" the original session ran against. The gateway passes it so
	// the adapter can refuse a Resume whose replacement pod was
	// provisioned with a different mount path — the §7.3 step (d)
	// contractual guard against runtime template drift. Empty leaves
	// the adapter without a hint (the §7.3 line 408 invariant is then
	// upheld by construction only). F-7.3.15.
	ExpectedWorkspaceRoot string
}

// ResumeResult is the adapter's response to a Resume call. The §4.4 /
// §7.2 mode and the echoed recovery_generation surface back to the
// gateway so the `session.resumed` event payload carries the resume
// mode that actually executed on the pod. F-7.3.22.
type ResumeResult struct {
	RestoredBytes      int64
	Mode               string
	RecoveryGeneration int64
}

// Resume asks the pod's adapter to restore the session workspace from the
// named §4.4 checkpoint and start the runtime (§4.7, §7.1) — the
// replacement-pod counterpart of StartSession. The params re-deliver the
// §15.4 manifest fields to the restored runtime. It returns the
// adapter's ResumeResult, including the §4.4 / §7.2 mode label and the
// echoed §4.2 recovery_generation. F-7.3.22, F-7.3.26.
func (c *Client) Resume(ctx context.Context, p ResumeParams) (ResumeResult, error) {
	resp, err := c.rpc.Resume(ctx, &adapterv1.ResumeRequest{
		SessionId:               &adapterv1.SessionId{Value: p.SessionID},
		Runtime:                 p.Runtime,
		CheckpointId:            p.CheckpointID,
		ExperimentContext:       p.ExperimentContext,
		TracingContext:          p.TracingContext,
		AgentInterface:          p.AgentInterface,
		MinPlatformVersion:      p.MinPlatformVersion,
		RecoveryGeneration:      p.RecoveryGeneration,
		ExpectedWorkspaceBytes:  p.ExpectedWorkspaceBytes,
		WorkspaceSizeLimitBytes: p.WorkspaceSizeLimitBytes,
		ExpectedWorkspaceRoot:   p.ExpectedWorkspaceRoot,
	})
	if err != nil {
		return ResumeResult{}, err
	}
	return ResumeResult{
		RestoredBytes:      resp.GetRestoredBytes(),
		Mode:               resp.GetMode(),
		RecoveryGeneration: resp.GetRecoveryGeneration(),
	}, nil
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
// pod's adapter (§4.7). When cumulative is false the accounting is
// incremental — each call returns usage accumulated since the previous
// one — so the gateway sums it for §11.2 budget enforcement and billing;
// the steady-state direct-mode poll uses this path. When cumulative is
// true the adapter returns the session's running cumulative total
// instead, which a reconnected gateway replica pulls to seed its
// restored counter for the §11.2 crash-recovery MAX rule
// (MAX(postgres_checkpoint, pod-reported cumulative total)). The adapter
// advances its last-read watermark on either read, so a cumulative
// recovery read followed by a steady-state delta read reports zero
// rather than re-adding the already-recovered tokens.
//
// spec: §4.7 (ReportUsageRequest.cumulative), §11.2 (pod-reported
// cumulative-usage re-report on reconnection).
//
// spec: spec/04_system-components.md line 1468 — for sessions in proxy
// mode the gateway is the sole counter of record (the §4.9 LLM proxy
// extracts authoritative counts from the upstream response). The
// caller must filter proxy-mode sessions before reaching this RPC; the
// pod-reported counts are not accepted for them. ErrUsageReportProxyMode
// surfaces a misrouted proxy-mode call so a regression is observable
// rather than silently double-counting.
func (c *Client) ReportUsage(ctx context.Context, sessionID string, cumulative bool) (UsageReport, error) {
	resp, err := c.rpc.ReportUsage(ctx, &adapterv1.ReportUsageRequest{
		SessionId:  &adapterv1.SessionId{Value: sessionID},
		Cumulative: cumulative,
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
// cumulative selects the read: the steady-state direct-mode poll passes
// false for the incremental delta, and the §11.2 crash-recovery path
// passes true for the session's running cumulative total. The proxy-mode
// filter runs first for both reads, so a proxy lease is never
// double-counted regardless of which read a caller requests.
//
// spec: §4.7 (ReportUsageRequest.cumulative), §11.2 (direct-mode usage,
// crash-recovery MAX rule); spec/04_system-components.md line 1468.
func (c *Client) ReportUsageForLease(ctx context.Context, sessionID string, deliveryMode credential.DeliveryMode, cumulative bool) (UsageReport, error) {
	if deliveryMode == credential.DeliveryProxy {
		return UsageReport{}, ErrUsageReportProxyMode
	}
	return c.ReportUsage(ctx, sessionID, cumulative)
}

// AttachStream is a live §4.7 bidirectional content stream to a pod's
// adapter. Send forwards a client-to-agent envelope; Recv returns the
// next agent-to-gateway envelope. A concurrent-pool stream carries the
// adapter-assigned §6.4 slotId so the reconciled adapter and the
// runtime's dispatch loop key on it; an exclusive session-mode stream
// leaves it empty.
type AttachStream struct {
	stream    grpc.BidiStreamingClient[adapterv1.AttachRequest, adapterv1.AttachResponse]
	sessionID string
	slotID    string
}

// Attach opens the §4.7 content stream for sessionID and binds it with
// an envelope-free first message, so the returned stream is ready to
// carry content. slotID stamps the §6.4 slot the gateway resolved for
// the session onto the binding frame and every subsequent Send frame;
// an empty slotID (the exclusive session-mode bind) emits no slotId so
// a single-session pod's runtime never sees one. The caller closes the
// stream by cancelling ctx. spec: §7.2 (per-slot routing), §15.4.1
// (slotId multiplexing).
func (c *Client) Attach(ctx context.Context, sessionID, slotID string) (*AttachStream, error) {
	stream, err := c.rpc.Attach(ctx)
	if err != nil {
		return nil, fmt.Errorf("adapterclient: open attach stream: %w", err)
	}
	req := &adapterv1.AttachRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
	if err := stream.Send(req); err != nil {
		return nil, fmt.Errorf("adapterclient: bind attach stream: %w", err)
	}
	return &AttachStream{stream: stream, sessionID: sessionID, slotID: slotID}, nil
}

// Send forwards a §15.4.1 client-to-agent envelope to the agent. A
// concurrent-pool stream stamps the resolved slotId on the frame.
func (a *AttachStream) Send(envelope []byte) error {
	req := &adapterv1.AttachRequest{
		SessionId:    &adapterv1.SessionId{Value: a.sessionID},
		EnvelopeJson: envelope,
	}
	if a.slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: a.slotID}
	}
	return a.stream.Send(req)
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
	return c.shutdown(ctx, sessionID, "")
}

// ShutdownSlot is the §6.4 concurrent-workspace counterpart of Shutdown:
// it tears down only the named slot (its runtime and per-slot tree) while
// sibling slots on the pod keep running. spec: §6.4 lines 401-405.
func (c *Client) ShutdownSlot(ctx context.Context, sessionID, slotID string) (bool, error) {
	return c.shutdown(ctx, sessionID, slotID)
}

func (c *Client) shutdown(ctx context.Context, sessionID, slotID string) (bool, error) {
	req := &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
	}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
	resp, err := c.rpc.Shutdown(ctx, req)
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

// RecycleScrub carries the pod identity and the §5.2 whole-pod scrub
// parameters the gateway delivers on the occupancy-zero recycle-boundary
// Shutdown. The gateway resolves these from the pod's SandboxName and the
// resolved pool's sessionPolicy.recycle block, captured once at bind time.
type RecycleScrub struct {
	// PodID is the agent_pod_state row key (the Sandbox name) the gateway
	// keys the missing-report timeout on and the adapter echoes back in
	// ReportPodScrub so the report matches the armed timer.
	// spec: §4.7 Shutdown recycle disposition.
	PodID string
	// CleanupCommands are the deployer-defined cleanupCommands the scrub runs
	// after the credential purge and before the standard scrub steps. Empty
	// runs no cleanup commands. spec: §5.2 whole-pod scrub trigger.
	CleanupCommands []string
	// CleanupTimeoutSeconds is the aggregate cap on the cleanup commands; the
	// gateway-side missing-report timeout is this value plus a grace period.
	// spec: §5.2 whole-pod scrub trigger.
	CleanupTimeoutSeconds int32
}

// ShutdownRecycle is the §4.7 recycle-disposition variant of the proto
// Shutdown RPC (the row the §4.7 table names Terminate). It marks the
// occupancy-zero recycle boundary: the adapter closes the ending session's
// runtime, keeps the pod process alive across the boundary, runs the §5.2
// whole-pod scrub with the carried parameters, and reports its binary
// outcome for rc.PodID asynchronously via ReportPodScrub. The gateway does
// not block the response on the scrub; the missing-report timeout it armed
// before this call bounds it. The returned bool reports whether the ending
// session's runtime exited cleanly.
//
// The plain Shutdown, ShutdownSlot, and Terminate helpers leave the recycle
// sub-message nil (the terminate path, on which the pod is replaced).
//
// spec: §4.7 Shutdown recycle disposition; §5.2 whole-pod scrub trigger.
func (c *Client) ShutdownRecycle(ctx context.Context, sessionID string, rc RecycleScrub) (bool, error) {
	resp, err := c.rpc.Shutdown(ctx, &adapterv1.ShutdownRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Recycle: &adapterv1.RecycleScrub{
			PodId:                 rc.PodID,
			CleanupCommands:       rc.CleanupCommands,
			CleanupTimeoutSeconds: rc.CleanupTimeoutSeconds,
		},
	})
	if err != nil {
		return false, err
	}
	return resp.GetExitedCleanly(), nil
}
