// SPDX-License-Identifier: MIT

package podsession

import (
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// WorkspacePlanToProto converts a parsed §14 WorkspacePlan into the
// adapter-protocol WorkspacePlan the gateway sends to a pod's adapter
// in StartSession. Each §14 source variant is flattened onto the
// adapterv1.WorkspaceSource union message; an unrecognized variant
// yields a source carrying only its type, which the adapter rejects
// at materialization. A nil result is never returned — an empty plan
// converts to an empty WorkspacePlan.
func WorkspacePlanToProto(p workspaceplan.Plan) *adapterv1.WorkspacePlan {
	out := &adapterv1.WorkspacePlan{SchemaVersion: int32(p.SchemaVersion)}
	for _, src := range p.Sources {
		ws := &adapterv1.WorkspaceSource{Type: src.Type}
		switch v := src.Variant.(type) {
		case workspaceplan.InlineFile:
			ws.Path = v.PathField
			ws.Content = v.Content
			ws.Mode = v.Mode
		case workspaceplan.UploadFile:
			ws.Path = v.PathField
			ws.UploadRef = v.UploadRef
			ws.Mode = v.Mode
		case workspaceplan.UploadArchive:
			ws.Path = v.PathPrefix
			ws.UploadRef = v.UploadRef
			ws.Format = v.Format
			ws.StripComponents = int32(v.StripComponents)
		case workspaceplan.Mkdir:
			ws.Path = v.PathField
			ws.Mode = v.Mode
		case workspaceplan.GitClone:
			ws.Path = v.PathField
			ws.Url = v.URL
			ws.GitRef = v.Ref
			ws.Depth = int32(v.Depth)
			ws.Submodules = v.Submodules
			ws.ResolvedCommitSha = v.ResolvedCommitSha
			if v.Auth != nil {
				ws.Auth = &adapterv1.GitAuth{Mode: v.Auth.Mode, LeaseScope: v.Auth.LeaseScope}
			}
		}
		out.Sources = append(out.Sources, ws)
	}
	for _, sc := range p.SetupCommands {
		out.SetupCommands = append(out.SetupCommands, &adapterv1.SetupCommand{
			Cmd:            sc.Cmd,
			TimeoutSeconds: int32(sc.TimeoutSeconds),
		})
	}
	return out
}
