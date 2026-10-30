// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The §28.8 matrix completeness check asserts a bijection between the
// channel identifiers in the §28.3 channel register and the rows of the
// §28.8 failure and degradation matrix. §28.8 states the correspondence
// itself: the matrix carries exactly one row per channel identifier in
// the §28.3 channel register, no identifier is missing, and no row names
// an identifier the register does not carry.
//
// The gate reads the bijection in both directions and reports every
// offending member by name rather than a count, because the remedy
// differs per member: a registered identifier with no row is a missing
// degradation statement, a row naming no registered identifier is a row
// left behind by a retirement, and a second row for one identifier is
// two statements an operator cannot choose between.
//
// The register-entry register and the link register are deliberately
// outside the correspondence. A link entry is a connection and a
// register entry is a store key; neither is a channel, and §28.8 states
// that neither carries a row. Widening the gate to read them would make
// the matrix carry rows §28.8 forbids.

// specChannelsPath is the specification file carrying §28.3 and §28.8.
const specChannelsPath = "spec/28_communication-channels.md"

// channelRegisterHeading is the §28.3 subheading above the channel
// register table. The link register and the register-entry register sit
// under their own subheadings and are not read.
const channelRegisterHeading = "#### Channel register"

// degradationMatrixHeading is the §28.8 heading above the matrix table.
const degradationMatrixHeading = "### 28.8 Failure and degradation matrix"

// matrixFailure is one offending member of the bijection, named with the
// direction it fails in so the remedy is unambiguous.
type matrixFailure struct {
	identifier string
	reason     string
}

func (f matrixFailure) String() string {
	return fmt.Sprintf("%s: %s", f.identifier, f.reason)
}

// firstColumnUnder returns the first-column cells of the first markdown
// table below the given heading, in document order, with the header row
// and the alignment row dropped and surrounding backticks stripped. It
// reports an error when the heading is absent or carries no table, so a
// heading renamed out from under the gate fails rather than passing on
// an empty population.
func firstColumnUnder(body, heading string) ([]string, error) {
	lines := strings.Split(body, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("heading %q is absent", heading)
	}
	var cells []string
	inTable := false
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			if inTable {
				break
			}
			if strings.HasPrefix(trimmed, "#") {
				return nil, fmt.Errorf("no table follows heading %q", heading)
			}
			continue
		}
		inTable = true
		cells = append(cells, strings.TrimSpace(strings.SplitN(strings.TrimPrefix(trimmed, "|"), "|", 2)[0]))
	}
	if len(cells) < 2 {
		return nil, fmt.Errorf("no table follows heading %q", heading)
	}
	out := make([]string, 0, len(cells)-2)
	for _, cell := range cells[2:] {
		out = append(out, strings.Trim(cell, "`"))
	}
	return out, nil
}

// matrixCompleteness reports every member on which the register and the
// matrix fail to correspond one to one.
func matrixCompleteness(body string) ([]matrixFailure, error) {
	registered, err := firstColumnUnder(body, channelRegisterHeading)
	if err != nil {
		return nil, err
	}
	rows, err := firstColumnUnder(body, degradationMatrixHeading)
	if err != nil {
		return nil, err
	}

	rowCount := map[string]int{}
	for _, r := range rows {
		rowCount[r]++
	}
	inRegister := map[string]bool{}
	var failures []matrixFailure
	for _, id := range registered {
		inRegister[id] = true
		switch rowCount[id] {
		case 1:
		case 0:
			failures = append(failures, matrixFailure{id, "carried by the §28.3 channel register with no row in the §28.8 matrix"})
		default:
			failures = append(failures, matrixFailure{id, fmt.Sprintf("carried by %d rows of the §28.8 matrix, which states one row per identifier", rowCount[id])})
		}
	}
	var unregistered []string
	for _, r := range rows {
		if !inRegister[r] {
			unregistered = append(unregistered, r)
		}
	}
	sort.Strings(unregistered)
	seen := map[string]bool{}
	for _, r := range unregistered {
		if seen[r] {
			continue
		}
		seen[r] = true
		failures = append(failures, matrixFailure{r, "named by a row of the §28.8 matrix and carried by no entry of the §28.3 channel register"})
	}
	return failures, nil
}

func failureLines(failures []matrixFailure) string {
	out := make([]string, 0, len(failures))
	for _, f := range failures {
		out = append(out, f.String())
	}
	return strings.Join(out, "\n  ")
}

// spec: §28.8 (failure and degradation matrix: exactly one row per
// channel identifier in the §28.3 channel register), §28.3 (channel
// register)
func TestDegradationMatrixCorrespondsOneToOneWithTheChannelRegister(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(specChannelsPath)))
	if err != nil {
		t.Fatalf("read %s: %v", specChannelsPath, err)
	}
	failures, err := matrixCompleteness(string(body))
	if err != nil {
		t.Fatalf("read the §28.3 channel register and the §28.8 matrix: %v", err)
	}
	if len(failures) > 0 {
		t.Errorf("%d channel identifier(s) break the §28.3 register to §28.8 matrix bijection:\n  %s",
			len(failures), failureLines(failures))
	}
}

// matrixFixture renders a two-table document in the layout §28 carries,
// so a case states its register and its matrix as identifier lists.
func matrixFixture(registered, rows []string) string {
	var b strings.Builder
	b.WriteString("### 28.3 Registers\n\n")
	b.WriteString(channelRegisterHeading + "\n\n")
	b.WriteString("| Identifier | Boundary |\n|:--|:--|\n")
	for _, id := range registered {
		fmt.Fprintf(&b, "| `%s` | `intra-pod` |\n", id)
	}
	b.WriteString("\nProse between the register and the matrix.\n\n")
	b.WriteString(degradationMatrixHeading + "\n\n")
	b.WriteString("Prose above the matrix table.\n\n")
	b.WriteString("| Channel | Peer absent |\n|:--|:--|\n")
	for _, id := range rows {
		fmt.Fprintf(&b, "| `%s` | The specification does not state it |\n", id)
	}
	b.WriteString("\nProse below the matrix.\n")
	return b.String()
}

// spec: §28.8 (one row per channel identifier), §28.3 (channel register)
func TestDegradationMatrixCompletenessCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		registered []string
		rows       []string
		want       []string
	}{
		{
			name:       "a bijection passes",
			registered: []string{"CH-ATTACH", "CH-FENCE"},
			rows:       []string{"CH-ATTACH", "CH-FENCE"},
		},
		{
			name:       "row order is not the register order",
			registered: []string{"CH-ATTACH", "CH-FENCE"},
			rows:       []string{"CH-FENCE", "CH-ATTACH"},
		},
		{
			name:       "an identifier added to the register with no matrix row",
			registered: []string{"CH-ATTACH", "CH-NEWCHANNEL"},
			rows:       []string{"CH-ATTACH"},
			want:       []string{"CH-NEWCHANNEL"},
		},
		{
			name:       "a matrix row for a retired identifier",
			registered: []string{"CH-ATTACH"},
			rows:       []string{"CH-ATTACH", "CH-RETIRED"},
			want:       []string{"CH-RETIRED"},
		},
		{
			name:       "a duplicate row for one identifier",
			registered: []string{"CH-ATTACH", "CH-FENCE"},
			rows:       []string{"CH-ATTACH", "CH-FENCE", "CH-ATTACH"},
			want:       []string{"CH-ATTACH"},
		},
		{
			name:       "an empty matrix reports every registered identifier",
			registered: []string{"CH-ATTACH", "CH-FENCE"},
			rows:       nil,
			want:       []string{"CH-ATTACH", "CH-FENCE"},
		},
		{
			name:       "both directions fail at once",
			registered: []string{"CH-ATTACH", "CH-NEWCHANNEL"},
			rows:       []string{"CH-ATTACH", "CH-RETIRED"},
			want:       []string{"CH-NEWCHANNEL", "CH-RETIRED"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			failures, err := matrixCompleteness(matrixFixture(tc.registered, tc.rows))
			if err != nil {
				t.Fatalf("read the fixture register and matrix: %v", err)
			}
			var got []string
			for _, f := range failures {
				got = append(got, f.identifier)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("failures %v, want %v:\n  %s", got, want, failureLines(failures))
			}
		})
	}
}

// spec: §28.8 (the matrix reads the §28.3 channel register), §28.3
// (registers)
func TestDegradationMatrixCompletenessRejectsAnAbsentHeading(t *testing.T) {
	t.Parallel()
	body := matrixFixture([]string{"CH-ATTACH"}, []string{"CH-ATTACH"})
	for _, heading := range []string{channelRegisterHeading, degradationMatrixHeading} {
		t.Run(heading, func(t *testing.T) {
			t.Parallel()
			if _, err := matrixCompleteness(strings.Replace(body, heading, "#### Renamed", 1)); err == nil {
				t.Errorf("a document with no %q heading was read as a population rather than reported", heading)
			}
		})
	}
}
