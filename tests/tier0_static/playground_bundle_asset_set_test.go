// SPDX-License-Identifier: MIT

package tier0_static

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// testOnlyDirNames are directory names inside the SPA bundle whose
// whole contents are test material rather than browser assets.
var testOnlyDirNames = map[string]bool{
	"__tests__":    true,
	"__mocks__":    true,
	"test":         true,
	"tests":        true,
	"spec":         true,
	"fixtures":     true,
	"node_modules": true,
}

// isTestOnlyAssetName reports whether a bundle file name is a
// test-only file rather than a browser asset. It covers the naming
// conventions the Node and browser toolchains use for unit tests and
// their configuration: `x.test.js`, `x.spec.ts`, `x_test.js`, and
// per-suite config files such as `jest.config.js`.
func isTestOnlyAssetName(base string) bool {
	name := strings.ToLower(base)
	stem := name
	if dot := strings.LastIndex(name, "."); dot > 0 {
		stem = name[:dot]
	}
	switch {
	case strings.HasSuffix(stem, ".test"), strings.HasSuffix(stem, ".spec"):
		return true
	case strings.HasSuffix(stem, "_test"), strings.HasSuffix(stem, "_spec"):
		return true
	case strings.HasPrefix(name, "jest.config."), strings.HasPrefix(name, "vitest.config."),
		strings.HasPrefix(name, "karma.conf."):
		return true
	}
	return false
}

// spec: 27.7 ("Static assets (`index.html`, hashed `*.js` and `*.css`
//
//	bundles) are served from the embedded FS with long cache headers
//	(`Cache-Control: public, max-age=31536000, immutable`)."), 27.2
//	("It is compiled into the gateway binary as an embedded static
//	asset bundle (`embed.FS`) so there is no separate deployment
//	target.")
//
// diagnosis: The embedded playground SPA bundle gained a test-only
//
//	file. Everything under the embedded subtree is reachable at
//	GET /playground/<path> for any caller who clears the playground
//	auth chain, and is served with the year-long immutable cache
//	header §27.7 reserves for the SPA's own assets. §27.7 enumerates
//	that asset set as index.html plus the hashed script and style
//	bundles; a unit-test file is neither, so shipping one both
//	discloses test source to playground users and inflates the
//	gateway binary. Move the test file out of the embedded subtree
//	(a sibling directory the //go:embed pattern does not cover)
//	rather than relaxing this test.
func TestPlaygroundBundleShipsNoTestOnlyFiles(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)

	// The enumeration below reads the source directory, which is only a
	// faithful reading of what the gateway ships while assets.go embeds
	// that directory wholesale. Pin the directive first, exactly as the
	// offline-asset walk in this package does.
	assetsGo, err := os.ReadFile(filepath.Join(root, playgroundAssetsGo))
	if err != nil {
		t.Fatalf("read %s: %v", playgroundAssetsGo, err)
	}
	if !strings.Contains(string(assetsGo), "//go:embed ui\n") {
		t.Fatalf("%s no longer carries the `//go:embed ui` directive; this test enumerates %s on disk as a stand-in for the embedded bundle and that equivalence must hold",
			playgroundAssetsGo, playgroundUIDir)
	}
	if n := strings.Count(string(assetsGo), "//go:embed"); n != 1 {
		t.Fatalf("%s carries %d //go:embed directives, want exactly 1; a second embed root would ship assets this test does not enumerate",
			playgroundAssetsGo, n)
	}

	uiRoot := filepath.Join(root, playgroundUIDir)
	walkErr := filepath.WalkDir(uiRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(uiRoot, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		base := strings.ToLower(filepath.Base(rel))
		if d.IsDir() {
			if testOnlyDirNames[base] {
				t.Errorf("%s/%s is a test-only directory inside the embedded playground bundle; every file under it is served at GET /playground/%s/... to any caller who clears the playground auth chain, and §27.7 serves only index.html and the hashed script and style bundles from the embedded FS",
					playgroundUIDir, rel, rel)
			}
			return nil
		}
		if isTestOnlyAssetName(base) {
			t.Errorf("%s/%s is a test-only file inside the embedded playground bundle; the gateway serves it at GET /playground/%s with the §27.7 year-long immutable cache header to any caller who clears the playground auth chain, and §27.7 serves only index.html and the hashed script and style bundles from the embedded FS. Move it to a sibling directory outside the //go:embed pattern.",
				playgroundUIDir, rel, rel)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", playgroundUIDir, walkErr)
	}
}
