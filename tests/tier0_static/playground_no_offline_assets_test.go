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

// playgroundUIDir is the SPA bundle the gateway compiles into its
// binary. assets.go embeds this directory wholesale, so the set of
// files on disk is the set of files shipped to the browser.
const playgroundUIDir = "pkg/gateway/mcpfabric/playground/ui"

// playgroundAssetsGo holds the //go:embed directive that makes the
// on-disk enumeration below a faithful reading of the shipped bundle.
const playgroundAssetsGo = "pkg/gateway/mcpfabric/playground/assets.go"

// offlineAssetNames are bundle file names that would give the SPA an
// offline mode: a service-worker script can precache the shell and
// serve it with the gateway unreachable, an appcache manifest does the
// same on older engines, and a web-app manifest declares an installable
// scope whose whole purpose is running detached from the origin server.
var offlineAssetNames = map[string]string{
	"service-worker.js": "a service worker can precache the SPA shell and serve it while the gateway is unreachable",
	"serviceworker.js":  "a service worker can precache the SPA shell and serve it while the gateway is unreachable",
	"sw.js":             "a service worker can precache the SPA shell and serve it while the gateway is unreachable",
	"manifest.json":     "a web-app manifest declares an installable, offline-scoped application",
	"manifest.webmanifest": "a web-app manifest declares an installable, offline-scoped " +
		"application",
	"app.webmanifest": "a web-app manifest declares an installable, offline-scoped application",
	"appcache":        "an application cache manifest serves the SPA with the origin unreachable",
}

// offlineSourceTokens are substrings whose presence in a shipped asset
// means the SPA reaches for a browser API that either survives the
// gateway going away or survives a page refresh. Each entry maps the
// token to the reason it is disallowed, which the failure message
// prints so a future author sees why rather than only what.
var offlineSourceTokens = map[string]string{
	"serviceWorker":      "registering a service worker gives the SPA an offline cache",
	"ServiceWorkerRegis": "registering a service worker gives the SPA an offline cache",
	"applicationCache":   "the application cache serves the SPA with the origin unreachable",
	"caches.open":        "the Cache Storage API stores responses for offline replay",
	"caches.match":       "the Cache Storage API stores responses for offline replay",
	"CacheStorage":       "the Cache Storage API stores responses for offline replay",
	"indexedDB":          "IndexedDB persists structured state across a refresh",
	"IndexedDB":          "IndexedDB persists structured state across a refresh",
	"localStorage":       "localStorage persists state across a refresh; the pane must clear",
	"rel=\"manifest\"":   "a linked web-app manifest declares an installable, offline-scoped application",
	"rel='manifest'":     "a linked web-app manifest declares an installable, offline-scoped application",
}

// spec: 27.1 ("No offline mode. The playground requires a live
//
//	gateway."), 27.4 ("No conversation persistence. Refresh clears the
//	pane; the session continues on the backend until terminated or
//	timed out."), 27.2 ("It is compiled into the gateway binary as an
//	embedded static asset bundle (`embed.FS`)")
//
// diagnosis: The embedded playground SPA bundle gained an asset or a
//
//	source reference that would let the UI keep working, or keep
//	state, without a live gateway. §27.1 lists "no offline mode" as a
//	non-goal and §27.4 requires a refresh to clear the chat pane, so
//	the bundle must ship no service worker, no application-cache or
//	web-app manifest, and no client-side persistent-storage use.
//	Remove the offending asset or call site rather than relaxing this
//	test; if the playground genuinely needs one of these APIs, the
//	§27.1 non-goal has to change through the proposal pipeline first.
func TestPlaygroundBundleShipsNoOfflineCapableAssets(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)

	// The enumeration below reads the source directory. That is only a
	// faithful reading of what the gateway ships while assets.go embeds
	// the directory wholesale, so pin the directive first: a narrowed
	// pattern (or an added second embed root) would silently decouple
	// the two.
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
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(uiRoot, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		base := strings.ToLower(filepath.Base(rel))
		if reason, bad := offlineAssetNames[base]; bad {
			t.Errorf("%s/%s is embedded in the playground bundle: %s, and §27.1 lists offline mode as a non-goal",
				playgroundUIDir, rel, reason)
		}
		if strings.HasSuffix(base, ".appcache") {
			t.Errorf("%s/%s is embedded in the playground bundle: an application cache manifest serves the SPA with the origin unreachable, and §27.1 lists offline mode as a non-goal",
				playgroundUIDir, rel)
		}

		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		for lineNo, line := range strings.Split(string(data), "\n") {
			if isCommentLine(line) {
				// app.js documents in prose that the bearer is never
				// held in localStorage. Prose that names a forbidden
				// API is the opposite of a violation.
				continue
			}
			for token, reason := range offlineSourceTokens {
				if strings.Contains(line, token) {
					t.Errorf("%s/%s:%d references %q: %s (§27.1 non-goal \"No offline mode\"; §27.4 \"Refresh clears the pane\")",
						playgroundUIDir, rel, lineNo+1, token, reason)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", playgroundUIDir, walkErr)
	}
}

// isCommentLine reports whether a line of the bundle is entirely a
// JavaScript, CSS, or HTML comment. The token scan skips these so that
// a comment stating that an API is deliberately unused does not read as
// a use of it.
func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return true
	case strings.HasPrefix(trimmed, "//"):
		return true
	case strings.HasPrefix(trimmed, "/*"), strings.HasPrefix(trimmed, "*"):
		return true
	case strings.HasPrefix(trimmed, "<!--"):
		return true
	}
	return false
}
