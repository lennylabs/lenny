// SPDX-License-Identifier: MIT

// Tier-11 sweep for the retired pod-global credential literal.
//
// The credential file is written per session at
// /run/lenny/slots/{sessionId}/credentials.json. No pod carries a
// pod-global credential file directly under /run/lenny/, so every reader-facing surface
// that names one sends an author or an operator to a file that is never
// written, and every SDK default or template comment that names one
// documents a location no session resolves to.
//
// The sweep reads the surfaces the sibling retirement sweeps read,
// widened with the library root: the credential path is stated in a
// constant's doc comment, in a struct-field comment, and in a test
// header under pkg/ as often as it is in prose, and a comment naming a
// file no pod writes is invisible to the compiler wherever it stands.
//
// spec: 4.7 (manifest credentialsPath), 6.1 (per-session credential
// lease), 13.1 (credential-file delivery)

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// permittedCredentialRetirementStatements are the occurrences of the
// retired literal that state its retirement. Each is keyed by the
// repository-relative file it stands in and matched as a substring of
// the trimmed line text, so a line that moves keeps its exemption and a
// line that is reworded loses it. The exemption covers the statement's
// own occurrence, so any further occurrence on the same line is still
// reported.
var permittedCredentialRetirementStatements = map[string][]string{
	filepath.Join("pkg", "adapter", "slotlayout", "slotlayout_test.go"): {
		"// global " + retiredPodGlobalCredentialPath + " so a rotation on one slot does not",
		`if a.CredentialsFile == "` + retiredPodGlobalCredentialPath + `" {`,
	},
	filepath.Join("tests", "tier4_integration", "concurrent_delegation_proxy_test.go"): {
		"//     the retired pod-global " + retiredPodGlobalCredentialPath + ", which no pod",
		"// " + retiredPodGlobalCredentialPath + ", which no pod writes, was never created.",
		`t.Errorf("the retired pod-global ` + retiredPodGlobalCredentialPath +
			`, which no pod writes, exists (err=%v); credentials were not written per slot", err)`,
	},
}

// spec: 6.1, 4.7
// diagnosis: a swept surface still names the pod-global
//
//	credential file directly under /run/lenny/. That path exists on no pod: the adapter
//	writes one credential file per session under
//	/run/lenny/slots/{sessionId}/. A runtime author who follows the
//	surviving site reads a file that is never written, and an SDK whose
//	default names it delivers no credentials at all. A failure names the
//	file and line to restate on the manifest's credentialsPath.
func TestNoSurfaceNamesTheRetiredPodGlobalCredentialFile(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]map[string]bool{}
	for _, path := range credentialSweepSurfaces(t, root) {
		reportCredentialLiteral(t, root, path, seen)
	}
	// An exemption that outlives its sentence would silently widen the
	// sweep's permitted set, so every permitted statement must still stand
	// where it is recorded.
	for rel, statements := range permittedCredentialRetirementStatements {
		for _, statement := range statements {
			if !seen[rel][statement] {
				t.Errorf("%s no longer carries the retirement statement %q; drop the exemption or restore the statement", rel, statement)
			}
		}
	}
}

// spec: 6.1, 4.7
// diagnosis: the credential sweep reads the reader-facing roots alone.
//
//	The path is stated in a constant's doc comment, in a struct-field
//	comment, and in a package header under pkg/, and in a test header and
//	a diagnosis comment under tests/, which the earlier root set did not
//	read, so a retired pod-global credential file left in a Go doc comment
//	or restated in a suite header as the single-session layout shipped
//	green while every reader-facing surface was clean. A failure means the
//	root set no longer reaches the libraries and the suites.
func TestTheCredentialSweepReadsTheLibraryAndTestRoots(t *testing.T) {
	roots := map[string]bool{}
	for _, rel := range credentialSweepRoots {
		roots[rel] = true
	}
	for _, rel := range append(append([]string{}, retirementSweepRoots...), "pkg", "tests") {
		if !roots[rel] {
			t.Errorf("credentialSweepRoots omits %s, so a retired credential literal under it is unread", rel)
		}
	}
	root := repoRoot(t)
	want := []string{
		filepath.Join(root, "pkg", "adapter", "scrub", "scrub.go"),
		filepath.Join(root, "tests", "tier9_security", "credential_file_contract_test.go"),
	}
	swept := map[string]bool{}
	for _, path := range credentialSweepSurfaces(t, root) {
		swept[path] = true
	}
	for _, path := range want {
		if !swept[path] {
			t.Errorf("%s was not swept; a retired credential literal restated there would ship green",
				mustRel(t, root, path))
		}
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
	for _, path := range credentialSweepSurfaces(t, root) {
		if path == want {
			return
		}
	}
	t.Fatalf("%s was not swept; the credentials_rotated field contract is written there",
		mustRel(t, root, want))
}

// reportCredentialLiteral fails the test once per line naming the
// retired literal, so one run reports every site rather than the first.
func reportCredentialLiteral(t *testing.T, root, path string, seen map[string]map[string]bool) {
	t.Helper()
	rel := mustRel(t, root, path)
	permitted := permittedCredentialRetirementStatements[rel]
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, line := range strings.Split(string(body), "\n") {
		if !strings.Contains(line, retiredPodGlobalCredentialPath) {
			continue
		}
		residue, matched := stripPermitted(permitted, strings.TrimSpace(line))
		for _, statement := range matched {
			if seen[rel] == nil {
				seen[rel] = map[string]bool{}
			}
			seen[rel][statement] = true
		}
		if !strings.Contains(residue, retiredPodGlobalCredentialPath) {
			continue
		}
		t.Errorf("%s:%d names the retired pod-global credential file; the file is written per session at %s:\n%s",
			rel, i+1, credentialSlotPath, strings.TrimSpace(line))
	}
}
