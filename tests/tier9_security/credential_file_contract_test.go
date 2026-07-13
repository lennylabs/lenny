// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §4.7 / §13.1 credential-file delivery contract probe. The
// adapter writes the runtime's LLM credentials to a tmpfs-backed
// /run/lenny/credentials.json with mode 0440, owned by the adapter
// UID and group-owned by the shared lenny-cred-readers group. §4.7
// states: "mode 0440, owned by the adapter UID and group-owned by
// the shared lenny-cred-readers supplementary group ... Mode 0440
// grants read to owner (adapter) and group (agent via
// supplementary-group membership) while denying all access to other
// UIDs, so no third party in the pod can read the file." §13.1
// restates it: "written by the adapter with mode 0440 — owner-writable
// and group-readable, no access for other UIDs."
//
// The credfile writer's 0440 mode is pinned at tier-1
// (pkg/adapter/credfile TestWriteSetsRestrictiveMode) and the pod's
// fsGroup / supplementalGroups declaration at tier-2
// (pkg/controller/sandbox/podspec). This probe is the missing
// real-pod assertion: it stats the credential file the adapter
// actually materialized inside a running agent pod and confirms the
// three delivery-contract properties (mode, owner UID, group GID)
// hold on the live file, and that the "other" read bit is clear so a
// third UID in the pod cannot read it.

package tier9_security_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 4.7 (runtime adapter credential file), 13.1 (pod security credential-file read boundary)
// diagnosis: a failure means the adapter-materialized
// /run/lenny/credentials.json in a live agent pod does not carry the
// §4.7 delivery contract (mode 0440, owner = adapter UID, group =
// lenny-cred-readers GID, no "other" read access). A wrong mode or
// ownership would let a third UID in the pod read upstream LLM
// credentials, breaching the §13.1 credential-file read boundary.
func TestCredentialFileDeliveryContract(t *testing.T) {
	// The e2e overlay configures no §4.9 credential provider or pool,
	// and cred-shell-echo-runtime declares no credentialPoolRefs, so
	// the gateway never calls AssignCredentials for it and the adapter
	// never writes /run/lenny/credentials.json in any real pod (the
	// binder calls AssignCredentials only when the BindRequest names
	// credential pools). Without a lease-bearing session against a
	// credential-declaring runtime, this contract cannot be exercised
	// on a live file. Exercising it requires wiring credential
	// delivery into the shared Kind install (a static credential
	// provider, a credential pool, credentialPoolRefs on a probe
	// runtime, and a session driver that binds a lease), which is left
	// as an open coverage decision.
	t.Skip("no §4.9 credential lease is delivered to any e2e agent pod, so the adapter never materializes " +
		"/run/lenny/credentials.json; the real-pod §4.7 delivery-contract assertion needs a credential-bearing session first")

	c := kind.InstallLenny(t)
	pod := findCredShellPod(t, c)

	// Locate the adapter-materialized credential file. Single-session
	// pods use /run/lenny/credentials.json; maxConcurrentSessions > 1
	// pods use the per-slot /run/lenny/slots/{slotId}/credentials.json
	// (§13.1 per-slot clause). A find over the mount covers both.
	found, err := execContainer(t, c, pod, "runtime", "find", "/run/lenny", "-name", credfile.FileName, "-type", "f")
	if err != nil {
		t.Skipf("§4.7 credential-file probe: could not enumerate /run/lenny in cred-shell-echo pod %s: %v\noutput:\n%s",
			pod, err, found)
	}
	path := strings.TrimSpace(found)
	if path == "" {
		t.Skipf("§4.7 credential-file probe: no %s materialized under /run/lenny in cred-shell-echo pod %s "+
			"(no active credential-bearing session)", credfile.FileName, pod)
	}
	// A find may return several files under concurrent slots; assert
	// the contract on the first.
	path = strings.SplitN(path, "\n", 2)[0]

	// stat -c '%a %u %g' reports octal mode, numeric owner UID, and
	// numeric group GID, e.g. "440 65532 65534".
	statOut, err := execContainer(t, c, pod, "runtime", "stat", "-c", "%a %u %g", path)
	if err != nil {
		t.Fatalf("§4.7 credential-file probe: stat %s in pod %s failed: %v\noutput:\n%s", path, pod, err, statOut)
	}
	fields := strings.Fields(strings.TrimSpace(statOut))
	if len(fields) != 3 {
		t.Fatalf("§4.7 credential-file probe: unexpected stat output for %s: %q", path, statOut)
	}

	// Mode: exactly 0440. The spec fixes this value, not a range.
	wantMode := strings.TrimPrefix(credfile.FileMode.Perm().String(), "-") // "r--r-----"
	mode, err := strconv.ParseUint(fields[0], 8, 32)
	if err != nil {
		t.Fatalf("§4.7 credential-file probe: parse mode %q: %v", fields[0], err)
	}
	if uint32(mode) != uint32(credfile.FileMode.Perm()) {
		t.Errorf("§4.7 (mode) FAIL: %s is mode %04o, want %04o (%s); the credential file MUST be 0440",
			path, mode, uint32(credfile.FileMode.Perm()), wantMode)
	}
	// Deny-all-to-other: the "other" read bit (0o004) MUST be clear so
	// a third UID in the pod cannot read the file.
	if mode&0o004 != 0 {
		t.Errorf("§4.7 (other-read) FAIL: %s grants read to other UIDs (mode %04o); §4.7 requires denying all "+
			"access to other UIDs so no third party in the pod can read the credential file", path, mode)
	}

	// Owner: the adapter UID (the adapter process writes the file).
	owner, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		t.Fatalf("§4.7 credential-file probe: parse owner uid %q: %v", fields[1], err)
	}
	if owner != podspec.AdapterUID {
		t.Errorf("§4.7 (owner) FAIL: %s is owned by UID %d, want the adapter UID %d", path, owner, podspec.AdapterUID)
	}

	// Group: the lenny-cred-readers GID (set by the pod fsGroup at
	// mount time), the shared read boundary the agent reads through.
	group, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		t.Fatalf("§4.7 credential-file probe: parse group gid %q: %v", fields[2], err)
	}
	if group != podspec.CredReadersGID {
		t.Errorf("§4.7 (group) FAIL: %s is group-owned by GID %d, want the lenny-cred-readers GID %d",
			path, group, podspec.CredReadersGID)
	}
}
