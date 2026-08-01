// SPDX-License-Identifier: MIT

package register_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/register"
)

// The residual register has one loader, one entry schema, and one kind
// declaration, shared by the residual gate that writes the tracked
// registers and by the passes that read them. A second kind string on
// either side is a register one half writes and the other half refuses.
//
// These cases carry no spec annotation: the residual register is
// migration tooling for the repository's own records rather than a
// platform behavior.

// TestKindIsTheDeclarationTheTrackedRegistersCarry pins the kind
// constant to the string the tracked registers declare, read from a
// register in the tree rather than restated here.
func TestKindIsTheDeclarationTheTrackedRegistersCarry(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "registers", "residual-generated-artifacts.yaml"))
	if err != nil {
		t.Fatalf("read a tracked residual register: %v", err)
	}
	declared := ""
	for _, line := range strings.Split(string(body), "\n") {
		if rest, ok := strings.CutPrefix(line, "kind: "); ok {
			declared = strings.TrimSpace(rest)
			break
		}
	}
	if declared == "" {
		t.Fatal("the tracked register declares no kind")
	}
	if declared != register.Kind {
		t.Errorf("the tracked registers declare kind %q and the loader reads %q, so the loader reads none of them",
			declared, register.Kind)
	}
}

// TestReadRejectsARegisterOfAnotherKind pins that the kind check is what
// separates a residual register from any other document, so a document
// that is not one fails to load rather than loading as a register with
// no entries.
func TestReadRejectsARegisterOfAnotherKind(t *testing.T) {
	t.Parallel()
	body := "kind: line-citation-count-baseline\nversion: 1\nclass: line-citations\nentries: []\n"
	read := func(string) ([]byte, error) { return []byte(body), nil }
	if _, err := register.Read(read, "residual-line-citations.yaml"); err == nil {
		t.Error("the loader accepted a document of another kind")
	}
}

// TestReadForHoldsARegisterToTheClassItIsReadFor pins that a register
// copied to another class's path is refused, because loading it would
// exempt that class's whole population.
func TestReadForHoldsARegisterToTheClassItIsReadFor(t *testing.T) {
	t.Parallel()
	entries := []register.Entry{{
		Member: "pkg/carrier/carrier.go", Class: "generated-artifacts",
		Disposition: register.Excluded, Reason: "hand-authored carrier",
	}}
	body, err := register.Render("residual-generated-artifacts.yaml", "generated-artifacts", entries)
	if err != nil {
		t.Fatalf("render a register: %v", err)
	}
	read := func(string) ([]byte, error) { return []byte(body), nil }
	if _, err := register.ReadFor(read, "residual-generated-artifacts.yaml", "generated-artifacts"); err != nil {
		t.Fatalf("read a register for the class it declares: %v", err)
	}
	if _, err := register.ReadFor(read, "residual-line-citations.yaml", "line-citations"); err == nil {
		t.Error("the loader accepted a register declaring another class")
	}
}

// TestAMemberIsKeyedByItsFoldedText pins that a member stored across two
// lines, which is how a citation read across a comment wrap is recorded,
// is one member with the same text stored on one line.
func TestAMemberIsKeyedByItsFoldedText(t *testing.T) {
	t.Parallel()
	if got := register.MemberKey("§7.2\n(table line 123"); got != "§7.2 (table line 123" {
		t.Errorf("the wrapped member keys as %q", got)
	}
	entries := []register.Entry{
		{Member: "§7.2\n(table line 123", Class: "line-citations", Disposition: register.InClass, Reason: "r"},
		{Member: "§7.2 (table line 123", Class: "line-citations", Disposition: register.InClass, Reason: "r"},
	}
	doc := &register.Document{Kind: register.Kind, Version: register.Version, Class: "line-citations", Entries: &entries}
	if err := doc.Validate(); err == nil {
		t.Error("the validator accepted the same member stored wrapped and unwrapped")
	}
}
