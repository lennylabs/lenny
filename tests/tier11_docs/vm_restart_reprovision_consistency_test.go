// SPDX-License-Identifier: MIT

// Tier-11 spec/code/proto/doc-consistency checks for the §5.2 `vm-restart`
// scrub profile reconciliation to retire-and-reprovision (proposal 0034,
// F-5.2.32). The reconciled §5.2 step 7 ("Fresh-guest reprovision") retires the
// pod at the occupancy-zero recycle boundary and the gateway provisions a fresh
// replacement pod, which is a fresh guest VM, from the warm pool. There is no
// in-guest guest restart: host sharing is forbidden, egress is default-deny,
// and a full guest restart would destroy the driving process. The in-guest
// `VMRestarter` seam and the `scrub_profile` wire field are removed, and the
// §6.2 occupancy projection and the §5.2 recycle-lifecycle successful-scrub
// sentence scope the post-scrub `sdk_connecting` SDK re-warm leg to `standard`
// and `in-place` pools so a `vm-restart` pool projects `claimed → draining`
// instead.
//
// This test pins that reconciliation across the surfaces it touches so a later
// edit cannot silently drift any one of them back:
//   - the §5.2 step 7 heading and body carry no in-guest Kata VM-lifecycle
//     attribution,
//   - no `VMRestarter` symbol remains in pkg/adapter,
//   - the proto `RecycleScrub` message carries no `scrub_profile` field,
//   - the §6.2 occupancy-projection prose and the §5.2 recycle-lifecycle
//     successful-scrub sentence scope the `sdk_connecting` re-warm leg to
//     `standard`/`in-place` pools and route a `vm-restart` pool to the retire,
//   - no reader-facing doc under docs/ asserts an in-guest guest restart for
//     `vm-restart`.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (delivered-parameter enumeration), 5.2 (vm-restart fresh-guest
// reprovision; no in-guest restart), 6.2 (recycle-disposition occupancy
// projection).

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prohibitedInGuestRestartSpecPhrases are the pre-fix §5.2 step-7 prose strings
// that attributed the guest restart to an in-guest adapter calling the Kata VM
// lifecycle API. The reconciled §5.2 forecloses that mechanism, so none may
// appear in spec/05. Each is a phrase the S1 reword removed. spec: §5.2 step 7.
var prohibitedInGuestRestartSpecPhrases = []string{
	"Kata runtime's VM lifecycle API",
	"requests a full guest VM restart",
	"**Guest VM restart:**",
}

// spec: 5.2
// diagnosis: The §5.2 step-7 (`vm-restart` scrub profile) paragraph reasserts an
//
//	in-guest Kata VM-lifecycle restart, which the reconciled §5.2 (proposal
//	0034, F-5.2.32) forecloses. Step 7 is now "Fresh-guest reprovision":
//	retire-and-reprovision at the recycle boundary, driven by the gateway and
//	the warm pool, not an in-guest adapter calling a host-side Kata VM lifecycle
//	socket. A failure here means the step-7 heading or body reintroduced the
//	"Guest VM restart" / "Kata runtime's VM lifecycle API" attribution, which is
//	unimplementable under the zero-RBAC, host-sharing-forbidden agent-pod model
//	and would be destroyed by the restart it triggers.
func TestSpec52Step7NoInGuestRestartAttribution_F5232(t *testing.T) {
	root := repoRoot(t)
	spec05 := filepath.Join(root, "spec", "05_runtime-registry-and-pool-model.md")
	s52 := specSection(t, spec05, "### 5.2 ")

	// The reworded step-7 heading is the retire-and-reprovision statement.
	step7 := requireLine(t, s52, "**Fresh-guest reprovision:**")
	requireAllContain(t, "§5.2 step 7 (Fresh-guest reprovision)", step7, []string{
		"retired at the occupancy-zero recycle boundary",
		"provisions a fresh replacement pod from the warm pool",
		"neither performs nor requests the restart",
	})

	// None of the foreclosed in-guest-restart phrasings may appear anywhere in
	// §5.2. Scoping to the whole section catches a reintroduction outside the
	// step-7 line as well.
	for _, phrase := range prohibitedInGuestRestartSpecPhrases {
		if strings.Contains(s52, phrase) {
			t.Errorf("§5.2 contains the foreclosed in-guest-restart phrase %q; step 7 retires the pod and reprovisions a fresh guest from the warm pool rather than restarting the guest in place via the Kata VM lifecycle API", phrase)
		}
	}
}

// spec: 5.2
// diagnosis: A `VMRestarter` symbol reappeared under pkg/adapter. Proposal 0034
//
//	(C1/C2) deleted the in-guest VMRestarter seam (the interface, the
//	Config.MicrovmRestart/Restarter fields, the ErrNoRestarter sentinel, the
//	StepGuestRestart step, and the Server.VMRestarter field) because
//	retire-and-reprovision is the single canonical mechanism and the seam had no
//	concrete implementer. A failure here means the seam was reintroduced,
//	resurrecting the impossible single-process in-guest restart flow that
//	contradicts the reconciled §5.2 and leaves a removed surface compiling.
func TestAdapterNoVMRestarterSymbol_F5232(t *testing.T) {
	root := repoRoot(t)
	adapterDir := filepath.Join(root, "pkg", "adapter")

	var hits []string
	err := filepath.Walk(adapterDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(body), "VMRestarter") {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk pkg/adapter: %v", err)
	}
	if len(hits) > 0 {
		t.Errorf("the removed VMRestarter seam reappeared under pkg/adapter in %v; proposal 0034 (C1/C2) deleted it and retire-and-reprovision at the gateway is the single canonical mechanism", hits)
	}
}

// spec: 4.7, 5.2
// diagnosis: The proto RecycleScrub message carries a scrub_profile field.
//
//	Proposal 0034 (C4) removed the RecycleScrub.scrub_profile wire field: the
//	gateway resolves the §5.2 recycle disposition (reuse for standard/in-place,
//	retire-and-reprovision for vm-restart) from the recycle policy in its own
//	runtime store, so the wire request carries only the pod identity and the
//	scrub parameters the adapter runs. A failure here means the field was
//	reintroduced, resurrecting a write-only wire echo the adapter no longer
//	reads and re-widening the delivered-parameter enumeration §4.7/§5.2 struck.
func TestProtoRecycleScrubNoScrubProfileField_F5232(t *testing.T) {
	root := repoRoot(t)
	proto := readRepoFile(t, root, "schemas", "lenny-adapter.proto")

	msg := messageBody(proto, "RecycleScrub")
	if msg == "" {
		t.Fatal("proto message RecycleScrub not found (renamed or removed?)")
	}
	// A field declaration is `<type> scrub_profile = N;`. The message body still
	// mentions scrub_profile in a comment documenting its removal, so match a
	// field declaration (a scrub_profile token followed by ` = `) rather than any
	// occurrence.
	for _, line := range strings.Split(msg, "\n") {
		code := line
		if idx := strings.Index(code, "//"); idx >= 0 {
			code = code[:idx]
		}
		if strings.Contains(code, "scrub_profile") && strings.Contains(code, "=") {
			t.Errorf("proto RecycleScrub carries a scrub_profile field declaration (%q); proposal 0034 (C4) removed the wire field, so the gateway resolves the recycle disposition from its own runtime store", strings.TrimSpace(line))
		}
	}
}

// spec: 5.2, 6.2
// diagnosis: The §6.2 occupancy-projection prose or the §5.2 recycle-lifecycle
//
//	successful-scrub sentence asserts the post-scrub `sdk_connecting` SDK re-warm
//	leg (or the `reserved` hold) for a `vm-restart` pool. Proposal 0034 (S2/S3)
//	scoped both sites to `standard` and `in-place` pools and routes a
//	`vm-restart` pool to `claimed → draining` after its scrub report (the pod
//	retires and the gateway provisions a fresh replacement). A failure here means
//	one site dropped the scope, so a reader applies the re-warm/reserve
//	disposition to a `vm-restart` pool that never enters it, an internal
//	contradiction with the reconciled step-7 retire.
func TestVMRestartRewarmLegScopedToStandardAndInPlace_F5232(t *testing.T) {
	root := repoRoot(t)

	// §6.2 occupancy-projection prose: the sdk_connecting re-warm leg is scoped to
	// standard/in-place, and a vm-restart recycling claim takes claimed → draining.
	spec06 := filepath.Join(root, "spec", "06_warm-pod-model.md")
	s62 := specSection(t, spec06, "### 6.2 ")
	projection := requireLine(t, s62, "a `recycling` claim projects `claimed` until its whole-pod scrub reports successful")
	requireAllContain(t, "§6.2 occupancy-projection prose", projection, []string{
		"on a `standard` or `in-place` pool a `recycling` claim projects `claimed`",
		"and `sdk_connecting` during the preConnect SDK re-warm leg that follows",
		"on a `vm-restart` pool a `recycling` claim projects `claimed`",
		"projects `claimed → draining` after the scrub report",
		"rather than entering the `sdk_connecting` re-warm leg or the `reserved` hold",
	})

	// §5.2 recycle-lifecycle successful-scrub sentence: the reserved hold and the
	// preConnect SDK re-warm are scoped to standard/in-place, and a vm-restart
	// preConnect pool projects claimed → draining after its scrub report rather
	// than running the SDK re-warm leg or entering reserved.
	spec05 := filepath.Join(root, "spec", "05_runtime-registry-and-pool-model.md")
	s52 := specSection(t, spec05, "### 5.2 ")
	recycleLifecycle := requireLine(t, s52, "the pod is held for its tenant through the claim's `reserved` state")
	requireAllContain(t, "§5.2 recycle-lifecycle successful-scrub sentence", recycleLifecycle, []string{
		"On a successful scrub on a `standard` or `in-place` pool the pod is held for its tenant",
		"on a `standard` or `in-place` preConnect pool the SDK re-warm runs after the scrub reports success",
		"A `vm-restart` pool instead retires the pod at the occupancy-zero boundary",
		"a `vm-restart` preConnect pool projects `claimed → draining` after its scrub report rather than running the SDK re-warm leg or entering `reserved`",
	})
}

// spec: 5.2
// diagnosis: A reader-facing doc page under docs/ asserts an in-guest guest
//
//	restart for the `vm-restart` scrub profile. Proposal 0034 (C6/S12) reconciled
//	the reader-facing docs to retire-and-reprovision, and the §5.2 step-7 reword
//	forecloses the in-guest restart. This grep-clean assertion pins the S12
//	reconciliation across the three pages the fix touched so the docs cannot
//	silently drift back to the "restarts the guest" / "guest VM is restarted
//	between tenants" framing. A failure means a page reintroduced in-guest-restart
//	prose, telling an operator a mechanism the platform does not implement.
func TestVMRestartDocsGrepCleanOfInGuestRestart_F5232(t *testing.T) {
	root := repoRoot(t)

	// Reader-facing pages that describe the vm-restart mechanism. Each must be
	// clean of every in-guest-restart phrasing the reconciliation removed.
	pages := []string{
		"docs/reference/configuration.md",
		"docs/operator-guide/security-principles.md",
		"docs/operator-guide/multi-tenancy.md",
	}
	// In-guest-restart prose the reconciliation forecloses. These pin the S12
	// reconciliation so no page reintroduces an in-guest guest-restart claim for
	// vm-restart.
	prohibited := []string{
		"restarts the guest",
		"guest VM is restarted between tenants",
		"the guest is restarted between tenants",
		"restarts the microvm guest",
	}
	for _, page := range pages {
		body := readDocPage(t, filepath.Join(root, page))
		for _, phrase := range prohibited {
			if strings.Contains(body, phrase) {
				t.Errorf("%s contains the foreclosed in-guest-restart phrase %q; the reconciled §5.2 retires the pod and reprovisions a fresh guest from the warm pool rather than restarting the guest in place", page, phrase)
			}
		}
	}
}

// messageBody returns the body of the named proto message (the text between its
// opening `message <name> {` and the matching closing brace), or "" when the
// message is absent. It brace-matches so a nested block does not truncate the
// body prematurely. It scopes a proto assertion to a single message so a match
// elsewhere in the schema does not mask a drift in the message itself.
func messageBody(proto, name string) string {
	marker := "message " + name + " {"
	start := strings.Index(proto, marker)
	if start < 0 {
		return ""
	}
	rest := proto[start+len(marker):]
	depth := 1
	for i, r := range rest {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:i]
			}
		}
	}
	return rest
}
