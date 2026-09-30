// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// publishWorkspaceWarnings emits one §14 `workspace_plan_warning`
// frame per advisory the adapter returned from FinalizeWorkspace. The
// frame goes on the per-session SSE bus (so clients subscribing to
// /v1/sessions/{id}/events see it) and on the §16.6 / §25.3
// operational-event stream (so Ops console and AI DevOps agents see
// it asynchronously). The emitted payload carries the §14
// per-warning structured fields (`entryPath`, `segmentCount`,
// `stripComponents`) so a consumer that matches on these can extract
// them without parsing the free-form message.
//
// spec: §7.4; §14; §16.6 catalogue. F-7.4.15,
// F-14.1.17, F-14.1.18.
func (s *Server) publishWorkspaceWarnings(result *podsession.BindResult) {
	if result == nil {
		return
	}
	s.publishWorkspacePlanWarnings(result.TenantID, result.SessionID, result.WorkspacePlanWarnings)
}

// publishWorkspacePlanWarnings emits one §14 `workspace_plan_warning` frame
// per advisory the §4.7 materialization raised, on the per-session SSE bus
// and the §16.6 / §25.3 operational-event stream. The /start launch path
// (via publishWorkspaceWarnings on the BindResult) and the §4.3 finalize
// prepare barrier (on the PrepareResult) both republish their warnings
// through it, so the strip-components-skip advisory reaches subscribers
// regardless of which boundary materialized the workspace. spec: §7.4; §14; §16.6 catalogue. F-7.4.15, F-14.1.17, F-14.1.18.
func (s *Server) publishWorkspacePlanWarnings(tenantID, sessionID string, warnings []*adapterv1.WorkspacePlanWarning) {
	for _, w := range warnings {
		if w == nil {
			continue
		}
		payload := map[string]any{
			"code":            w.GetCode(),
			"sourceIndex":     w.GetSourceIndex(),
			"entryPath":       w.GetEntryPath(),
			"segmentCount":    w.GetSegmentCount(),
			"stripComponents": w.GetStripComponents(),
			"message":         w.GetMessage(),
		}
		// spec: §14 — the materializer-raised
		// `workspace_plan_unknown_source_type` warning carries
		// `unknownType` + `schemaVersion`; surface them only when set so
		// the other warning codes keep their existing payload. F-14.1.2.
		if w.GetUnknownType() != "" {
			payload["unknownType"] = w.GetUnknownType()
		}
		if w.GetSchemaVersion() != 0 {
			payload["schemaVersion"] = w.GetSchemaVersion()
		}
		// spec: §14 — the materialization-time
		// `workspace_plan_path_collision` warning carries `path`,
		// `winningSourceIndex`, `losingSourceIndex`. A non-empty path is
		// the discriminator: only collision warnings set it, so the other
		// codes keep their existing payload. F-14.1.9.
		if w.GetPath() != "" {
			payload["path"] = w.GetPath()
			payload["winningSourceIndex"] = w.GetWinningSourceIndex()
			payload["losingSourceIndex"] = w.GetLosingSourceIndex()
		}
		s.publishEvent(tenantID, sessionID, "workspace_plan_warning", payload)
		s.emitWorkspacePlanWarningOps(tenantID, sessionID, payload)
	}
}

// emitWorkspacePlanWarningOps publishes a §14 warning on the §16.6 /
// §25.3 operational-event stream so Ops/audit subscribers see the
// warning without having to subscribe to the per-session SSE feed.
// No-op when the OpsEmitter is not wired (tests).
//
// spec: §14; §16.6 catalogue. F-14.1.17.
func (s *Server) emitWorkspacePlanWarningOps(tenantID, sessionID string, payload map[string]any) {
	if s.opsEmitter == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte("{}")
	}
	subject := "session/" + sessionID
	_ = s.opsEmitter.Emit(context.Background(), events.OperationalEvent{
		Source:          "/v1/sessions",
		Subject:         subject,
		Type:            events.EventWorkspacePlanWarning.CloudEventsType(),
		Severity:        "info",
		DataContentType: "application/json",
		Data:            data,
	})
	_ = tenantID
}

// publishParsePlanWarnings emits one SSE `workspace_plan_warning`
// frame per §14 advisory the workspaceplan parser raised on
// CreateSession ingest (`workspace_plan_unknown_source_type`,
// `workspace_plan_path_collision`). The §14 spec calls each warning
// an "event" the gateway emits — the create response already echoes
// the same slice, but operators (Ops console, audit pipelines, AI
// DevOps agents per §25) cannot observe them asynchronously unless
// the gateway publishes them on the same per-session SSE bus the
// strip-components-skip warnings ride.
//
// spec: §14; §14 WarningCode. F-14.1.17,
// F-14.1.18.
func (s *Server) publishParsePlanWarnings(tenantID, sessionID string, warnings []workspaceplan.Warning) {
	if sessionID == "" || len(warnings) == 0 {
		return
	}
	for _, w := range warnings {
		payload := map[string]any{
			"code":        string(w.Code),
			"sourceIndex": w.SourceIndex,
			"message":     w.Message,
		}
		if w.Field != "" {
			payload["field"] = w.Field
		}
		// spec: §14 — unknown_source_type fields:
		// `schemaVersion`, `unknownType`.
		if w.SchemaVersion != nil {
			payload["schemaVersion"] = *w.SchemaVersion
		}
		if w.UnknownType != "" {
			payload["unknownType"] = w.UnknownType
		}
		// spec: §14 — path_collision fields: `path`,
		// `winningSourceIndex`, `losingSourceIndex`.
		if w.Path != "" {
			payload["path"] = w.Path
		}
		if w.WinningSourceIndex != nil {
			payload["winningSourceIndex"] = *w.WinningSourceIndex
		}
		if w.LosingSourceIndex != nil {
			payload["losingSourceIndex"] = *w.LosingSourceIndex
		}
		s.publishEvent(tenantID, sessionID, "workspace_plan_warning", payload)
		s.emitWorkspacePlanWarningOps(tenantID, sessionID, payload)
	}
}
