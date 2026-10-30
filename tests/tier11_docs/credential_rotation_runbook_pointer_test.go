// SPDX-License-Identifier: MIT

package tier11_docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The credential-rotation runbook check reads the operator-facing half of the
// intra-pod reduction.
//
// The `credentials_rotated` and `credentials_acknowledged` frames the rotation
// handshake consists of are stated by the `CH-RUNTIMEOPS` contract card in
// §28.5.3. The runbook states the handshake twice, in the `symptoms` entry an
// operator reads out of the alert catalog and in the sentence that opens its
// body, and each statement names the section that owns the frames. Neither
// carries a line citation or a retired anchor, so no migration pass repairs
// them: the section they used to name keeps its heading and its anchor, and the
// citation resolves onto prose that no longer states the handshake.
//
// Both sites are checked rather than one. They are read on different routes,
// the front-matter entry through the runbook search surface and the body
// sentence by an operator who has already opened the page, and a repointing
// that reaches only one of them leaves the two statements of one handshake
// naming two owners.
//
// Each site must also name the channel. §28.5.3 holds every intra-pod card, so
// a pointer that names the section alone leaves an operator reading four cards
// to find the frames the alert is about.

// rotationHandshakeSite matches either statement of the rotation handshake in
// the runbook, in the front-matter entry and in the body sentence alike.
var rotationHandshakeSite = regexp.MustCompile(`credential rotation handshake failed for an active session`)

// rotationHandshakeOwner is the card heading that owns the rotation frames, and
// rotationHandshakeChannel the identifier of the channel that carries them.
const (
	rotationHandshakeOwner   = "28.5.3"
	rotationHandshakeChannel = "CH-RUNTIMEOPS"
	rotationHandshakeRetired = "§4.7"
)

// rotationRunbookFaults returns what is wrong with the runbook's two statements
// of the rotation handshake, and returns nothing when both name the owning card
// and its channel. A body that carries neither statement is itself a fault: the
// sentences the reduction falsified have been reworded or deleted, and the
// check would otherwise pass by reading nothing.
func rotationRunbookFaults(lines []string) []string {
	var faults []string
	inFront, frontClosed := false, false
	var front, body int
	for i, l := range lines {
		if strings.TrimSpace(l) == "---" && !frontClosed {
			if i == 0 {
				inFront = true
				continue
			}
			if inFront {
				inFront, frontClosed = false, true
			}
			continue
		}
		if !rotationHandshakeSite.MatchString(l) {
			continue
		}
		where := "the body sentence"
		if inFront {
			where = "the front-matter symptoms entry"
			front++
		} else {
			body++
		}
		switch {
		case strings.Contains(l, rotationHandshakeRetired):
			faults = append(faults, where+" names "+rotationHandshakeRetired+
				" for a handshake the §"+rotationHandshakeOwner+" card owns")
		case !strings.Contains(l, "§"+rotationHandshakeOwner):
			faults = append(faults, where+" names no §"+rotationHandshakeOwner+" card for the handshake")
		case !strings.Contains(l, rotationHandshakeChannel):
			faults = append(faults, where+" points at §"+rotationHandshakeOwner+" without naming "+
				rotationHandshakeChannel+", so an operator cannot tell which card applies")
		}
	}
	if front == 0 {
		faults = append(faults, "carries no front-matter symptoms entry for the rotation handshake")
	}
	if body == 0 {
		faults = append(faults, "carries no body sentence stating the rotation handshake")
	}
	return faults
}

const rotationRunbook = "docs/runbooks/credential-rotation-failure.md"

// spec: 28.5.3 (intra-pod cards, CH-RUNTIMEOPS)
// diagnosis: the credential-rotation runbook still names the section that gave
// up the rotation frames, or names the owning section without the channel, so
// an operator following the alert lands on prose that does not state the
// handshake the alert is about.
func TestCredentialRotationRunbookNamesTheCardThatOwnsTheHandshake(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, rotationRunbook))
	if err != nil {
		t.Fatalf("read %s: %v", rotationRunbook, err)
	}
	for _, fault := range rotationRunbookFaults(strings.Split(string(body), "\n")) {
		t.Errorf("%s: %s", rotationRunbook, fault)
	}
}

// spec: 28.5.3 (intra-pod cards, CH-RUNTIMEOPS)
// diagnosis: the runbook points at a card heading or a channel the channels
// specification does not declare, so the repointed sentence sends an operator
// nowhere.
func TestCredentialRotationRunbookPointerResolves(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	cards, err := os.ReadFile(filepath.Join(root, "spec/28_communication-channels.md"))
	if err != nil {
		t.Fatalf("read the channels specification: %v", err)
	}
	heading := regexp.MustCompile(`(?m)^#{2,6}\s+` + regexp.QuoteMeta(rotationHandshakeOwner) + `\s`)
	if !heading.MatchString(string(cards)) {
		t.Errorf("the channels specification declares no §%s heading for the runbook to point at",
			rotationHandshakeOwner)
	}
	if !strings.Contains(string(cards), "**`"+rotationHandshakeChannel+"`**") {
		t.Errorf("the channels specification declares no %s card", rotationHandshakeChannel)
	}
	for _, frame := range []string{"`credentials_rotated`", "`credentials_acknowledged`"} {
		if !strings.Contains(string(cards), frame) {
			t.Errorf("the channels specification does not state %s, which the runbook's handshake consists of",
				frame)
		}
	}
}

// spec: 28.5.3 (intra-pod cards, CH-RUNTIMEOPS)
// diagnosis: the runbook matcher reports a corrected runbook as a fault, or
// passes a runbook that still names the section the frames left, that names no
// channel, or that repoints one site and not the other, so the check above is
// either red on correct prose or inert.
func TestRotationRunbookMatcherReadsBothSites(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		fixture string
		want    int
	}{
		{"both sites at the owning card are accepted", "runbook-pointer-owning-card.md", 0},
		{"both sites at the section the frames left are reported", "runbook-pointer-retired-owner.md", 2},
		{"sites that name no channel are reported", "runbook-pointer-unnamed-card.md", 2},
		{"a runbook repointed in the body alone is reported", "runbook-pointer-body-only.md", 1},
		{"a runbook that lost both statements is reported", "runbook-pointer-absent-statement.md", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rotationRunbookFaults(ownershipFixture(t, tc.fixture))
			if len(got) != tc.want {
				t.Errorf("fixture %s: %d fault(s) reported, want %d: %v", tc.fixture, len(got), tc.want, got)
			}
		})
	}
	if len(rotationRunbookFaults(nil)) != 2 {
		t.Errorf("an empty body reported fewer than both missing sites; the check would pass vacuously " +
			"on a deleted runbook")
	}
}
