// SPDX-License-Identifier: MIT

package tier11_docs_test

import (
	"strings"
	"testing"
)

// treeBudgetReturnDocFiles are the files whose doc comments describe the
// delegation tree budget return, with a phrase each must name.
var treeBudgetReturnDocFiles = []struct {
	path string
	want string
}{
	{path: "pkg/gateway/sessionserver/delegation_seams.go", want: "returnTreeBudget"},
	{path: "pkg/gateway/mcpfabric/delegationtree/treebudget/treebudget.go", want: "post-terminal hook"},
}

// retiredArchiveReturnFragment names the archive-time budget return the
// terminal pipeline's return step replaced.
const retiredArchiveReturnFragment = "completed-subtree offload"

// spec: §8.3 (Return on completion)
// diagnosis: the TreeBudgetReturner or treebudget.Reserver.Return doc still
// describes the budget return as happening on completed-subtree offload, or no
// longer names the post-terminal hook's return step. The return runs once per
// applied terminal write of a delegated child, after the usage quiescence wait,
// and archiving a node returns nothing; rewrite the doc to say so.
func TestTreeBudgetReturnDocsNameTheTerminalPipelineReturn_spec_8_3(t *testing.T) {
	root := repoRoot(t)
	for _, f := range treeBudgetReturnDocFiles {
		body := readRepoFile(t, root, f.path)
		if strings.Contains(body, retiredArchiveReturnFragment) {
			t.Errorf("%s still describes the retired %q return", f.path, retiredArchiveReturnFragment)
		}
		if !strings.Contains(body, f.want) {
			t.Errorf("%s does not name %q", f.path, f.want)
		}
	}
}
