// SPDX-License-Identifier: MIT

// Tier-11 doc/spec-consistency check for the stale slot reaper's description on
// the operator configuration page. The gateway leader removes a stale session
// member only when the adapter reports a clean exit, the pod's `rehydrated`
// flag is present, and the set holds another member, so the reaper never runs
// the occupancy-zero disposition. It visits only the pods that a session holds.
// A slot outside those conditions stays counted until the
// orphaned-claim garbage collection drains the pod after its last live
// session. The configuration page owns the reader-facing description of the
// reaper, so it must state both exclusions and must scope its recovery-time
// bound to pods that still run other sessions.
//
// spec: 5.2 (Slot assignment atomicity), 4.6.1 (Orphaned SandboxClaim detection)

package tier11_docs_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// staleSlotReaperRequiredPhrases are the statements the configuration page's
// stale slot reaper subsection must carry. spec: §5.2, §4.6.1.
var staleSlotReaperRequiredPhrases = []string{
	// The reaper keeps a member that is the set's last one.
	"keeps a slot that is the pod's last remaining slot",
	// The reaper visits only pods a session holds.
	"does not visit a pod that no session holds",
	// Both cases fall to the orphaned-claim drain.
	"the controller drains the pod once the orphaned-claim timeout",
	// The recovery-time bound applies only where other sessions still run.
	"On a pod that still runs other sessions, a slot whose release failed is freed",
}

// staleSlotReaperSection returns the body of the "Stale slot reaper"
// subsection of the operator configuration page.
func staleSlotReaperSection(t *testing.T) string {
	t.Helper()
	body := readDocPage(t, filepath.Join(repoRoot(t), "docs/operator-guide/configuration.md"))
	start := strings.Index(body, "\n### Stale slot reaper\n")
	if start < 0 {
		t.Fatalf("docs/operator-guide/configuration.md has no \"### Stale slot reaper\" subsection")
	}
	rest := body[start+1:]
	if next := regexp.MustCompile(`\n#{2,3} `).FindStringIndex(rest[4:]); next != nil {
		rest = rest[:next[0]+4]
	}
	return rest
}

// spec: 5.2 (Slot assignment atomicity), 4.6.1 (Orphaned SandboxClaim detection)
// diagnosis: The configuration page describes the stale slot reaper as freeing
//
//	every failed-release slot within a fixed bound. The spec has the reaper
//	keep a pod's last member and skip pods no session holds, leaving those
//	slots to the orphaned-claim drain, so an operator reading the page expects
//	a recovery the platform does not perform.
func TestStaleSlotReaperDocStatesLastMemberAndUnvisitedPodExclusions(t *testing.T) {
	section := staleSlotReaperSection(t)
	flat := strings.Join(strings.Fields(section), " ")
	for _, phrase := range staleSlotReaperRequiredPhrases {
		if !strings.Contains(flat, phrase) {
			t.Errorf("Stale slot reaper subsection is missing %q", phrase)
		}
	}
	if strings.Contains(flat, "so the reaper frees each of them within") {
		t.Errorf("Stale slot reaper subsection states an unconditional recovery bound for every failed-release slot")
	}
}

// redisLossReaperRequiredPhrases are the statements the disaster-recovery
// page's Redis-loss recovery item must carry about the stale slot reaper. When
// an outage ends and Redis kept its data, the recovery rebuild deletes the
// pod's `rehydrated` flag, and the reaper keeps the member until the pod's
// next reservation or release restores it. The reaper also keeps a pod's last
// member. spec: §5.2, §4.6.1.
var redisLossReaperRequiredPhrases = []string{
	// The fixed bound is scoped to pods whose set was not rebuilt.
	"On a pod whose slot set the gateway did not rebuild after the outage, the reaper frees the slot after",
	// A rebuilt pod waits for its next reservation or release.
	"the reaper reaches the slot only at the first pass after the pod's next slot reservation or release",
	// The reaper keeps the pod's last member.
	"A slot that is the pod's last remaining slot stays counted until the pod's last active session ends",
	// The page links the configuration page's description.
	"(configuration#stale-slot-reaper)",
}

// redisLossRecoveryItem returns the "Recovery from Redis Loss" section of the
// disaster-recovery page, up to the next heading.
func redisLossRecoveryItem(t *testing.T) string {
	t.Helper()
	body := readDocPage(t, filepath.Join(repoRoot(t), "docs/operator-guide/disaster-recovery.md"))
	loc := regexp.MustCompile(`\n#{2,4} Recovery from Redis Loss\n`).FindStringIndex(body)
	if loc == nil {
		t.Fatalf("docs/operator-guide/disaster-recovery.md has no \"Recovery from Redis Loss\" heading")
	}
	rest := body[loc[1]:]
	if next := regexp.MustCompile(`\n#{2,4} `).FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return rest
}

// spec: 5.2 (Slot assignment atomicity), 4.6.1 (Orphaned SandboxClaim detection)
// diagnosis: The disaster-recovery page states the stale slot reaper's
//
//	recovery bound without the condition that governs it after an outage
//	through which Redis kept its data. The recovery rebuild deletes each listed
//	pod's rehydrated flag, so the reaper keeps the member until the pod's next
//	reservation or release, and it always keeps a pod's last member. An
//	operator reading an unconditional 20-minute bound expects a recovery the
//	platform does not perform, and the page disagrees with the configuration
//	page.
func TestRedisLossRecoveryDocQualifiesStaleSlotReaperBound(t *testing.T) {
	flat := strings.Join(strings.Fields(redisLossRecoveryItem(t)), " ")
	for _, phrase := range redisLossReaperRequiredPhrases {
		if !strings.Contains(flat, phrase) {
			t.Errorf("Recovery from Redis Loss section is missing %q", phrase)
		}
	}
	if strings.Contains(flat, "The gateway's stale slot reaper frees each such slot after") {
		t.Errorf("Recovery from Redis Loss section states an unconditional stale slot reaper bound")
	}
}
