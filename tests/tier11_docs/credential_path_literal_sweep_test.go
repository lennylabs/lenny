// SPDX-License-Identifier: MIT

// Tier-11 sweep for the retired pod-global credential literal.
//
// The credential file is written per session at
// /run/lenny/slots/{sessionId}/credentials.json. No pod carries a
// pod-global /run/lenny/credentials.json, so every reader-facing surface
// that names one sends an author or an operator to a file that is never
// written, and every SDK default or template comment that names one
// documents a location no session resolves to.
//
// The sweep covers the directories the retirement reaches: the
// specification, the documentation, the schemas, the chart, the
// commands (the scaffold templates and the compliance harness), the
// runtime SDKs, and the served OpenAPI document. A missed edit in a
// template, a chart comment, or a comment beside an SDK default is
// invisible to the compiler, which is why the sweep is mechanical
// rather than enumerated.
//
// spec: 4.7 (manifest credentialsPath), 6.1 (per-session credential
// lease), 13.1 (credential-file delivery)

package tier11_docs_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// credentialSweepRoots are the directories the retirement reaches,
// relative to the repository root.
var credentialSweepRoots = []string{
	"spec",
	"docs",
	"schemas",
	"charts",
	"cmd",
	"sdks",
}

// credentialSweepExtensions are the carriers the literal can hide in.
// Every reader-facing document, schema (both the JSON schemas and the
// proto definitions that carry the credentials_rotated field contract),
// chart template, scaffold template, and SDK source under the swept
// roots is one of these.
var credentialSweepExtensions = map[string]bool{
	".md":    true,
	".yaml":  true,
	".yml":   true,
	".json":  true,
	".go":    true,
	".tmpl":  true,
	".py":    true,
	".ts":    true,
	".tsx":   true,
	".js":    true,
	".sh":    true,
	".txt":   true,
	".proto": true,
}

// credentialSweepSkipDirs are directories whose contents are neither
// authored nor read as the current contract: dependency trees, build
// output, and fixtures recorded as they were written.
var credentialSweepSkipDirs = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"testdata":     true,
	"__pycache__":  true,
	".git":         true,
}

// spec: 6.1, 4.7
// diagnosis: a swept surface still names the pod-global
//
//	/run/lenny/credentials.json. That path exists on no pod: the adapter
//	writes one credential file per session under
//	/run/lenny/slots/{sessionId}/. A runtime author who follows the
//	surviving site reads a file that is never written, and an SDK whose
//	default names it delivers no credentials at all. A failure names the
//	file and line to restate on the manifest's credentialsPath.
func TestNoSurfaceNamesTheRetiredPodGlobalCredentialFile(t *testing.T) {
	root := repoRoot(t)
	for _, path := range sweepCredentialSurfaces(t, root) {
		reportCredentialLiteral(t, root, path)
	}
}

// spec: 6.1, 4.7
// diagnosis: the sweep above walks past the proto definitions under
//
//	schemas/, so a reintroduction of the retired pod-global credential
//	file into the file that carries the credentials_rotated field
//	contract turns nothing red. The gate is only as wide as the carriers
//	it reads.
func TestTheCredentialSweepReadsTheProtoSchemaCarrier(t *testing.T) {
	root := repoRoot(t)
	want := filepath.Join(root, "schemas", "lenny-adapter.proto")
	for _, path := range sweepCredentialSurfaces(t, root) {
		if path == want {
			return
		}
	}
	t.Fatalf("%s was not swept; the credentials_rotated field contract is written there",
		mustRel(t, root, want))
}

// sweepCredentialSurfaces returns every file the retirement's sweep
// reads: the carriers under the swept roots, plus the served OpenAPI
// document, which is generated rather than authored and so is swept by
// path rather than by directory.
func sweepCredentialSurfaces(t *testing.T, root string) []string {
	t.Helper()
	var swept []string
	for _, rel := range credentialSweepRoots {
		dir := filepath.Join(root, rel)
		walked := 0
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if credentialSweepSkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !credentialSweepExtensions[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			walked++
			swept = append(swept, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", rel, err)
		}
		if walked == 0 {
			t.Fatalf("walk %s: no files swept (moved or renamed?)", rel)
		}
	}
	return append(swept,
		filepath.Join(root, "pkg", "gateway", "externalapi", "openapi", "openapi.json"))
}

// reportCredentialLiteral fails the test once per line naming the
// retired literal, so one run reports every site rather than the first.
func reportCredentialLiteral(t *testing.T, root, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, line := range strings.Split(string(body), "\n") {
		if !strings.Contains(line, retiredPodGlobalCredentialPath) {
			continue
		}
		t.Errorf("%s:%d names the retired pod-global credential file; the file is written per session at %s:\n%s",
			mustRel(t, root, path), i+1, credentialSlotPath, strings.TrimSpace(line))
	}
}
