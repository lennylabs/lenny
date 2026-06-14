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

	"go.opentelemetry.io/otel/attribute"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
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
	// spec: §16.3 line 338 — `session.upload` is a Gateway + Pod span. This
	// is the Pod half: the adapter streams uploaded files into the staging
	// area. The gateway half (the client→gateway upload) is emitted there.
	// F-16.3.6 / pod half of F-16.3.1.
	_, span := tracing.NewTracer(nil).Start(stream.Context(), tracing.SpanSessionUpload)
	var spanErr error
	defer func() {
		tracing.RecordError(span, spanErr)
		span.End()
	}()

	// spec: §6.4 lines 401-405 — a slot-qualified upload stages into the
	// slot's /workspace/slots/{slotId}/staging area. The staging directory
	// is resolved from the first frame (every frame carries the same slot
	// id); session/task mode uses the pod-global StagingDir.
	var stagingDir string
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
			// §16.3: a broken upload stream is TRANSIENT (the gateway may
			// re-stream the upload).
			spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
			return err
		}
		if req.GetSessionId().GetValue() == "" {
			closeAll()
			spanErr = tracing.CategorizeError(
				status.Error(codes.InvalidArgument, "PrepareWorkspace frame requires a session id"),
				tracing.CategoryPermanent,
			)
			return status.Error(codes.InvalidArgument,
				"PrepareWorkspace frame requires a session id")
		}
		if stagingDir == "" {
			dir, derr := s.resolvePrepareStagingDir(req.GetSlotId().GetValue())
			if derr != nil {
				closeAll()
				spanErr = tracing.CategorizeError(derr, tracing.CategoryPermanent)
				return derr
			}
			stagingDir = dir
		}
		ref := req.GetUploadRef()
		f, ok := open[ref]
		if !ok {
			path, err := workspace.StagingPath(stagingDir, ref)
			if err != nil {
				closeAll()
				spanErr = tracing.CategorizeError(err, tracing.CategoryPermanent)
				return status.Errorf(codes.InvalidArgument, "%v", err)
			}
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				closeAll()
				spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
				return status.Errorf(codes.Internal, "open staging file: %v", err)
			}
			open[ref] = f
		}
		n, err := f.Write(req.GetChunk())
		if err != nil {
			closeAll()
			spanErr = tracing.CategorizeError(err, tracing.CategoryTransient)
			return status.Errorf(codes.Internal, "write staging file: %v", err)
		}
		stagedBytes += int64(n)
	}
	closeAll()
	span.SetAttributes(
		attribute.Int64("upload.staged_bytes", stagedBytes),
		attribute.Int("upload.staged_files", len(open)),
	)
	return stream.SendAndClose(&adapterv1.PrepareWorkspaceResponse{
		StagedBytes: stagedBytes,
		StagedFiles: int32(len(open)),
	})
}

// resolvePrepareStagingDir returns the staging directory PrepareWorkspace
// streams uploads into. For a §6.4 concurrent slot it ensures the slot
// tree exists and returns the slot's /workspace/slots/{slotId}/staging.
// For the session/task base path it returns the pod-global StagingDir,
// returning FailedPrecondition when unconfigured and creating it if
// absent (mirroring the prior behavior).
func (s *Server) resolvePrepareStagingDir(slotID string) (string, error) {
	if s.useSlot(slotID) {
		paths, err := s.ensureSlotPaths(slotID)
		if err != nil {
			return "", status.Errorf(codes.InvalidArgument, "resolve slot %s staging: %v", slotID, err)
		}
		return paths.Staging, nil
	}
	if s.StagingDir == "" {
		return "", status.Error(codes.FailedPrecondition,
			"adapter is not configured with a staging directory")
	}
	if err := os.MkdirAll(s.StagingDir, 0o700); err != nil {
		return "", status.Errorf(codes.Internal, "create staging directory: %v", err)
	}
	return s.StagingDir, nil
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
func (s *Server) FinalizeWorkspace(ctx context.Context, req *adapterv1.FinalizeWorkspaceRequest) (*adapterv1.FinalizeWorkspaceResponse, error) {
	// spec: §16.3 line 339 — `session.finalize_workspace` is emitted by the
	// Pod. The span covers the workspace-plan materialization the adapter
	// runs before the session is claimed. F-16.3.6.
	_, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanSessionFinalizeWorkspace)
	var spanErr error
	defer func() {
		tracing.RecordError(span, spanErr)
		span.End()
	}()

	if req.GetSessionId().GetValue() == "" {
		spanErr = tracing.CategorizeError(
			status.Error(codes.InvalidArgument, "FinalizeWorkspace requires a session id"),
			tracing.CategoryPermanent,
		)
		return nil, status.Error(codes.InvalidArgument, "FinalizeWorkspace requires a session id")
	}
	// spec: §6.4 lines 401-405 — a slot-qualified finalize materializes
	// into the slot's per-slot tree (/workspace/slots/{slotId}/staging
	// promoted to /current) and creates that tree on first reference.
	// Session/task mode uses the pod-global WorkspaceRoot/StagingDir.
	workspaceRoot, stagingDir := s.WorkspaceRoot, s.StagingDir
	if slotID := req.GetSlotId().GetValue(); s.useSlot(slotID) {
		paths, perr := s.ensureSlotPaths(slotID)
		if perr != nil {
			spanErr = tracing.CategorizeError(perr, tracing.CategoryPermanent)
			return nil, status.Errorf(codes.InvalidArgument, "resolve slot %s workspace: %v", slotID, perr)
		}
		workspaceRoot, stagingDir = paths.Current, paths.Staging
	}
	if workspaceRoot == "" {
		spanErr = tracing.CategorizeError(
			status.Error(codes.FailedPrecondition, "adapter is not configured with a workspace root"),
			tracing.CategoryPermanent,
		)
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
		// §16.3: an unsupported plan schema is PERMANENT (the gateway must
		// not retry the same plan against this adapter build).
		spanErr = tracing.CategorizeError(err, tracing.CategoryPermanent)
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
		archive.WorkspaceRoot = workspaceRoot
	}
	sources := req.GetWorkspacePlan().GetSources()
	span.SetAttributes(attribute.Int("workspace.source_count", len(sources)))
	// spec: §7.4 line 433 — a mid-session upload overlays the sources onto
	// the running session's existing /workspace/current rather than
	// replacing the whole tree, then signals the runtime once promotion
	// completes. The pre-start path (mid_session false) keeps the §4.7
	// whole-tree promotion the assignment sequence relies on. F-7.4.6.
	midSession := req.GetMidSession()
	span.SetAttributes(attribute.Bool("workspace.mid_session", midSession))
	var (
		warnings []workspace.Warning
		err      error
	)
	if midSession {
		warnings, err = workspace.MaterializeOverlayWithPolicy(workspaceRoot, stagingDir,
			sources, archive)
	} else {
		warnings, err = workspace.MaterializeWithPolicy(workspaceRoot, stagingDir,
			sources, archive)
	}
	if err != nil {
		// §16.3: a plan the adapter cannot materialize (invalid source,
		// containment violation) is PERMANENT.
		spanErr = tracing.CategorizeError(err, tracing.CategoryPermanent)
		return nil, status.Errorf(codes.InvalidArgument, "materialize workspace: %v", err)
	}
	// spec: §7.4 line 433 — "The runtime adapter receives a FilesUpdated
	// notification only after promotion, so the agent never sees
	// partially-written files." The overlay above is already promoted, so
	// the signal is safe to emit now. It is best-effort: a not-connected
	// channel (no Full-level runtime, or the pre-start path) is benign,
	// and the promoted files are already on disk regardless. F-7.4.6.
	if midSession && s.Lifecycle != nil {
		if sigErr := s.Lifecycle.SignalFilesUpdated(); sigErr != nil {
			log.Printf("lenny-adapter: files_updated signal for session %s not delivered: %v",
				req.GetSessionId().GetValue(), sigErr)
		}
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
		// spec: §14 line 338 — the path-collision warning carries `path`,
		// `winningSourceIndex`, and `losingSourceIndex` so a consumer can
		// audit which source overwrote which. F-14.1.9.
		if w.Code == "workspace_plan_path_collision" {
			pw.Path = w.Path
			pw.WinningSourceIndex = int32(w.WinningSourceIndex)
			pw.LosingSourceIndex = int32(w.LosingSourceIndex)
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
	// spec: §16.3 line 340 — `session.run_setup` is emitted by the Pod. The
	// span covers the §14 setup-command execution phase. F-16.3.6.
	ctx, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanSessionRunSetup)
	var spanErr error
	defer func() {
		tracing.RecordError(span, spanErr)
		span.End()
	}()

	if req.GetSessionId().GetValue() == "" {
		spanErr = tracing.CategorizeError(
			status.Error(codes.InvalidArgument, "RunSetup requires a session id"),
			tracing.CategoryPermanent,
		)
		return nil, status.Error(codes.InvalidArgument, "RunSetup requires a session id")
	}
	// spec: §6.4 line 404 — a slot-qualified setup runs against the slot's
	// own /workspace/slots/{slotId}/current cwd. Session/task mode uses the
	// pod-global WorkspaceRoot.
	workspaceRoot := s.WorkspaceRoot
	if slotID := req.GetSlotId().GetValue(); s.useSlot(slotID) {
		paths, perr := s.ensureSlotPaths(slotID)
		if perr != nil {
			spanErr = tracing.CategorizeError(perr, tracing.CategoryPermanent)
			return nil, status.Errorf(codes.InvalidArgument, "resolve slot %s workspace: %v", slotID, perr)
		}
		workspaceRoot = paths.Current
	}
	if workspaceRoot == "" {
		spanErr = tracing.CategorizeError(
			status.Error(codes.FailedPrecondition, "adapter is not configured with a workspace root"),
			tracing.CategoryPermanent,
		)
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root")
	}
	span.SetAttributes(attribute.Int("setup.command_count", len(req.GetSetupCommands())))
	outputs, err := workspace.RunSetup(ctx, workspaceRoot, req.GetSetupCommands(),
		setupOptionsFromProto(req.GetSetupPolicy(), workspaceRoot))
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
		//
		// §16.3: a failing setup command (non-zero exit, hard timeout) is the
		// UPSTREAM category — the failure originates in the runtime's own
		// setup command rather than in the adapter.
		spanErr = tracing.CategorizeError(err, tracing.CategoryUpstream)
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
