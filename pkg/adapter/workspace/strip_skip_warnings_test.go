// SPDX-License-Identifier: MIT

package workspace_test

import (
	"testing"
)

// F-7.4.15: §7.4 line 459 — entries with fewer than stripComponents
// segments are skipped without aborting extraction and emit a
// workspace_plan_strip_components_skip warning event per skipped entry.
func TestMaterializeUploadArchive_TarEmitsStripSkipWarning_spec_7_4_15(t *testing.T) {
	// strip=2 on an entry with 1 leading segment (top-level "readme") +
	// a normal nested entry produces exactly one warning.
	archive := buildTar(t, false, map[string]string{
		"readme.md":                "skipped — fewer than 2 segments",
		"prefix/sub/keep/file.txt": "extracted",
	})
	_, warnings, err := materializeArchiveWithWarnings(t, "tar", "", 2, archive)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings: got %d, want 1; warnings=%+v", len(warnings), warnings)
	}
	if got := warnings[0].Code; got != "workspace_plan_strip_components_skip" {
		t.Errorf("warning.Code = %q, want workspace_plan_strip_components_skip", got)
	}
	if warnings[0].Entry != "readme.md" {
		t.Errorf("warning.Entry = %q, want readme.md", warnings[0].Entry)
	}
}

// F-7.4.15: the zip variant emits the same warning per skipped entry.
func TestMaterializeUploadArchive_ZipEmitsStripSkipWarning_spec_7_4_15(t *testing.T) {
	archive := buildZip(t, map[string]string{
		"top.txt":        "skipped",
		"prefix/sub/ok":  "extracted",
		"another-top.md": "also skipped",
	})
	_, warnings, err := materializeArchiveWithWarnings(t, "zip", "", 2, archive)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings: got %d, want 2; warnings=%+v", len(warnings), warnings)
	}
	for i, w := range warnings {
		if w.Code != "workspace_plan_strip_components_skip" {
			t.Errorf("warning[%d].Code = %q, want workspace_plan_strip_components_skip", i, w.Code)
		}
		if w.Entry == "" {
			t.Errorf("warning[%d].Entry empty", i)
		}
	}
}

// F-7.4.15: with strip=0 no skip warning is ever produced, because no
// entry can have fewer than 0 segments. spec: §14 stripComponents
// algorithm.
func TestMaterializeUploadArchive_NoStripNoWarnings_spec_7_4_15(t *testing.T) {
	archive := buildTar(t, false, map[string]string{
		"top.txt":   "a",
		"sub/x.txt": "b",
	})
	_, warnings, err := materializeArchiveWithWarnings(t, "tar", "", 0, archive)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings: got %d, want 0; warnings=%+v", len(warnings), warnings)
	}
}
