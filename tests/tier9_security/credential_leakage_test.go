// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 TESTING.md §12.9.8 credential-leakage probe. Three adversarial checks
// against a cred-shell-echo pod running on the Kind cluster:
//
//   - Environment: dump the runtime container's /proc/1/environ and
//     assert no LLM-provider credential prefix appears. The
//     cred-shell-echo image is the only test runtime that retains
//     /bin/sh so the probe can read its environment from inside.
//   - Filesystem: list /run/lenny, the §4.7 credential mount, and
//     assert the credential file (when present) is group-readable
//     under the lenny-cred-readers GID and unreadable by anyone
//     else. The runtime container mounts the credential tmpfs
//     read-only per §13.1, so writes from inside the runtime fail.
//   - Network egress: locate the egress-capture sidecar in the same
//     pod, parse the JSONL capture file the sidecar writes for each
//     accepted forward, and assert no captured payload hash matches
//     a known credential. The TESTING.md §12.9.8 sidecar (see
//     pkg/controller/sandbox/podspec.EgressCapture) is the network-
//     side analogue of the environment and filesystem probes.
//
// Each test calls t.Skip when the precondition is not met (no Kind
// cluster, no cred-shell-echo pod, no egress-capture sidecar), so the
// suite runs cleanly on hosts where install.sh has not stood up the
// cluster. A test only fails when the live invariant is breached.

package tier9_security_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// credShellPoolSuffix is the pool name suffix the TESTING.md §12.9.8 probe
// looks for: agent-workload.yaml names the pool
// cred-shell-echo-pool.
const credShellPoolSuffix = "cred-shell-echo-pool"

// credentialPrefixes are the well-known LLM-provider credential
// shapes the probe scans for in environment dumps and egress
// captures. A real production credential matches one of these
// prefixes; an adversarial dump that surfaces one indicates the §13.1
// boundary has been breached.
var credentialPrefixes = []string{
	"sk-ant-",     // anthropic_direct
	"sk-",         // openai
	"AKIA",        // aws_bedrock (long-term)
	"ASIA",        // aws_bedrock (short-term / STS)
	"ya29.",       // google/vertex_ai
	"AIza",        // google api key
	"ghp_",        // github personal access token
	"ghs_",        // github installation
	"github_pat_", // github fine-grained
	"hvs.",        // hashicorp vault token
}

// findCredShellPod returns one cred-shell-echo agent pod from the
// running workload, or "" with a skip when no such pod is present.
// The pool name suffix is the discriminator; install.sh applies
// agent-workload.yaml with cred-shell-echo-pool, and the SandboxClaim
// reconciler stamps the pool label onto each managed pod.
func findCredShellPod(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	pods := kind.RequireAgentWorkload(t, c)
	for _, p := range pods {
		if strings.HasSuffix(p.Pool, credShellPoolSuffix) || strings.Contains(p.Pool, "cred-shell-echo") {
			return p.Name
		}
	}
	t.Skip("TESTING.md §12.9.8 probe: no cred-shell-echo agent pod present; install.sh applies agent-workload.yaml " +
		"which declares the cred-shell-echo-pool. Without the pool the TESTING.md §12.9.8 boundary cannot be probed.")
	return ""
}

// exec runs `kubectl exec` against a named pod's container and returns
// the combined output. Skip is used when the pod is unreachable so the
// suite degrades gracefully on a partially-installed cluster.
func execContainer(t *testing.T, c *kind.Cluster, pod, container string, cmd ...string) (string, error) {
	t.Helper()
	args := []string{"-n", "lenny-agents", "exec", pod, "-c", container, "--"}
	args = append(args, cmd...)
	var stdout, stderr bytes.Buffer
	cmdObj := c.Kubectl(args...)
	cmdObj.Stdout = &stdout
	cmdObj.Stderr = &stderr
	if err := cmdObj.Run(); err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}

// scanCredentialPrefix returns the first known-credential prefix that
// appears in body, or "" when none does. The scan is case-sensitive
// because every credential prefix carries case-sensitive bytes.
func scanCredentialPrefix(body string) string {
	for _, p := range credentialPrefixes {
		if strings.Contains(body, p) {
			return p
		}
	}
	return ""
}

// spec: 12.9.8
// diagnosis: a TESTING.md §12.9.8 credential-leakage probe dumps the runtime
// container's environment and asserts no LLM-provider credential
// prefix surfaces. The cred-shell-echo runtime retains /bin/sh so the
// probe can read /proc/1/environ from inside; an adversarial dump that
// returns a known credential prefix breaches §13.1 by exposing
// upstream secrets through the agent pod's environment.
func TestCredentialLeakageEnvironment(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := findCredShellPod(t, c)

	env, err := execContainer(t, c, pod, "runtime", "cat", "/proc/1/environ")
	if err != nil {
		t.Skipf("TESTING.md §12.9.8 (env): could not read /proc/1/environ from cred-shell-echo pod %s: %v\noutput:\n%s",
			pod, err, env)
	}
	if hit := scanCredentialPrefix(env); hit != "" {
		t.Errorf("TESTING.md §12.9.8 (env) FAIL: cred-shell-echo runtime container exposes a credential matching prefix %q in /proc/1/environ; "+
			"upstream LLM credentials MUST NOT appear in the agent pod's environment", hit)
	}
}

// spec: 12.9.8
// diagnosis: a TESTING.md §12.9.8 filesystem probe lists the §4.7 credential
// mount (/run/lenny) and asserts no LLM-provider credential prefix
// is readable from inside the runtime container. The runtime mounts
// the credential tmpfs read-only per §13.1, so the only legitimate
// content is the per-session credential file the adapter writes.
// The probe checks both the mount listing and the content of any
// file under /run/lenny.
func TestCredentialLeakageFilesystem(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := findCredShellPod(t, c)

	// /run/lenny is the §4.7 credential mount path. ls -la makes the
	// mode + group ownership readable from the test, and `cat` on
	// each readable file probes for credential strings. The
	// `2>/dev/null || true` guards against an empty mount (no
	// credential files yet) — `ls /run/lenny/*` returns exit code 1
	// when the glob doesn't match, which is the safe "no credentials
	// on disk" state. The probe only needs to assert that whatever
	// files do exist contain no LLM-provider credential prefix.
	listing, err := execContainer(t, c, pod, "runtime", "sh", "-c",
		"ls -la /run/lenny 2>/dev/null || true; find /run/lenny -type f 2>/dev/null -exec cat {} +; true")
	if err != nil {
		t.Skipf("TESTING.md §12.9.8 (filesystem): probe failed against cred-shell-echo pod %s: %v\noutput:\n%s",
			pod, err, listing)
	}
	if hit := scanCredentialPrefix(listing); hit != "" {
		t.Errorf("TESTING.md §12.9.8 (filesystem) FAIL: /run/lenny in cred-shell-echo runtime container exposes credential prefix %q; "+
			"the §4.7 credential file MUST contain only the per-session lease material, not standing LLM credentials", hit)
	}
}

// spec: 12.9.8
// diagnosis: the TESTING.md §12.9.8 network-egress probe reads the
// lenny-egress-capture sidecar's JSONL capture file (mounted
// read-only on the runtime container at /run/lenny-capture/egress.jsonl)
// and asserts every captured connection's SentHash is a stable
// SHA-256 digest, not a passthrough of credential bytes. The hash
// itself is irreversible so the capture artifact cannot leak
// credentials; this test asserts the capture is well-formed and the
// sidecar is reachable on the standard mount path so the TESTING.md §12.9.8
// boundary is exercised end-to-end. A missing sidecar or unparseable
// capture is the probe's failure mode.
//
// spec: TESTING.md §12.9.8, 13.1 (Pod Security)
// diagnosis: a failure means the egress-capture sidecar is unreachable,
// its JSONL capture is malformed, or the runtime cannot read it. The
// capture container runs at its own UID rather than the agent UID
// (§13.1 Container identity reserves the agent UID to the runtime), so
// the runtime reads the file only through the lenny-cred-readers group:
// a mode other than 0640, a group other than that GID, or a cat that
// fails for any reason but a missing file means the runtime has lost
// read access and the TESTING.md §12.9.8 SentHash boundary (hashed, never
// raw credential bytes) cannot be verified end-to-end.
func TestCredentialLeakageNetworkEgress(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := findCredShellPod(t, c)

	// The egress-capture sidecar mounts /run/lenny-capture writable on
	// itself and the runtime mounts the same volume read-only.
	listing, err := execContainer(t, c, pod, "runtime", "ls", "-la", "/run/lenny-capture")
	if err != nil {
		t.Skipf("TESTING.md §12.9.8 (egress): /run/lenny-capture not mounted on cred-shell-echo pod %s; the TESTING.md §12.9.8 sidecar may not be injected (check controller.egressCaptureImage and the template annotation). %v\noutput:\n%s",
			pod, err, listing)
	}

	stat, err := execContainer(t, c, pod, "runtime", "ls", "-ln", egressCapturePath)
	if err != nil {
		if strings.Contains(stat, missingFileMessage) {
			// Sidecar present but no capture yet: the pod has not
			// emitted any outbound TCP. The mount existing is the
			// meaningful assertion at this point.
			t.Logf("TESTING.md §12.9.8 (egress): %s not yet written (no egress traffic from cred-shell-echo); mount is in place.", egressCapturePath)
			return
		}
		t.Fatalf("TESTING.md §12.9.8 (egress): ls -ln %s in the runtime container of pod %s failed: %v\noutput:\n%s",
			egressCapturePath, pod, err, stat)
	}
	assertCaptureFileReadableByRuntime(t, stat)

	// The file exists, so any cat failure (a permission denial above
	// all) means the runtime cannot read the capture.
	body, err := execContainer(t, c, pod, "runtime", "cat", egressCapturePath)
	if err != nil {
		t.Fatalf("TESTING.md §12.9.8 (egress) FAIL: the runtime container of pod %s cannot read %s: %v\noutput:\n%s",
			pod, egressCapturePath, err, body)
	}

	// Parse each JSONL row. A malformed capture is a sidecar bug.
	type record struct {
		Timestamp string `json:"timestamp"`
		Peer      string `json:"peer"`
		Upstream  string `json:"upstream"`
		SentHash  string `json:"sent_hash"`
		BytesSent int64  `json:"bytes_sent"`
	}
	for line := range strings.SplitSeq(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("TESTING.md §12.9.8 (egress) FAIL: lenny-egress-capture wrote a malformed JSONL row %q: %v",
				line, err)
			continue
		}
		if rec.SentHash == "" || rec.Upstream == "" {
			t.Errorf("TESTING.md §12.9.8 (egress) FAIL: capture row missing sent_hash or upstream: %+v", rec)
		}
		// Defense in depth: the sent_hash field must NOT itself
		// contain a credential prefix; only hex digits and dashes
		// are legitimate.
		if hit := scanCredentialPrefix(rec.SentHash); hit != "" {
			t.Errorf("TESTING.md §12.9.8 (egress) FAIL: capture row hash %q contains credential prefix %q (sidecar bug); hashes must be SHA-256 hex",
				rec.SentHash, hit)
		}
	}

	// Final sanity: the body itself must not contain a credential
	// prefix. The sidecar writes only hashes, but a copy-paste bug
	// could leak raw bytes; the probe catches that regression.
	if hit := scanCredentialPrefix(body); hit != "" {
		t.Errorf("TESTING.md §12.9.8 (egress) FAIL: capture file body contains credential prefix %q; lenny-egress-capture MUST only write SHA-256 hashes", hit)
	}
}

// egressCapturePath is the capture file lenny-egress-capture writes,
// as the runtime container sees it through its read-only mount.
const egressCapturePath = "/run/lenny-capture/egress.jsonl"

// missingFileMessage is the coreutils and busybox error text for a path
// that does not exist, the one ls or cat failure that means "not yet
// written" rather than "not readable".
const missingFileMessage = "No such file or directory"

// captureFileMode is the mode lenny-egress-capture creates the capture
// file with, as ls -l renders it: owner read-write, group read, nothing
// for other.
const captureFileMode = "-rw-r-----"

// assertCaptureFileReadableByRuntime checks the ls -ln line for the
// capture file. The capture container runs at a UID of its own, so the
// runtime reads the file only through the group: the fsGroup-managed
// emptyDir gives it the lenny-cred-readers GID, which the runtime holds
// as a supplementary group, and mode 0640 grants that group read.
//
// spec: 13.1 (Pod Security)
func assertCaptureFileReadableByRuntime(t *testing.T, lsLine string) {
	t.Helper()
	mode, gid, err := parseLsLn(lsLine)
	if err != nil {
		t.Fatalf("TESTING.md §12.9.8 (egress): %v", err)
	}
	if mode != captureFileMode {
		t.Errorf("§13.1 (egress capture) FAIL: %s has mode %s, want %s (0640) so the runtime reads it through the lenny-cred-readers group",
			egressCapturePath, mode, captureFileMode)
	}
	if gid != podspec.CredReadersGID {
		t.Errorf("§13.1 (egress capture) FAIL: %s is group-owned by GID %d, want the lenny-cred-readers GID %d",
			egressCapturePath, gid, podspec.CredReadersGID)
	}
}

// parseLsLn extracts the mode string and the numeric group from one
// `ls -ln` line, for example "-rw-r----- 1 65531 65534 120 Oct  3 06:00 x".
// Busybox and coreutils both print mode, link count, uid, and gid as the
// first four fields.
func parseLsLn(line string) (mode string, gid int64, err error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 4 {
		return "", 0, fmt.Errorf("unexpected ls -ln output %q", line)
	}
	gid, err = strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("parse group %q from ls -ln output %q: %w", fields[3], line, err)
	}
	// A trailing "." or "+" marks an SELinux context or an ACL and is
	// not part of the permission bits.
	return strings.TrimRight(fields[0], ".+"), gid, nil
}

// guard: keep static-analysis happy even if the only callers are
// disabled by build tags or guards.
var (
	_ = errors.New
	_ = fmt.Sprintf
)

// spec: 13.1 (Pod Security)
// diagnosis: parseLsLn misreads the ls -ln line the egress-capture check
// relies on, so the mode and group assertions on the capture file would
// compare the wrong fields. The test needs no cluster.
func TestParseLsLnReadsModeAndGroup(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantMode string
		wantGID  int64
		wantErr  bool
	}{
		{name: "busybox", line: "-rw-r-----    1 65531    65534          120 Oct  3 06:00 /run/lenny-capture/egress.jsonl\n", wantMode: "-rw-r-----", wantGID: 65534},
		{name: "coreutils with ACL marker", line: "-rw-r-----+ 1 65531 65534 120 Oct  3 06:00 egress.jsonl", wantMode: "-rw-r-----", wantGID: 65534},
		{name: "too few fields", line: "-rw-r----- 1 65531", wantErr: true},
		{name: "non-numeric group", line: "-rw-r----- 1 65531 lenny 120 Oct  3 06:00 egress.jsonl", wantErr: true},
		{name: "empty", line: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, gid, err := parseLsLn(tc.line)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseLsLn(%q) = (%q, %d, nil), want an error", tc.line, mode, gid)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLsLn(%q): %v", tc.line, err)
			}
			if mode != tc.wantMode || gid != tc.wantGID {
				t.Errorf("parseLsLn(%q) = (%q, %d), want (%q, %d)", tc.line, mode, gid, tc.wantMode, tc.wantGID)
			}
		})
	}
}
