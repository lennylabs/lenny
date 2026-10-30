// SPDX-License-Identifier: MIT

// Tier-11 consistency check for the checkpoint scoping key. §10.1 keys the
// partial manifest, the supersede rule, and the reassembly predicate on
// session_id alone, §12.5 states retention and supersession on the same key,
// and §5.2's per-slot checkpoint cap reads the session's last measured
// workspace size from the session row. The two-column `(session_id, slot_id)`
// pair names no key any table carries once the persisted duplicate columns are
// dropped, so no normative statement of the key and no reader-facing mirror of
// one may spell it.
//
// spec: 5.2 (per-slot checkpoint cap and preStop budget), 10.1 (partial
// manifest scoping key), 12.5 (checkpoint retention and supersession)

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// twoColumnScopingKey matches the retired `(session_id, slot_id)` pair in the
// spellings prose uses for it, with or without the code-span backticks and with
// any run of spaces after the comma.
var twoColumnScopingKey = regexp.MustCompile(`\(\s*` + "`?" + `session_id` + "`?" + `\s*,\s*` + "`?" + `slot_id` + "`?" + `\s*\)`)

// markdownUnder returns every `.md` file under the named repository
// directories, so a check states its domain as a directory set rather than as
// a file list that ages out as sections are added.
func markdownUnder(t *testing.T, root string, dirs ...string) []string {
	t.Helper()
	var files []string
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return files
}

// spec: 5.2, 10.1, 12.5
// diagnosis: a normative statement of the checkpoint scoping key, or a
//
//	reader-facing mirror of one, still spells the retired
//	`(session_id, slot_id)` pair. The checkpoint tables are keyed on session_id
//	alone and carry no slot_id column, so a site naming the pair sends a reader
//	to a key no index and no table has. A failure here names the file and line
//	that has to be restated on the session.
func TestCheckpointScopingKeyNamesTheSessionAlone(t *testing.T) {
	root := repoRoot(t)
	for _, path := range markdownUnder(t, root, "spec", "docs") {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if twoColumnScopingKey.MatchString(line) {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				t.Errorf("%s:%d states the checkpoint scoping key as the retired (session_id, slot_id) pair; restate it on session_id alone", rel, i+1)
			}
		}
	}
}

// spec: 5.2
// diagnosis: §5.2's per-slot checkpoint cap no longer reads the cap input from
//
//	the session. The cap is selected from last_checkpoint_workspace_bytes,
//	which migrations/0087_sessions_retry_policy.up.sql puts on the session row,
//	and the preStop budget sums those caps across the sessions active on the
//	pod. A failure here means the bullet names a source the schema does not
//	carry, or derives the budget from a per-slot key again.
func TestCheckpointCapReadsTheSessionsWorkspaceSize(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "spec", "05_runtime-registry-and-pool-model.md"))
	if err != nil {
		t.Fatalf("read spec/05: %v", err)
	}
	for _, want := range []string{
		"(`last_checkpoint_workspace_bytes` for the session in Postgres)",
		"the **sum** of the per-session caps across the sessions active on the pod",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("spec/05 §5.2 does not state %q, so the checkpoint cap is no longer stated on the session", want)
		}
	}
}
