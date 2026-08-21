// SPDX-License-Identifier: MIT

package tier0_static

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The claim register is generator-produced, so the committed file and the
// generator are two spellings of one statement. Editing the file alone leaves
// the next seeding run re-emitting every retired row and dropping every added
// one, which is a divergence nothing else in the tree reports: the register's
// other gates read the committed file and pass, and the generator is only run
// by hand. This gate runs the generator and requires the result to be
// byte-identical to the committed register, which is what holds the two halves
// together.

// claimRegisterGeneratorPath is the repo-relative path of the generator the
// register's own header documents.
const claimRegisterGeneratorPath = "scripts/seed-claim-register.py"

// claimRegisterSeedTimeout bounds the generator run. It parses one markdown
// document and writes one JSON file, so a run that reaches this bound is hung
// rather than slow.
const claimRegisterSeedTimeout = 60 * time.Second

// spec: 28.4 (claim register)
// diagnosis: the committed claim register and the generator that produces it
// disagree. Either a row was edited into the register with no row source in the
// generator, so the next seeding run drops it, or a row source the generator
// still carries re-emits a row the register retired. The register is read as
// the work queue for the steps that follow, and a queue only one of its two
// producers agrees with cannot be worked.
func TestClaimRegisterIsReproducibleFromItsGenerator(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)

	generator := filepath.Join(root, claimRegisterGeneratorPath)
	if _, err := os.Stat(generator); err != nil {
		t.Fatalf("%s: %v", claimRegisterGeneratorPath, err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is not on PATH, so the generator the register documents cannot be run: %v", err)
	}

	committed, err := os.ReadFile(filepath.Join(root, claimRegisterPath))
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}

	out := filepath.Join(t.TempDir(), "claim-map.json")
	ctx, cancel := context.WithTimeout(context.Background(), claimRegisterSeedTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, generator, "--out", out)
	cmd.Dir = root
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s --out: %v\n%s", claimRegisterGeneratorPath, err, combined)
	}

	seeded, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the generator wrote no register: %v", err)
	}
	if !bytes.Equal(seeded, committed) {
		t.Errorf("%s is not what %s produces; re-run `%s --out %s` and reconcile the row sources with the rows",
			claimRegisterPath, claimRegisterGeneratorPath, claimRegisterGeneratorPath, claimRegisterPath)
		for _, f := range claimRegisterRowDifferences(t, seeded, committed) {
			t.Errorf("%s", f)
		}
	}
}

// claimRegisterRowDifferences names the rows the two registers disagree on and
// which side produced or omitted each, so a failure points at the row source to
// reconcile rather than at a byte offset. A row the generator emits and the
// committed register omits was retired in the file with its row source left in
// the generator; a row the committed register carries and the generator omits
// was added to the file with no row source behind it.
func claimRegisterRowDifferences(t *testing.T, seeded, committed []byte) []string {
	t.Helper()
	seededRows, err := claimRegisterRows(seeded)
	if err != nil {
		return []string{fmt.Sprintf("the generator's output does not parse as a register: %v", err)}
	}
	committedRows, err := claimRegisterRows(committed)
	if err != nil {
		return []string{fmt.Sprintf("the committed register does not parse: %v", err)}
	}

	var findings []string
	for name, row := range seededRows {
		other, ok := committedRows[name]
		switch {
		case !ok:
			findings = append(findings, fmt.Sprintf(
				"the generator emits the row %q and %s does not carry it, so a row source the generator still holds re-emits a retired row",
				name, claimRegisterPath))
		case row != other:
			findings = append(findings, fmt.Sprintf(
				"the row %q differs: the generator emits %+v and %s carries %+v",
				name, row, claimRegisterPath, other))
		}
	}
	for name := range committedRows {
		if _, ok := seededRows[name]; !ok {
			findings = append(findings, fmt.Sprintf(
				"%s carries the row %q and the generator emits no such row, so the row has no row source behind it",
				claimRegisterPath, name))
		}
	}
	sort.Strings(findings)
	return findings
}

// claimRegisterRows keys a register's rows by their claim text.
func claimRegisterRows(body []byte) (map[string]claim, error) {
	var doc claimRegister
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	if doc.Claims == nil {
		return nil, fmt.Errorf("the document declares no claims block")
	}
	rows := map[string]claim{}
	for _, c := range *doc.Claims {
		rows[c.Claim] = c
	}
	return rows, nil
}

// spec: 28.4 (claim register)
// diagnosis: the reproducibility gate's difference report is broken. A
// divergence between the register and its generator would fail the run without
// naming the row or the side that produced it, which leaves the reconciliation
// to a byte comparison of a generated file.
func TestClaimRegisterDifferenceReportNamesTheRowAndItsSide(t *testing.T) {
	t.Parallel()
	register := func(rows ...claim) []byte {
		body, err := json.Marshal(claimRegister{Kind: "claim-map", Version: 1, Claims: &rows})
		if err != nil {
			t.Fatalf("marshal register: %v", err)
		}
		return body
	}
	kept := claim{Claim: "Fenced interrupt", Status: "WIRED", Surface: "pkg/adapter/lifecycle.go"}
	retired := claim{Claim: "Slot-qualified interrupt", Status: "UNWIRED", DeferralID: "R24"}
	added := claim{Claim: "Per-session credential rotation", Status: "WIRED", Surface: "pkg/adapter/credentials.go"}

	cases := map[string]struct {
		seeded, committed []byte
		want              string
	}{
		"a retired row a row source still emits": {
			seeded:    register(kept, retired),
			committed: register(kept),
			want:      `the generator emits the row "Slot-qualified interrupt" and tests/claim-map.json does not carry it, so a row source the generator still holds re-emits a retired row`,
		},
		"an added row with no row source": {
			seeded:    register(kept),
			committed: register(kept, added),
			want:      `tests/claim-map.json carries the row "Per-session credential rotation" and the generator emits no such row, so the row has no row source behind it`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := claimRegisterRowDifferences(t, tc.seeded, tc.committed)
			for _, f := range got {
				if f == tc.want {
					return
				}
			}
			t.Errorf("the report did not name the divergence; got=%v, want %q", got, tc.want)
		})
	}

	if got := claimRegisterRowDifferences(t, register(kept), register(kept)); len(got) != 0 {
		t.Errorf("the report named a difference between two identical registers: %v", got)
	}
}
