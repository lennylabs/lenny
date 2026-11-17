// SPDX-License-Identifier: MIT

package docgate

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
)

// verdict is the outcome of comparing a quoted value with a block's.
type verdict int

const (
	match verdict = iota
	mismatch
	undecided
)

// valueColumns maps a table column header to the block field its cells
// quote.
var valueColumns = map[string]string{
	"default": "default",
	"value":   "value",
	"limit":   "value",
	"unit":    "unit",
}

// proseDefault matches a default quoted right after a setting's name in
// prose, such as "(default `30`)" or ", which defaults to `30s`": the word
// default within 30 characters of the name, with no sentence end and no
// other code span between them.
var proseDefault = regexp.MustCompile("(?i)^[^.;`]{0,30}?\\bdefault(?:s|\\s+value)?(?:\\s+(?:is|of|to)|:)?\\s+(`[^`]+`|[0-9][0-9.]*[A-Za-z%]*)")

// checkValues compares the values a span's context quotes with the block
// the span names: the cells of its table row under a Default, Value,
// Limit, or Unit column, and a default quoted after it in prose. A row is
// compared only when the span is the whole of the row's first cell, which
// names what the row describes. A name two blocks share is compared with
// neither.
func checkValues(r *Result, p *Page, s Span, blocks []*defs.Block) {
	if len(blocks) != 1 {
		return
	}
	b := blocks[0]
	if s.Row != nil {
		if s.Column != 0 || strings.TrimSpace(s.Before) != "" || strings.TrimSpace(s.After) != "" {
			return
		}
		for _, header := range slices.Sorted(maps.Keys(s.Row)) {
			cell := s.Row[header]
			field, ok := valueColumns[header]
			if !ok || header == headerKey(s.Header) || !b.Has(field) {
				continue
			}
			compareField(r, p, s, b, field, cell)
		}
		return
	}
	if b.Kind != defs.KindSetting {
		return
	}
	if m := proseDefault.FindStringSubmatch(s.After); m != nil {
		compareField(r, p, s, b, "default", m[1])
	}
}

// compareField compares one quoted value with a block's field and records
// a mismatch as a problem and a value it cannot compare as undecided.
func compareField(r *Result, p *Page, s Span, b *defs.Block, field, quoted string) {
	value, ok := cellValue(quoted)
	if !ok {
		if strings.TrimSpace(quoted) != "" {
			r.Undecided = append(r.Undecided, Problem{Path: p.Path, Line: s.Line, Message: fmt.Sprintf(
				"the %s quoted for `%s` (%s) is not a single value, so it is not compared with the definition block's %s", field, b.Name, strings.TrimSpace(quoted), b.Text(field))})
		}
		return
	}
	var v verdict
	if field == "unit" {
		v = compareUnit(value, b.Text("unit"))
	} else {
		v = compareValue(value, b.Text(field), b.Text("unit"))
	}
	switch v {
	case mismatch:
		r.Problems = append(r.Problems, Problem{Path: p.Path, Line: s.Line, Message: fmt.Sprintf(
			"the %s quoted for `%s` is %q, and the definition block gives %q", field, b.Name, value, b.Text(field))})
	case undecided:
		r.Undecided = append(r.Undecided, Problem{Path: p.Path, Line: s.Line, Message: fmt.Sprintf(
			"the %s quoted for `%s` is %q, and the definition block gives %q; the gate cannot tell whether they agree", field, b.Name, value, b.Text(field))})
	}
}

// emptyCells are the cells that quote no value.
var emptyCells = map[string]bool{"": true, "-": true, "--": true, "—": true, "n/a": true}

// cellValue returns the single value a cell or a prose match quotes: the
// content of a cell that is one code span, or a cell that is one word. It
// reports false for an empty cell and for a cell that holds more.
func cellValue(cell string) (string, bool) {
	cell = strings.TrimSpace(cell)
	if emptyCells[strings.ToLower(cell)] {
		return "", false
	}
	if strings.HasPrefix(cell, "`") && strings.HasSuffix(cell, "`") && strings.Count(cell, "`") == 2 {
		return strings.TrimSpace(strings.Trim(cell, "`")), true
	}
	if !strings.ContainsAny(cell, " `") {
		return cell, true
	}
	return "", false
}

var (
	numberUnit = regexp.MustCompile(`^(-?[0-9]+(?:\.[0-9]+)?)\s*([A-Za-z%]*)$`)
	plainWord  = regexp.MustCompile(`^[a-z][a-z_-]*$`)
)

// durationUnits gives the seconds in each duration unit spelling.
var durationUnits = map[string]float64{
	"ms": 0.001, "millisecond": 0.001, "milliseconds": 0.001,
	"s": 1, "sec": 1, "secs": 1, "second": 1, "seconds": 1,
	"m": 60, "min": 60, "mins": 60, "minute": 60, "minutes": 60,
	"h": 3600, "hour": 3600, "hours": 3600,
	"d": 86400, "day": 86400, "days": 86400,
}

// compareValue compares a quoted value with a block's value. Equal text
// matches. Two numbers, or two durations, compare by quantity, with the
// block's unit standing for a bare number's. Two plain words that differ,
// such as two enum values, mismatch. Anything else is undecided.
func compareValue(quoted, want, unit string) verdict {
	q, w := normValue(quoted), normValue(want)
	if q == w {
		return match
	}
	if w == "" {
		return undecided
	}
	qn, qu, qok := splitNumber(q)
	wn, wu, wok := splitNumber(w)
	if qok && wok {
		if qu == "" && wu == "" {
			return equalOr(qn == wn)
		}
		if qu == "" {
			qu = strings.ToLower(unit)
		}
		if wu == "" {
			wu = strings.ToLower(unit)
		}
		qs, qd := durationUnits[qu]
		ws, wd := durationUnits[wu]
		if qd && wd {
			return equalOr(qn*qs == wn*ws)
		}
		if normUnit(qu) == normUnit(wu) {
			return equalOr(qn == wn)
		}
		return undecided
	}
	if plainWord.MatchString(q) && plainWord.MatchString(w) {
		return mismatch
	}
	return undecided
}

func equalOr(equal bool) verdict {
	if equal {
		return match
	}
	return mismatch
}

// compareUnit compares a quoted unit with a block's, treating singular and
// plural and the common abbreviations of a duration as one unit.
func compareUnit(quoted, want string) verdict {
	if want == "" {
		return undecided
	}
	return equalOr(normUnit(normValue(quoted)) == normUnit(normValue(want)))
}

// normValue lower-cases a value and removes quotes and surrounding space.
func normValue(v string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(v), `"'`))
}

// normUnit reduces a unit spelling to one form.
func normUnit(u string) string {
	if s, ok := durationUnits[u]; ok {
		return strconv.FormatFloat(s, 'g', -1, 64) + "s"
	}
	if len(u) > 2 && strings.HasSuffix(u, "s") {
		return u[:len(u)-1]
	}
	return u
}

// splitNumber splits a value into a number and a unit suffix.
func splitNumber(v string) (float64, string, bool) {
	m := numberUnit.FindStringSubmatch(strings.ReplaceAll(v, ",", ""))
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, "", false
	}
	return n, strings.ToLower(m[2]), true
}
