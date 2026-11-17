// SPDX-License-Identifier: MIT

package main

import (
	"path"
	"regexp"
)

// gitleaks skips some files by path before it reads them: the paths the
// global allowlist of its default configuration lists. .gitleaks.toml
// extends that configuration, so the skip applies to the secret scan. The
// check therefore writes each committed file under such a path into the
// scan tree under a renamed path that no pattern matches, so gitleaks
// reads it (secretscan_tree.go), and fails when a committed text file
// under such a path has no entry in the skipped-paths register, which
// records the files scanned that way (change 0158, decision 5). It
// implements the merge gate rather than a specification behavior, so it
// carries no spec annotation.
//
// The patterns below are copied from the `[allowlist]` table of
// config/gitleaks.toml at the tag v8.30.1 of github.com/gitleaks/gitleaks,
// the release tests/gate/Dockerfile pins. TestGitleaksDefaultSkippedPathsMatchThePinnedRelease
// fails when the pinned release or the minVersion of .gitleaks.toml moves
// away from gitleaksDefaultsVersion, or go.mod moves off the classifier
// modules that release pins, so a new pin reads the new release's patterns
// and modules before it lands, and the check reports unverified when the
// gitleaks it runs is another release. The value patterns of that allowlist stay as
// gitleaks ships them and are not copied here.

// gitleaksDefaultsVersion is the gitleaks release the patterns of
// gitleaksDefaultSkippedPaths were read from.
const gitleaksDefaultsVersion = "8.30.1"

// gitleaksClassifierModules names the modules gitleaks v8.30.1 uses to
// decide which files it does not read, with the versions its go.mod pins.
// The check imports the same versions (secretscan_coverage.go).
var gitleaksClassifierModules = map[string]string{
	"github.com/mholt/archives": "v0.1.2",
	"github.com/h2non/filetype": "v1.1.3",
}

// gitleaksDefaultSkippedPaths lists the `paths` of the default global
// allowlist of gitleaks v8.30.1, verbatim and in order. gitleaks compiles
// them with Go's regexp package and matches them, unanchored, against the
// path of each file and each directory it walks, relative to the scanned
// root; a matched directory is not entered.
var gitleaksDefaultSkippedPaths = []string{
	`gitleaks\.toml`,
	`(?i)\.(?:bmp|gif|jpe?g|png|svg|tiff?)$`,
	`(?i)\.(?:eot|[ot]tf|woff2?)$`,
	`(?i)\.(?:docx?|xlsx?|pdf|bin|socket|vsidx|v2|suo|wsuo|.dll|pdb|exe|gltf)$`,
	`go\.(?:mod|sum|work(?:\.sum)?)$`,
	`(?:^|/)vendor/modules\.txt$`,
	`(?:^|/)vendor/(?:github\.com|golang\.org/x|google\.golang\.org|gopkg\.in|istio\.io|k8s\.io|sigs\.k8s\.io)(?:/.*)?$`,
	`(?:^|/)gradlew(?:\.bat)?$`,
	`(?:^|/)gradle\.lockfile$`,
	`(?:^|/)mvnw(?:\.cmd)?$`,
	`(?:^|/)\.mvn/wrapper/MavenWrapperDownloader\.java$`,
	`(?:^|/)node_modules(?:/.*)?$`,
	`(?:^|/)(?:deno\.lock|npm-shrinkwrap\.json|package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$`,
	`(?:^|/)bower_components(?:/.*)?$`,
	`(?:^|/)(?:angular|bootstrap|jquery(?:-?ui)?|plotly|swagger-?ui)[a-zA-Z0-9.-]*(?:\.min)?\.js(?:\.map)?$`,
	`(?:^|/)javascript\.json$`,
	`(?:^|/)(?:Pipfile|poetry)\.lock$`,
	`(?i)(?:^|/)(?:v?env|virtualenv)/lib(?:64)?(?:/.*)?$`,
	`(?i)(?:^|/)(?:lib(?:64)?/python[23](?:\.\d{1,2})+|python/[23](?:\.\d{1,2})+/lib(?:64)?)(?:/.*)?$`,
	`(?i)(?:^|/)[a-z0-9_.]+-[0-9.]+\.dist-info(?:/.+)?$`,
	`(?:^|/)vendor/(?:bundle|ruby)(?:/.*?)?$`,
	`\.gem$`,
	`verification-metadata\.xml`,
	`Database.refactorlog`,
	`(?:^|/)\.git$`,
}

// gitleaksSkippedPathRegexps holds gitleaksDefaultSkippedPaths compiled.
var gitleaksSkippedPathRegexps = func() []*regexp.Regexp {
	res := make([]*regexp.Regexp, len(gitleaksDefaultSkippedPaths))
	for i, p := range gitleaksDefaultSkippedPaths {
		res[i] = regexp.MustCompile(p)
	}
	return res
}()

// skippedByDefaultAllowlist reports whether gitleaks skips the tracked
// file rel, a slash-separated path relative to the repository root. It
// tests the file's path and the path of each directory above it, because
// gitleaks does not enter a directory whose path matches. It returns the
// path that matched (rel or one of its directories) and the pattern.
func skippedByDefaultAllowlist(rel string) (matched, pattern string, ok bool) {
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if p, hit := matchSkippedPath(dir); hit {
			// Keep walking up: report the outermost skipped directory,
			// which is the one gitleaks stops at.
			matched, pattern, ok = dir, p, true
		}
	}
	if ok {
		return matched, pattern, true
	}
	if p, hit := matchSkippedPath(rel); hit {
		return rel, p, true
	}
	return "", "", false
}

// matchSkippedPath returns the first default pattern that matches p.
func matchSkippedPath(p string) (string, bool) {
	for i, re := range gitleaksSkippedPathRegexps {
		if re.MatchString(p) {
			return gitleaksDefaultSkippedPaths[i], true
		}
	}
	return "", false
}
