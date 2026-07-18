// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"io"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

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
	// spec: §10.1 line 155 — the gateway resolves the checkpoint's chunk
	// set and hands the adapter one presigned single-key GET capability per
	// chunk on ResumeRequest.chunks; the adapter fetches them directly from
	// object storage. A restore that carries chunks needs the transport; a
	// conversation-only or coordinator-handoff resume carries none.
	if len(req.GetChunks()) > 0 && s.CheckpointTransport == nil {
		return nil, status.Error(codes.FailedPrecondition,
			"adapter is not configured with a checkpoint transport")
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

	// spec: §7.3 line 408 step (e) "Replay latest workspace checkpoint" +
	// line 409 step (f) "Restore session file to expected path" + §10.1
	// line 155 reassembly. The gateway resolved the checkpoint's chunk set
	// and passed one presigned GET capability per index; the adapter
	// fetches them in ascending index order, concatenates the chunk bodies
	// into a single tar (or tar.gz) byte stream, and ExtractTree replays
	// the workspace under workspace.WorkspacePrefix and the §6.4 line 380
	// /sessions session file under workspace.SessionsPrefix. A resume that
	// carries no chunks (conversation-only) restores nothing.
	restored, extractErr := s.restoreChunks(ctx, req.GetChunks())
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

// restoreChunks fetches each presigned GET capability in ascending index
// order, concatenates the chunk bodies into one byte stream, and extracts
// the checkpoint bundle into the checkpoint roots. It returns the
// uncompressed bytes restored. An empty chunk set restores nothing (a
// conversation-only or coordinator-handoff resume), which is not an error.
//
// spec: §10.1 line 155 — the concatenation of all chunks in index order is
// consumed as a single tar (or tar.gz) stream fed end-to-end into one
// decompress→untar pipeline; chunk boundaries are never parsed in
// isolation.
func (s *Server) restoreChunks(ctx context.Context, chunks []*adapterv1.ChunkGrant) (int64, error) {
	if len(chunks) == 0 {
		return 0, nil
	}
	ordered := make([]*adapterv1.ChunkGrant, len(chunks))
	copy(ordered, chunks)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].GetIndex() < ordered[j].GetIndex() })

	pr, pw := io.Pipe()
	go func() {
		for _, ch := range ordered {
			rc, err := s.CheckpointTransport.GetChunk(ctx, ch.GetUrl(), ch.GetHeaders())
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			_, cerr := io.Copy(pw, rc)
			_ = rc.Close()
			if cerr != nil {
				_ = pw.CloseWithError(cerr)
				return
			}
		}
		_ = pw.Close()
	}()

	restored, extractErr := workspace.ExtractTree(s.checkpointRoots(), pr)
	_ = pr.Close()
	return restored, extractErr
}
