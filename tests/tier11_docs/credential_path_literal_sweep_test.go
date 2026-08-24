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
// The sweep reads the surfaces retirementSweepSurfaces walks, which is
// the same set the retired pod-global working directory is swept over.
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
	for _, path := range retirementSweepSurfaces(t, root) {
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
	for _, path := range retirementSweepSurfaces(t, root) {
		if path == want {
			return
		}
	}
	t.Fatalf("%s was not swept; the credentials_rotated field contract is written there",
		mustRel(t, root, want))
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
