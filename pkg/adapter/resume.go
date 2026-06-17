// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// CheckpointSource loads a stored §4.4 workspace checkpoint archive
// from the artifact store. It is the read counterpart of CheckpointSink.
type CheckpointSource interface {
	// LoadCheckpoint opens the gzip-tar workspace archive for the
	// checkpoint. The caller closes the returned reader.
	LoadCheckpoint(ctx context.Context, checkpointID string) (io.ReadCloser, error)
}

// Resume implements the §4.7 / §7.1 Resume RPC: it restores a session's
// workspace from a checkpoint on a replacement pod. It claims the pod
// for the session, rebuilds the workspace from the checkpoint archive,
// and starts the runtime — the replacement-pod counterpart of
// StartSession. On any failure after the session is claimed the pod is
// returned to idle so a retry can land on a fresh pod.
func (s *Server) Resume(ctx context.Context, req *adapterv1.ResumeRequest) (*adapterv1.ResumeResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "Resume requires a session id")
	}
	if req.GetCheckpointId() == "" {
		return nil, status.Error(codes.InvalidArgument, "Resume requires a checkpoint id")
	}
	if s.WorkspaceRoot == "" || s.Runtime == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a workspace root and runtime")
	}
	if s.Restorer == nil {
		return nil, status.Error(codes.Unimplemented,
			"adapter is not configured with a checkpoint source")
	}

	if err := s.claimSession(sessionID); err != nil {
		return nil, err
	}

	// spec: §7.3 line 408 step (d) — "Recreate same absolute `cwd` path."
	// The gateway carries the original session's cwd on
	// `expected_workspace_root`; the adapter MUST refuse a Resume whose
	// replacement pod was provisioned with a different mount path so a
	// runtime template change between sessions cannot silently restore
	// into the wrong absolute path. The invariant is otherwise upheld by
	// construction (the SandboxTemplate's WorkspaceRoot is identical on
	// the replacement pod), but the assertion is the §7.3 contractual
	// guard called out by F-7.3.15. An empty `expected_workspace_root`
	// disables the assertion so a pre-F-7.3.15 client can still resume.
	if expected := req.GetExpectedWorkspaceRoot(); expected != "" && expected != s.WorkspaceRoot {
		s.releaseSession()
		return nil, status.Errorf(codes.FailedPrecondition,
			"resume rejected: workspace root mismatch (expected %q, adapter has %q)",
			expected, s.WorkspaceRoot)
	}

	// spec: §7.3 line 397 — the gateway passes `last_checkpoint_workspace_bytes`
	// (expected_workspace_bytes) and the §4.4 hard workspace size limit so the
	// adapter refuses a restore that would exceed the pod's emptyDir budget
	// before quiescing the runtime. The kubelet emptyDir guard remains the
	// backstop; this is the symmetric pre-extraction check called out by
	// F-7.3.26.
	if err := checkpoint.WorkspaceSizePreCheck(
		req.GetExpectedWorkspaceBytes(), req.GetWorkspaceSizeLimitBytes(),
	); err != nil {
		var sizeErr *checkpoint.WorkspaceSizeExceededError
		s.releaseSession()
		if errors.As(err, &sizeErr) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"resume rejected: %s", sizeErr.Error())
		}
		return nil, status.Errorf(codes.Internal, "workspace size precheck: %v", err)
	}

	rc, err := s.Restorer.LoadCheckpoint(ctx, req.GetCheckpointId())
	if err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "load checkpoint %s: %v", req.GetCheckpointId(), err)
	}
	// spec: §7.3 line 408 step (e) "Replay latest workspace checkpoint" +
	// line 409 step (f) "Restore session file to expected path". The
	// checkpoint bundle carries the workspace under workspace.WorkspacePrefix
	// and the §6.4 line 380 /sessions session file under
	// workspace.SessionsPrefix; ExtractTree replays each to its own root so
	// the runtime's native SDK session state is rehydrated, not lost. A
	// checkpoint written before the bundle change, or one without a
	// /sessions tree, restores workspace-only.
	restored, extractErr := workspace.ExtractTree(s.checkpointRoots(), rc)
	_ = rc.Close()
	if extractErr != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "restore workspace from checkpoint %s: %v",
			req.GetCheckpointId(), extractErr)
	}
	// §9.3 line 142: re-resolve the session's permitted connectors so the
	// restored runtime gets the same per-connector MCP servers it had
	// before the resume. Best-effort.
	connectors := s.sessionConnectors(ctx, sessionID)
	// §15.4: re-deliver the manifest so the restored runtime reads the
	// same §4.7 / §8.3 fields as before the resume.
	nonce, err := s.writeSessionManifest(manifestInputs{
		sessionID:          sessionID,
		taskID:             req.GetTaskId(),
		experimentContext:  req.GetExperimentContext(),
		tracingContext:     req.GetTracingContext(),
		agentInterface:     req.GetAgentInterface(),
		minPlatformVersion: req.GetMinPlatformVersion(),
		connectors:         connectors,
	})
	if err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "write adapter manifest: %v", err)
	}
	// §4.7: start the platform MCP server for the restored session.
	if err := s.startPlatformMCP(nonce); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "start platform MCP server: %v", err)
	}
	// §9.3 lines 142-164: re-open the per-connector MCP servers. F-9.1.2.
	s.startConnectorMCPServers(sessionID, nonce, connectors)
	if err := s.Runtime.Start(ctx, sessionID); err != nil {
		s.releaseSession()
		return nil, status.Errorf(codes.Internal, "start runtime: %v", err)
	}
	// spec: §4.4 / §7.2 ResumeMode — the adapter restored the workspace
	// from the named full checkpoint. The §10.1 partial-manifest reassembly
	// is gateway-driven; the adapter never assembles partials directly, so
	// it reports `full` here unconditionally. The gateway's
	// `classifyResume` upgrades this to `partial_workspace` /
	// `conversation_only` when its own lookups indicate so. F-7.3.22.
	mode := string(checkpoint.ResumeFull)
	return &adapterv1.ResumeResponse{
		RestoredBytes:      restored,
		Mode:               mode,
		RecoveryGeneration: req.GetRecoveryGeneration(),
	}, nil
}
