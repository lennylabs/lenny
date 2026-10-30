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
// lines, which is how an occurrence read across a comment wrap is
// recorded, is one member with the same text stored on one line.
//
// The member below carries no reference and no line-number token, so this
// case does not write an occurrence of the line-citation class into a file
// the residual scan reads.
func TestAMemberIsKeyedByItsFoldedText(t *testing.T) {
	t.Parallel()
	wrapped, folded := "the pool selector\nand the claim path", "the pool selector and the claim path"
	if got := register.MemberKey(wrapped); got != folded {
		t.Errorf("the wrapped member keys as %q", got)
	}
	entries := []register.Entry{
		{Member: wrapped, Class: "line-citations", Disposition: register.InClass, Reason: "r"},
		{Member: folded, Class: "line-citations", Disposition: register.InClass, Reason: "r"},
	}
	doc := &register.Document{Kind: register.Kind, Version: register.Version, Class: "line-citations", Entries: &entries}
	if err := doc.Validate(); err == nil {
		t.Error("the validator accepted the same member stored wrapped and unwrapped")
	}
}

// registerDir is the tree that tracks one residual register per class,
// relative to this package.
var registerDir = filepath.Join("..", "..", "..", "tests", "registers")

// trackedRegisters returns the path of every residual register in the
// tree.
func trackedRegisters(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(registerDir)
	if err != nil {
		t.Fatalf("read the tracked register directory: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "residual-") || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		out = append(out, filepath.Join(registerDir, e.Name()))
	}
	if len(out) == 0 {
		t.Fatal("no residual register is tracked, so this case pins nothing")
	}
	return out
}

// TestEveryTrackedResidualRegisterLoadsThroughTheSharedLoader holds every
// register in the tree to the one kind declaration, the one version, and
// the one entry schema, so a register the residual gate writes stays a
// register a pass reads. A second writer with a schema of its own would
// produce a file the other half refuses, and nothing else in the tree
// would fail.
func TestEveryTrackedResidualRegisterLoadsThroughTheSharedLoader(t *testing.T) {
	t.Parallel()
	for _, path := range trackedRegisters(t) {
		rel := filepath.Base(path)
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			doc, err := register.Load(path)
			if err != nil {
				t.Fatalf("load %s through the shared residual-register loader: %v", rel, err)
			}
			if doc.Kind != register.Kind {
				t.Errorf("%s declares kind %q, and the shared loader reads %q", rel, doc.Kind, register.Kind)
			}
			if doc.Version != register.Version {
				t.Errorf("%s declares version %d, and the shared loader reads %d", rel, doc.Version, register.Version)
			}
			// The class a register declares is the class its filename
			// names, so a register cannot be read for one class while
			// carrying another's population.
			wantClass := strings.TrimSuffix(strings.TrimPrefix(rel, "residual-"), ".yaml")
			if doc.Class != wantClass {
				t.Errorf("%s declares class %q, and its path names %q", rel, doc.Class, wantClass)
			}
			if _, err := register.ReadFor(os.ReadFile, path, wantClass); err != nil {
				t.Errorf("read %s for the class its path names: %v", rel, err)
			}
			// Every entry is held to the one entry schema, which is what
			// makes an entry one side writes readable by the other.
			for _, e := range *doc.Entries {
				if err := register.ValidEntry(rel, doc.Class, e); err != nil {
					t.Errorf("%s: %v", rel, err)
				}
				if e.Disposition != register.InClass && e.Disposition != register.Excluded {
					t.Errorf("%s: member %q carries disposition %q", rel, e.Key(), e.Disposition)
				}
			}
		})
	}
}

// TestTheSharedWriterProducesARegisterTheSharedLoaderReads pins the two
// halves of the format against each other, so a register the shared
// writer produces reloads through the shared loader.
func TestTheSharedWriterProducesARegisterTheSharedLoaderReads(t *testing.T) {
	t.Parallel()
	entries := []register.Entry{{
		Member:      "pkg/carrier/carrier.go",
		Class:       "generated-artifacts",
		Disposition: register.InClass,
		Reason:      "producer output that carries no marker",
	}}
	path := filepath.Join(t.TempDir(), "residual-generated-artifacts.yaml")
	if err := register.Write(func(target string, content []byte) error {
		return os.WriteFile(target, content, 0o644)
	}, path, "generated-artifacts", entries); err != nil {
		t.Fatalf("write a register through the shared writer: %v", err)
	}
	doc, err := register.ReadFor(os.ReadFile, path, "generated-artifacts")
	if err != nil {
		t.Fatalf("reload the written register through the shared loader: %v", err)
	}
	if got := doc.Members(); len(got) != 1 || got[0] != "pkg/carrier/carrier.go" {
		t.Fatalf("the reloaded register carries %v", got)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written register: %v", err)
	}
	if !strings.Contains(string(body), "kind: "+register.Kind+"\n") {
		t.Errorf("the written register declares no %q kind:\n%s", register.Kind, body)
	}
}
