// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// PrepareWorkspace accepts streamed upload-file content into the pod's
// staging area. It is the first RPC of the §4.7 session assignment
// sequence (PrepareWorkspace, FinalizeWorkspace, RunSetup,
// StartSession). Frames sharing an upload_ref are concatenated in
// arrival order into one staged file; FinalizeWorkspace materializes
// the uploadFile and uploadArchive plan sources from the staged
// content. Like the rest of the staging sequence it runs before the
// session is claimed, so it does not touch pod-assignment state.
func (s *Server) PrepareWorkspace(stream adapterv1.Adapter_PrepareWorkspaceServer) error {
	if s.StagingDir == "" {
		return status.Error(codes.FailedPrecondition,
			"adapter is not configured with a staging directory")
	}
	if err := os.MkdirAll(s.StagingDir, 0o700); err != nil {
		return status.Errorf(codes.Internal, "create staging directory: %v", err)
	}
	open := map[string]*os.File{}
	closeAll := func() {
		for _, f := range open {
			_ = f.Close()
		}
	}
	var stagedBytes int64
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			closeAll()
			return err
		}
		if req.GetSessionId().GetValue() == "" {
			closeAll()
			return status.Error(codes.InvalidArgument,
				"PrepareWorkspace frame requires a session id")
		}
		ref := req.GetUploadRef()
		f, ok := open[ref]
		if !ok {
			path, err := workspace.StagingPath(s.StagingDir, ref)
			if err != nil {
				closeAll()
				return status.Errorf(codes.InvalidArgument, "%v", err)
			}
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				closeAll()
				return status.Errorf(codes.Internal, "open staging file: %v", err)
			}
			open[ref] = f
		}
		n, err := f.Write(req.GetChunk())
		if err != nil {
			closeAll()
			return status.Errorf(codes.Internal, "write staging file: %v", err)
		}
		stagedBytes += int64(n)
	}
	closeAll()
	return stream.SendAndClose(&adapterv1.PrepareWorkspaceResponse{
		StagedBytes: stagedBytes,
		StagedFiles: int32(len(open)),
	})
}

// FinalizeWorkspace materializes the §14 WorkspacePlan into the
// workspace root. It is the second RPC of the §4.7 session assignment
// sequence (PrepareWorkspace, FinalizeWorkspace, RunSetup,
// StartSession): like RunSetup it runs before the session is claimed,
// so it neither claims the session nor touches pod assignment state.
//
// Validation of the streamed PrepareWorkspace content against the plan
// arrives with the PrepareWorkspace RPC; this RPC materializes the
// filesystem-native plan sources via workspace.Materialize, which builds
// the resolved tree in /workspace/staging and atomically promotes it onto
// /workspace/current so the runtime never observes a partial workspace.
// spec: §7.4 line 433 — F-7.4.12.
func (s *Server) FinalizeWorkspace(_ context.Context, req *adapterv1.FinalizeWorkspaceRequest) (*adapterv1.FinalizeWorkspaceResponse, error) {
	if req.GetSessionId().GetValue() == "" {
		return nil, status.Error(codes.InvalidArgument, "FinalizeWorkspace requires a session id")
	}
	if s.WorkspaceRoot == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root")
	}
	// spec: §14.1 line 326 — the adapter is a live consumer at
	// materialization time and MUST reject a plan whose schemaVersion it
	// does not understand before touching the filesystem; a stale adapter
	// could otherwise misinterpret fields a newer gateway wrote during a
	// rolling upgrade. The typed WORKSPACE_PLAN_SCHEMA_UNSUPPORTED code +
	// version pair ride as a gRPC status detail so the gateway can map it
	// to the §15.1 422 envelope. F-14.1.3.
	schemaVersion := int(req.GetWorkspacePlan().GetSchemaVersion())
	if err := workspace.CheckSchemaVersion(schemaVersion); err != nil {
		st := status.New(codes.FailedPrecondition, err.Error())
		if withDetail, dErr := st.WithDetails(&adapterv1.Error{
			Code:      adapterv1.Error_ERROR_CODE_WORKSPACE_PLAN_SCHEMA_UNSUPPORTED,
			Category:  adapterv1.Error_CATEGORY_PERMANENT,
			Message:   err.Error(),
			Retryable: false,
		}); dErr == nil {
			return nil, withDetail.Err()
		}
		return nil, st.Err()
	}
	// spec: §7.4 lines 458, 462 — F-7.4.4. The gateway delivers the
	// per-Runtime allowSymlinks opt-in plus the absolute workspace root
	// (slot-scoped paths in §6.4 concurrent runtimes) on FinalizeWorkspace.
	// An unset workspace_root falls back to the adapter's configured root
	// so the §13.4 symlink-target check still has a basis when the gateway
	// delivers no override.
	archive := workspace.ArchivePolicy{
		AllowSymlinks: req.GetArchivePolicy().GetAllowSymlinks(),
		WorkspaceRoot: req.GetArchivePolicy().GetWorkspaceRoot(),
	}
	if archive.WorkspaceRoot == "" {
		archive.WorkspaceRoot = s.WorkspaceRoot
	}
	warnings, err := workspace.MaterializeWithPolicy(s.WorkspaceRoot, s.StagingDir,
		req.GetWorkspacePlan().GetSources(), archive)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "materialize workspace: %v", err)
	}
	// F-7.4.15 / F-14.1.18: transcribe the §14 advisory warnings onto
	// the FinalizeWorkspaceResponse so the gateway can republish the
	// §7.4 line 459 `workspace_plan_strip_components_skip` per-entry
	// warning event without redoing the archive walk in two places. The
	// per-warning structured fields (`entryPath`, `segmentCount`,
	// `stripComponents`) ride per §14 line 100.
	resp := &adapterv1.FinalizeWorkspaceResponse{}
	for _, w := range warnings {
		pw := &adapterv1.WorkspacePlanWarning{
			Code:            w.Code,
			SourceIndex:     int32(w.SourceIndex),
			EntryPath:       w.EntryPath,
			SegmentCount:    int32(w.SegmentCount),
			StripComponents: int32(w.StripComponents),
			Message:         w.Message,
		}
		// spec: §14 line 334 — the unknown-source-type warning carries
		// `unknownType` and the plan's `schemaVersion`. The materializer
		// fills UnknownType; the plan version is stamped here, where the
		// request (and thus the plan) is in scope. F-14.1.2.
		if w.UnknownType != "" {
			pw.UnknownType = w.UnknownType
			pw.SchemaVersion = int32(schemaVersion)
		}
		resp.WorkspacePlanWarnings = append(resp.WorkspacePlanWarnings, pw)
	}
	return resp, nil
}

// RunSetup executes the §14 WorkspacePlan setup commands against the
// materialized workspace. It is the third RPC of the §4.7 session
// assignment sequence (PrepareWorkspace, FinalizeWorkspace, RunSetup,
// StartSession): the workspace has already been materialized into
// WorkspaceRoot by the time RunSetup runs, so this RPC neither claims
// the session nor touches pod assignment state. The §5.1 setupPolicy in
// the request bounds the aggregate setup phase. The response carries
// the per-command setup output the §7.5 line 475 "Fully logged" contract
// requires; the gateway persists this on the session row so a client can
// see it via §15.1 GET /v1/sessions/{id}. F-7.5.4.
func (s *Server) RunSetup(ctx context.Context, req *adapterv1.RunSetupRequest) (*adapterv1.RunSetupResponse, error) {
	if req.GetSessionId().GetValue() == "" {
		return nil, status.Error(codes.InvalidArgument, "RunSetup requires a session id")
	}
	if s.WorkspaceRoot == "" {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root")
	}
	outputs, err := workspace.RunSetup(ctx, s.WorkspaceRoot, req.GetSetupCommands(),
		setupOptionsFromProto(req.GetSetupPolicy(), s.WorkspaceRoot))
	resp := &adapterv1.RunSetupResponse{Outputs: setupOutputsToProto(outputs)}
	if err != nil {
		// spec: §5.1 lines 89-91 — setupPolicy.onTimeout = warn proceeds
		// to runtime start past the aggregate cap. workspace.RunSetup
		// returns ErrSetupAggregateTimeoutWarn (wrapped with cap +
		// command-index) so the warn case is operationally observable
		// rather than silently swallowed. Log a structured warning line
		// and report RPC success. F-7.5.13.
		if errors.Is(err, workspace.ErrSetupAggregateTimeoutWarn) {
			session := req.GetSessionId().GetValue()
			cap := req.GetSetupPolicy().GetTimeoutSeconds()
			log.Printf("lenny-adapter: setup_aggregate_timeout_warn session=%s cap_seconds=%d cmd_count=%d: %v",
				session, cap, len(req.GetSetupCommands()), err)
			return resp, nil
		}
		// On a hard failure the partial outputs survive in the gRPC status
		// details so the gateway can persist what was captured before the
		// failure. The error itself is a FailedPrecondition status.
		st := status.New(codes.FailedPrecondition,
			fmt.Sprintf("run setup commands: %v", err))
		if stWithDetails, dErr := st.WithDetails(resp); dErr == nil {
			return nil, stWithDetails.Err()
		}
		return nil, st.Err()
	}
	return resp, nil
}

// setupOutputsToProto converts the workspace-layer SetupCommandOutput
// slice to the wire form the §15.1 session-envelope plumbing consumes.
// spec: §7.5 line 475 — F-7.5.4.
func setupOutputsToProto(outs []workspace.SetupCommandOutput) []*adapterv1.SetupCommandOutput {
	if len(outs) == 0 {
		return nil
	}
	wire := make([]*adapterv1.SetupCommandOutput, 0, len(outs))
	for _, o := range outs {
		wire = append(wire, &adapterv1.SetupCommandOutput{
			Cmd:        o.Cmd,
			ExitCode:   o.ExitCode,
			Stdout:     o.Stdout,
			Stderr:     o.Stderr,
			DurationMs: o.Duration.Milliseconds(),
			Truncated:  o.Truncated,
		})
	}
	return wire
}
