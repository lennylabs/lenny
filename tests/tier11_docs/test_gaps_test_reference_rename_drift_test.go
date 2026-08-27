// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the coverage audit at TEST-GAPS.md and the
// test functions it names as evidence.
//
// A resolved finding in the audit cites the case that closed it as a
// `path/to/file_test.go::TestName` reference. A reader chasing that evidence
// opens the file and looks for the function. When a test function is renamed
// so that its name agrees with its own `// spec:` citation, every audit
// reference to the former name resolves to no function in the tree and the
// evidence trail ends.
//
// This check pins that one drift class: a reference whose file exists, whose
// named function does not, and whose name without its trailing `_spec_X_Y`
// suffix does resolve in that same file under a different suffix. That
// combination is a rename the audit did not follow, and the corrected name is
// the one the file declares.
//
// The check is scoped to spec-suffix rename drift. Holding every audit
// reference to a declared function requires reconciling the references that
// name a planned case, a bare file name with no directory, or a case that
// moved packages, which is its own sweep.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 7.1 (session lifecycle preparation barrier)

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// testFuncReference matches a `path::TestName` evidence reference in the
	// audit. Only a reference carrying a directory is considered, because a
	// bare file name does not name a location the check can resolve.
	testFuncReference = regexp.MustCompile(`([A-Za-z0-9_./-]+/[A-Za-z0-9_.-]+_test\.go)::(Test[A-Za-z0-9_]+)`)
	// specSuffix matches the trailing spec-section suffix a test name carries.
	specSuffix = regexp.MustCompile(`_spec_[0-9][0-9_]*$`)
)

// declaredFunc reports whether the file declares the named function.
func declaredFunc(body, name string) bool {
	return regexp.MustCompile(`func\s+` + regexp.QuoteMeta(name) + `\(`).MatchString(body)
}

// renamedSuffixes returns the names the file declares for a stem, under any
// spec-section suffix.
func renamedSuffixes(body, stem string) []string {
	m := regexp.MustCompile(`func\s+(`+regexp.QuoteMeta(stem)+`_spec_[0-9][0-9_]*)\(`).FindAllStringSubmatch(body, -1)
	names := make([]string, 0, len(m))
	for _, hit := range m {
		names = append(names, hit[1])
	}
	return names
}

// spec: 7.1
// diagnosis: TEST-GAPS.md cites a test function under a name the tree no
//
//	longer declares, while the same file declares that name under a different
//	`_spec_X_Y` suffix. A test function was renamed to agree with its own
//	`// spec:` citation and the audit reference was not swept with it, so the
//	evidence that closed the finding cannot be found from the audit. Fix the
//	audit reference to the name the file declares; do not rename the function
//	back, because its name follows its citation.
func TestTestGapsTestReferencesFollowSpecSuffixRenames(t *testing.T) {
	root := repoRoot(t)
	body := readDocPage(t, filepath.Join(root, "TEST-GAPS.md"))

	sources := map[string]string{}
	for _, ref := range testFuncReference.FindAllStringSubmatch(body, -1) {
		path, name := ref[1], ref[2]
		stem := specSuffix.ReplaceAllString(name, "")
		if stem == name {
			continue
		}
		src, seen := sources[path]
		if !seen {
			b, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				// A reference to a file that is not on disk names a planned or
				// relocated case; that class is out of this check's scope.
				src = ""
			} else {
				src = string(b)
			}
			sources[path] = src
		}
		if src == "" || declaredFunc(src, name) {
			continue
		}
		if declared := renamedSuffixes(src, stem); len(declared) > 0 {
			t.Errorf("TEST-GAPS.md cites %s::%s, which %s does not declare; the file declares %s",
				path, name, path, strings.Join(declared, ", "))
		}
	}
}
