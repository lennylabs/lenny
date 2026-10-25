// SPDX-License-Identifier: MIT

package tier0_static

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// retiredJWTSignerSymbols matches, word-bounded, the identifiers of the
// per-process HMAC signer and the in-process rotating verifier that the
// §10.2 key set replaced: the rotating verifier type, the constructor that
// reloaded an envelope-sealed HMAC signing key, and the retired-key
// rejection reason. §10.2 "Verifier key set and rotation" holds rotation
// state in the KMS alias map and rejects only missing_kid and unknown_kid,
// so none of these names a surface that exists.
var retiredJWTSignerSymbols = regexp.MustCompile(`\b(RotatingVerifier|KMSSignerFromSealed|key_retired)\b`)

// retiredJWTSignerRoots are the trees the gate scans, relative to the
// repository root.
var retiredJWTSignerRoots = []string{"pkg", "cmd"}

// TestRetiredJWTSignerSymbolsAbsentFromProductionGo asserts that no
// non-test Go file under pkg/ or cmd/ names a retired JWT signer or
// verifier symbol, in code or in a comment. The match covers comments
// deliberately: a doc comment that still names the rotating verifier tells
// a reader that verification rotates keys in process, which §10.2 no
// longer describes. A failure is fixed by deleting the reference or
// rewording the comment to name the key set, never by narrowing this gate
// to code.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation".
func TestRetiredJWTSignerSymbolsAbsentFromProductionGo(t *testing.T) {
	root := schematest.RepoRoot(t)
	for _, dir := range retiredJWTSignerRoots {
		for _, hit := range retiredSymbolHits(t, filepath.Join(root, dir)) {
			t.Errorf("%s: names a retired JWT signer symbol; reword or delete the reference (§10.2 key set)", strings.TrimPrefix(hit, root+string(filepath.Separator)))
		}
	}
}

// TestRetiredJWTSignerSymbolPatternIsWordBounded pins the matcher: it
// reports each retired symbol on its own and inside a comment, and it
// does not report an identifier that merely contains one as a substring.
//
// spec: §10.2 (Authentication) "Verifier key set and rotation".
func TestRetiredJWTSignerSymbolPatternIsWordBounded(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"rotatingVerifiers := 2", false},
		{"var v *jwt.RotatingVerifier", true},
		{"// built over RotatingVerifier members", true},
		{"jwt.KMSSignerFromSealed(ctx, p, a, k, s)", true},
		{`case "missing_kid", "unknown_kid", "key_retired":`, true},
		{"isKMSSignerFromSealedPath := true", false},
		{"key_retired_total", false},
		{`case "missing_kid", "unknown_kid":`, false},
	}
	for _, c := range cases {
		if got := retiredJWTSignerSymbols.MatchString(c.line); got != c.want {
			t.Errorf("match(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

// retiredSymbolHits walks dir and returns file:line for every line of a
// non-test Go file that matches retiredJWTSignerSymbols. It skips testdata
// directories, whose fixtures record a tree as it was written.
func retiredSymbolHits(t *testing.T, dir string) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if retiredJWTSignerSymbols.MatchString(line) {
				hits = append(hits, path+":"+itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return hits
}
