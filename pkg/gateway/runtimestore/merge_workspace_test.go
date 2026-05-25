// SPDX-License-Identifier: MIT

package runtimestore_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// TestMergeWorkspaceDefaultsAppends covers the §5.1 line 197-198 Append
// merge: derived files are appended onto base files with a conflicting
// path replaced by the derived entry, and derived setup commands are
// appended after the base setup commands.
func TestMergeWorkspaceDefaultsAppends(t *testing.T) {
	base := runtimestore.Runtime{
		Name: "base",
		WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files: []runtimestore.WorkspaceFile{
				{Path: "base.py", Content: "base"},
				{Path: "shared.py", Content: "from-base"},
			},
			SetupCommands: []runtimestore.WorkspaceSetupCommand{{Cmd: "pip install base"}},
		},
	}
	derived := runtimestore.Runtime{
		Name: "d", BaseRuntime: "base",
		WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files: []runtimestore.WorkspaceFile{
				{Path: "shared.py", Content: "from-derived"}, // conflicting path
				{Path: "agent.py", Content: "derived"},       // derived-only
			},
			SetupCommands: []runtimestore.WorkspaceSetupCommand{{Cmd: "pip install derived", TimeoutSeconds: 30}},
		},
	}
	eff := runtimestore.Merge(base, derived)
	wd := eff.WorkspaceDefaults
	if wd == nil || len(wd.Files) != 3 {
		t.Fatalf("expected 3 merged files (base.py, shared.py replaced, agent.py): %+v", wd)
	}
	// Order: base entries first (shared.py replaced in place), then
	// derived-only entries.
	if wd.Files[0].Path != "base.py" || wd.Files[1].Path != "shared.py" || wd.Files[2].Path != "agent.py" {
		t.Errorf("unexpected file order: %+v", wd.Files)
	}
	if wd.Files[1].Content != "from-derived" {
		t.Errorf("conflicting path shared.py must be replaced by derived content: %+v", wd.Files[1])
	}
	if len(wd.SetupCommands) != 2 || wd.SetupCommands[0].Cmd != "pip install base" || wd.SetupCommands[1].Cmd != "pip install derived" {
		t.Errorf("setupCommands must be base then derived: %+v", wd.SetupCommands)
	}
	if wd.SetupCommands[1].TimeoutSeconds != 30 {
		t.Errorf("per-command timeoutSeconds must be preserved from each source: %+v", wd.SetupCommands[1])
	}
}

// TestMergeWorkspaceDefaultsInheritsAndDoesNotAlias covers a derived
// runtime that omits the block inheriting the base, and confirms the
// merge result does not alias the base file slice.
func TestMergeWorkspaceDefaultsInheritsAndDoesNotAlias(t *testing.T) {
	base := runtimestore.Runtime{
		Name: "base",
		WorkspaceDefaults: &runtimestore.WorkspaceDefaults{
			Files: []runtimestore.WorkspaceFile{{Path: "base.py", Content: "base"}},
		},
	}
	eff := runtimestore.Merge(base, runtimestore.Runtime{Name: "d", BaseRuntime: "base"})
	if eff.WorkspaceDefaults == nil || len(eff.WorkspaceDefaults.Files) != 1 {
		t.Fatalf("derived must inherit base workspaceDefaults when unset: %+v", eff.WorkspaceDefaults)
	}
	eff.WorkspaceDefaults.Files[0].Content = "tampered"
	if base.WorkspaceDefaults.Files[0].Content != "base" {
		t.Error("Merge result aliases the base workspaceDefaults.files slice")
	}
}

// TestMergeSharedAssetsAppends covers the §5.1 line 208 Append merge:
// derived assets are appended with a conflicting destPath replaced by
// the derived entry.
func TestMergeSharedAssetsAppends(t *testing.T) {
	base := runtimestore.Runtime{
		Name: "base",
		SharedAssets: []runtimestore.SharedAsset{
			{Type: runtimestore.SharedAssetArtifact, Ref: "lenny-blob://base", DestPath: "models/"},
			{Type: runtimestore.SharedAssetInline, Path: "c.json", Content: "base", DestPath: "config.json"},
		},
	}
	derived := runtimestore.Runtime{
		Name: "d", BaseRuntime: "base",
		SharedAssets: []runtimestore.SharedAsset{
			{Type: runtimestore.SharedAssetInline, Path: "c.json", Content: "derived", DestPath: "config.json"}, // conflict
			{Type: runtimestore.SharedAssetArtifact, Ref: "lenny-blob://extra", DestPath: "extra/"},             // new
		},
	}
	eff := runtimestore.Merge(base, derived)
	if len(eff.SharedAssets) != 3 {
		t.Fatalf("expected 3 merged shared assets: %+v", eff.SharedAssets)
	}
	if eff.SharedAssets[1].DestPath != "config.json" || eff.SharedAssets[1].Content != "derived" {
		t.Errorf("conflicting destPath must be replaced by derived: %+v", eff.SharedAssets[1])
	}
	if eff.SharedAssets[2].DestPath != "extra/" {
		t.Errorf("derived-only asset must be appended: %+v", eff.SharedAssets[2])
	}
}

// TestMergeRuntimeOptionsSchemaOverride covers the §5.1 line 199
// Override merge: a derived schema replaces the base, and an absent
// derived schema inherits the base.
func TestMergeRuntimeOptionsSchemaOverride(t *testing.T) {
	base := runtimestore.Runtime{
		Name:                 "base",
		RuntimeOptionsSchema: json.RawMessage(`{"properties":{"model":{}}}`),
	}
	// Inherit when derived omits.
	inherit := runtimestore.Merge(base, runtimestore.Runtime{Name: "d", BaseRuntime: "base"})
	if string(inherit.RuntimeOptionsSchema) != `{"properties":{"model":{}}}` {
		t.Errorf("derived must inherit base runtimeOptionsSchema when unset: %s", inherit.RuntimeOptionsSchema)
	}
	// Override when derived sets.
	override := runtimestore.Merge(base, runtimestore.Runtime{
		Name: "d2", BaseRuntime: "base",
		RuntimeOptionsSchema: json.RawMessage(`{"properties":{"model":{"type":"string"}}}`),
	})
	if string(override.RuntimeOptionsSchema) != `{"properties":{"model":{"type":"string"}}}` {
		t.Errorf("derived runtimeOptionsSchema must replace base: %s", override.RuntimeOptionsSchema)
	}
}

// TestWorkspaceSetupCommandUnmarshalBareString covers the §5.1 YAML
// bare-string form for a setup command.
func TestWorkspaceSetupCommandUnmarshalBareString(t *testing.T) {
	var got runtimestore.WorkspaceDefaults
	if err := json.Unmarshal([]byte(`{"setupCommands":["pip install -r requirements.txt",{"cmd":"make","timeoutSeconds":60}]}`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.SetupCommands) != 2 {
		t.Fatalf("expected 2 setup commands: %+v", got.SetupCommands)
	}
	if got.SetupCommands[0].Cmd != "pip install -r requirements.txt" || got.SetupCommands[0].TimeoutSeconds != 0 {
		t.Errorf("bare-string command must parse with zero timeout: %+v", got.SetupCommands[0])
	}
	if got.SetupCommands[1].Cmd != "make" || got.SetupCommands[1].TimeoutSeconds != 60 {
		t.Errorf("object command must preserve timeout: %+v", got.SetupCommands[1])
	}
}
