// SPDX-License-Identifier: MIT

package podsession_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

func TestWorkspacePlanToProtoConvertsSourcesAndSetup(t *testing.T) {
	plan := workspaceplan.Plan{
		SchemaVersion: 1,
		Sources: []workspaceplan.Source{
			{Type: "inlineFile", Variant: workspaceplan.InlineFile{
				PathField: "CLAUDE.md", Content: "# instructions", Mode: "0644",
			}},
			{Type: "mkdir", Variant: workspaceplan.Mkdir{PathField: "out/"}},
			{Type: "uploadArchive", Variant: workspaceplan.UploadArchive{
				PathPrefix: ".", UploadRef: "upload_x", Format: "tar.gz", StripComponents: 1,
			}},
		},
		SetupCommands: []workspaceplan.SetupCommand{{Cmd: "npm ci", TimeoutSeconds: 300}},
	}

	got := podsession.WorkspacePlanToProto(plan)
	if got.GetSchemaVersion() != 1 {
		t.Errorf("schema version = %d, want 1", got.GetSchemaVersion())
	}
	if len(got.GetSources()) != 3 {
		t.Fatalf("sources = %d, want 3", len(got.GetSources()))
	}
	inline := got.GetSources()[0]
	if inline.GetType() != "inlineFile" || inline.GetPath() != "CLAUDE.md" ||
		inline.GetContent() != "# instructions" || inline.GetMode() != "0644" {
		t.Errorf("inlineFile source = %+v, want the converted §14 fields", inline)
	}
	archive := got.GetSources()[2]
	if archive.GetUploadRef() != "upload_x" || archive.GetFormat() != "tar.gz" ||
		archive.GetStripComponents() != 1 || archive.GetPath() != "." {
		t.Errorf("uploadArchive source = %+v, want the converted §14 fields", archive)
	}
	if len(got.GetSetupCommands()) != 1 || got.GetSetupCommands()[0].GetCmd() != "npm ci" ||
		got.GetSetupCommands()[0].GetTimeoutSeconds() != 300 {
		t.Errorf("setup commands = %+v, want one npm ci command", got.GetSetupCommands())
	}
}

func TestWorkspacePlanToProtoConvertsGitClone(t *testing.T) {
	plan := workspaceplan.Plan{
		SchemaVersion: 1,
		Sources: []workspaceplan.Source{
			{Type: "gitClone", Variant: workspaceplan.GitClone{
				URL: "https://github.com/acme/repo", Ref: "main", PathField: "repo",
				Depth: 1, Submodules: true,
				Auth: &workspaceplan.GitCloneAuth{Mode: "credential-lease", LeaseScope: "vcs.github.read"},
			}},
		},
	}

	src := podsession.WorkspacePlanToProto(plan).GetSources()[0]
	if src.GetUrl() != "https://github.com/acme/repo" || src.GetGitRef() != "main" ||
		src.GetDepth() != 1 || !src.GetSubmodules() {
		t.Errorf("gitClone source = %+v, want the converted §14 fields", src)
	}
	if src.GetAuth().GetMode() != "credential-lease" || src.GetAuth().GetLeaseScope() != "vcs.github.read" {
		t.Errorf("gitClone auth = %+v, want the converted §14 auth", src.GetAuth())
	}
}

func TestWorkspacePlanToProtoEmptyPlan(t *testing.T) {
	got := podsession.WorkspacePlanToProto(workspaceplan.Plan{SchemaVersion: 1})
	if got == nil {
		t.Fatal("conversion returned nil for an empty plan")
	}
	if len(got.GetSources()) != 0 || len(got.GetSetupCommands()) != 0 {
		t.Errorf("empty plan converted to %+v, want no sources or commands", got)
	}
}
