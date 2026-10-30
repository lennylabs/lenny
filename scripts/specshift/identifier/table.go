// SPDX-License-Identifier: MIT

package identifier

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// channelSectionPrefix is the specification file the naming table is
// written in. The table is normative there, so the pass reads it out of
// the tree it rewrites rather than restating it: a second copy would
// let the substitution the pass writes and the spelling the
// specification states disagree, and the identifier-resolution gate
// reads the tree rather than this package.
const channelSectionPrefix = "spec/28"

// Carrier names the surface a naming-table row states a spelling for.
// It is a closed set, because the pass selects a row by the form of the
// site it reached and a carrier it does not know is a carrier whose
// sites it cannot select rows for.
type Carrier string

// The carriers the naming table states a spelling for.
const (
	// CarrierProtoRPC is the RPC name of the proto service definition,
	// which is also the method component of a gRPC full-method literal.
	CarrierProtoRPC Carrier = "proto-rpc"
	// CarrierGoSymbol is the Go identifier stem, which prose naming the
	// type or the method carries as well.
	CarrierGoSymbol Carrier = "go-symbol"
	// CarrierSocket is the unix socket token.
	CarrierSocket Carrier = "socket"
	// CarrierManifestKey is the runtime manifest key.
	CarrierManifestKey Carrier = "manifest-key"
	// CarrierFlag is the command-line flag.
	CarrierFlag Carrier = "flag"
	// CarrierPath is the file-name stem, which is the carrier of the
	// occurrence in a tracked path.
	CarrierPath Carrier = "path"
	// CarrierMetric is the metric name stem.
	CarrierMetric Carrier = "metric"
)

// Carriers returns every carrier in a stable order.
func Carriers() []Carrier {
	return []Carrier{
		CarrierProtoRPC, CarrierGoSymbol, CarrierSocket,
		CarrierManifestKey, CarrierFlag, CarrierPath, CarrierMetric,
	}
}

// Valid reports whether the name is one of them.
func (c Carrier) Valid() bool {
	for _, known := range Carriers() {
		if c == known {
			return true
		}
	}
	return false
}

// carrierToken states the token form a carrier writes a spelling in,
// together with the prose a failure names the form by.
//
// The form is a property of the cell alone, so a row is held to it
// whatever the tree around it holds. That is what the check needs to
// be: a rename the passes have carried to completion writes the
// canonical spelling everywhere and the retired spelling nowhere, so a
// correctly authored row and a mis-authored one reach the same site
// count of zero and no count taken from the tree separates them. The
// mis-authored row the check exists to name is mis-authored lexically,
// a flag cell carrying the dashes the shell prefixes the flag with
// being the case that motivated it, and the cell states that on its
// own.
//
// Each form is the alphabet and the opening class of the carrier's
// tokens rather than a full naming convention, so the rule rejects a
// cell no carrier could write and admits every spelling a carrier does.
type carrierToken struct {
	form  *regexp.Regexp
	prose string
}

// carrierTokens states that form for every carrier. A carrier absent
// from the map has no form rule, which rowFrom reads as accepting any
// non-empty cell; every carrier of the closed set is present.
var carrierTokens = map[Carrier]carrierToken{
	CarrierProtoRPC: {
		form:  regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`),
		prose: "an upper camel-case RPC name",
	},
	CarrierGoSymbol: {
		form:  regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`),
		prose: "an exported Go identifier stem",
	},
	CarrierSocket: {
		form:  regexp.MustCompile(`^@?[a-z0-9]+(?:[-_.][a-z0-9]+)*$`),
		prose: "a lowercase socket token, which opens with the abstract-namespace at sign or with the token itself",
	},
	CarrierManifestKey: {
		form:  regexp.MustCompile(`^[a-z][A-Za-z0-9]*(?:-[a-z][A-Za-z0-9]*)*$`),
		prose: "a manifest key opening lowercase",
	},
	CarrierFlag: {
		form:  regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`),
		prose: "a lowercase hyphenated flag name, written without the dashes the shell prefixes it with",
	},
	CarrierPath: {
		form:  regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`),
		prose: "a lowercase file-name stem",
	},
	CarrierMetric: {
		form:  regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`),
		prose: "a metric name stem",
	},
}

// wellFormed reports whether a spelling is written in the carrier's
// token form.
func (c Carrier) wellFormed(spelling string) bool {
	token, stated := carrierTokens[c]
	return !stated || token.form.MatchString(spelling)
}

// tokenProse renders the carrier's token form for a failure message.
func (c Carrier) tokenProse() string {
	return carrierTokens[c].prose
}

// carrierNames renders the closed set for a failure message.
func carrierNames() string {
	names := make([]string, 0, len(Carriers()))
	for _, c := range Carriers() {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}

// Row is one naming-table row: the channel a spelling denotes, the
// carrier the spelling is written in, the spelling being retired, and
// the spelling that replaces it there.
type Row struct {
	Channel   string
	Carrier   Carrier
	Retired   string
	Canonical string
}

// Table is the naming table, indexed by retired spelling.
type Table struct {
	rows     []Row
	byRetire map[string][]Row
	channels map[string]bool
	// mentions holds, per specification file, the byte span of every
	// naming-table row. A retired spelling standing in the row that
	// retires it is the declaration of that spelling rather than a
	// reference to the channel, so it is no site: the pass reads the
	// column as data, and the table's own route out of the tree is the
	// change that empties it.
	mentions map[string][][2]int
}

// Retired returns every retired spelling the table carries, longest
// first, which is the order the matcher reads a site in: one retired
// spelling is a prefix of another whenever a carrier writes a stem and a
// compound of the same stem, and the shorter match would take the
// occurrence number the longer one is registered under.
func (t *Table) Retired() []string {
	out := make([]string, 0, len(t.byRetire))
	for spelling := range t.byRetire {
		out = append(out, spelling)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// Canonical returns every canonical spelling the table carries, longest
// first, which is the order Retired states for the same reason: one
// canonical spelling is a prefix of another wherever a carrier writes a
// stem and a compound of that stem, and the shorter match would consume
// the span the longer one stands on.
func (t *Table) Canonical() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(t.rows))
	for _, row := range t.rows {
		if seen[row.Canonical] {
			continue
		}
		seen[row.Canonical] = true
		out = append(out, row.Canonical)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// Declares reports whether the table names the channel.
func (t *Table) Declares(channel string) bool { return t.channels[channel] }

// rowsFor returns the rows a retired spelling carries for one channel.
func (t *Table) rowsFor(retired, channel string) []Row {
	var out []Row
	for _, row := range t.byRetire[retired] {
		if row.Channel == channel {
			out = append(out, row)
		}
	}
	return out
}

// identifierExpr matches a whole cell that is a canonical identifier,
// which the naming law writes uppercase and hyphenated under one of the
// class prefixes.
var identifierExpr = regexp.MustCompile(`^(?:LNK|CH|REG)-[A-Z0-9]+(?:-[A-Z0-9]+)*$`)

// tableColumns are the naming table's column headings, lowercased. The
// table is recognized by them rather than by its position in the file,
// because the specification section carries several tables and a
// positional rule would read whichever one an edit moved first.
var tableColumns = []string{"channel", "carrier", "retired spelling", "canonical spelling"}

// rowExpr matches a markdown table row.
var rowExpr = regexp.MustCompile(`^[ \t]*\|`)

// delimiterExpr matches the alignment row that follows a table heading.
var delimiterExpr = regexp.MustCompile(`^[ \t]*\|[ \t:|-]*\|[ \t]*$`)

// LoadTable reads the naming table out of the specification files of
// the tree.
//
// It fails on a tree carrying no communication-channels section and on
// a section carrying no naming table, rather than reporting an empty
// table. An empty table resolves no site, so the pass would abort at
// every occurrence in the tree, which reads as a register that has not
// been seeded rather than as a tree the pass cannot run against yet.
func LoadTable(ctx context.Context, list scope.Lister, read scope.FileReader) (*Table, error) {
	if list == nil || read == nil {
		return nil, fmt.Errorf("read the naming table: a lister and a reader are required")
	}
	paths, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the naming table: %w", err)
	}
	sort.Strings(paths)
	table := &Table{
		byRetire: map[string][]Row{},
		channels: map[string]bool{},
		mentions: map[string][][2]int{},
	}
	files := 0
	for _, path := range paths {
		if !strings.HasPrefix(path, channelSectionPrefix) || !strings.HasSuffix(path, ".md") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read the naming table: %w", err)
		}
		data, err := read(path)
		if err != nil {
			return nil, fmt.Errorf("read specification file %s: %w", path, err)
		}
		files++
		rows, spans, err := rowsIn(path, string(data))
		if err != nil {
			return nil, err
		}
		table.rows = append(table.rows, rows...)
		if len(spans) > 0 {
			table.mentions[path] = spans
		}
	}
	if files == 0 {
		return nil, fmt.Errorf("read the naming table: the tree carries no %s* specification file", channelSectionPrefix)
	}
	if len(table.rows) == 0 {
		return nil, fmt.Errorf("read the naming table: %d file(s) under %s* carry no table headed %s",
			files, channelSectionPrefix, strings.Join(tableColumns, " | "))
	}
	for _, row := range table.rows {
		table.byRetire[row.Retired] = append(table.byRetire[row.Retired], row)
		table.channels[row.Channel] = true
	}
	return table, nil
}

// rowsIn returns the naming-table rows one specification file states,
// together with the byte span of each row, which is the span the pass
// reads no site in.
func rowsIn(path, content string) ([]Row, [][2]int, error) {
	var out []Row
	var spans [][2]int
	var columns map[string]int
	at := 0
	for n, line := range strings.Split(content, "\n") {
		lo := at
		at += len(line) + 1
		if !rowExpr.MatchString(line) {
			columns = nil
			continue
		}
		if columns == nil {
			columns = headingColumns(line)
			if columns != nil {
				spans = append(spans, [2]int{lo, lo + len(line)})
			}
			continue
		}
		spans = append(spans, [2]int{lo, lo + len(line)})
		if delimiterExpr.MatchString(line) {
			continue
		}
		row, err := rowFrom(cells(line), columns)
		if err != nil {
			return nil, nil, fmt.Errorf("naming table %s line %d: %w", path, n+1, err)
		}
		out = append(out, row)
	}
	return out, spans, nil
}

// mentioned reports whether an offset of one file stands inside a
// naming-table row.
func (t *Table) mentioned(target string, at int) bool {
	for _, span := range t.mentions[target] {
		if at >= span[0] && at < span[1] {
			return true
		}
	}
	return false
}

// headingColumns returns the column index of each naming-table column,
// or nil when the row is not the naming table's heading.
func headingColumns(line string) map[string]int {
	found := map[string]int{}
	for i, cell := range cells(line) {
		name := strings.ToLower(strings.TrimSpace(cell))
		for _, column := range tableColumns {
			if name == column {
				found[column] = i
			}
		}
	}
	if len(found) != len(tableColumns) {
		return nil
	}
	return found
}

// cells splits a markdown table row into its cells.
func cells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	return strings.Split(trimmed, "|")
}

// rowFrom reads one naming-table row. Every defect fails the run rather
// than dropping the row: a row the reader skipped is a spelling with no
// substitution, and the pass would abort at every site carrying it with
// the operator sent to the sense register rather than to the table.
func rowFrom(raw []string, columns map[string]int) (Row, error) {
	value := func(column string) string {
		i := columns[column]
		if i >= len(raw) {
			return ""
		}
		return strings.Trim(strings.TrimSpace(raw[i]), "`*_")
	}
	row := Row{
		Channel:   value("channel"),
		Carrier:   Carrier(value("carrier")),
		Retired:   value("retired spelling"),
		Canonical: value("canonical spelling"),
	}
	if !identifierExpr.MatchString(row.Channel) {
		return Row{}, fmt.Errorf("the channel cell %q is not a canonical identifier", row.Channel)
	}
	if !row.Carrier.Valid() {
		return Row{}, fmt.Errorf("the carrier cell %q is none of %s", row.Carrier, carrierNames())
	}
	if row.Retired == "" || row.Canonical == "" {
		return Row{}, fmt.Errorf("the row for %s states the retired spelling %q and the canonical spelling %q, and a row states both",
			row.Channel, row.Retired, row.Canonical)
	}
	if row.Retired == row.Canonical {
		return Row{}, fmt.Errorf("the row for %s retires %q to itself, so a site carrying it has no substitution",
			row.Channel, row.Retired)
	}
	for _, cell := range []struct{ column, spelling string }{
		{"retired spelling", row.Retired},
		{"canonical spelling", row.Canonical},
	} {
		if !row.Carrier.wellFormed(cell.spelling) {
			return Row{}, fmt.Errorf("the row for %s states the %s %q, which is not %s, so the %s carrier writes no token the pass could read or splice there",
				row.Channel, cell.column, cell.spelling, row.Carrier.tokenProse(), row.Carrier)
		}
	}
	return row, nil
}
