// SPDX-License-Identifier: MIT

package journeys

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SelectPath parses out as JSON and returns the values the path selects.
//
// A path is a dot-separated list of segments. A name segment selects an
// object's field, a number selects an array element, and * selects every
// element of an array or every value of an object, flattening the
// result. A segment may end in a filter, [field=value], which keeps the
// array elements, or the objects, whose field renders as value; a dot
// inside the brackets does not separate segments. "items.*.status.phase"
// selects the phase of every item, "status.readyCount" selects one value,
// and "items[user_id=carol@globex.com].role" selects the role of the
// item whose user_id is carol@globex.com. A segment that does not exist
// selects nothing, which is not an error, so an expectation on a missing
// field fails with "selected nothing" rather than a parse error.
func SelectPath(out, path string) ([]any, error) {
	segs, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	var doc any
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("output is not JSON: %w (output: %s)", err, clip(out))
	}
	cur := []any{doc}
	for _, seg := range segs {
		if seg.name != "" {
			cur = step(cur, seg.name)
		}
		if seg.filter != nil {
			cur = filter(cur, *seg.filter)
		}
	}
	return cur, nil
}

type pathSegment struct {
	name   string
	filter *fieldFilter
}

type fieldFilter struct{ field, value string }

func parsePath(path string) ([]pathSegment, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("path is empty")
	}
	var raw []string
	depth, start := 0, 0
	for i, r := range path {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("path %q closes a bracket it did not open", path)
			}
		case '.':
			if depth == 0 {
				raw = append(raw, path[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("path %q leaves a bracket open", path)
	}
	raw = append(raw, path[start:])
	segs := make([]pathSegment, 0, len(raw))
	for _, r := range raw {
		seg, err := parseSegment(r)
		if err != nil {
			return nil, fmt.Errorf("path %q: %w", path, err)
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

func parseSegment(s string) (pathSegment, error) {
	if s == "" {
		return pathSegment{}, fmt.Errorf("empty segment")
	}
	open := strings.IndexByte(s, '[')
	if open < 0 {
		return pathSegment{name: s}, nil
	}
	if !strings.HasSuffix(s, "]") {
		return pathSegment{}, fmt.Errorf("segment %q has text after its filter", s)
	}
	field, value, ok := strings.Cut(s[open+1:len(s)-1], "=")
	if !ok || field == "" {
		return pathSegment{}, fmt.Errorf("filter in %q is not [field=value]", s)
	}
	return pathSegment{name: s[:open], filter: &fieldFilter{field: field, value: value}}, nil
}

func step(cur []any, seg string) []any {
	var next []any
	for _, v := range cur {
		switch node := v.(type) {
		case map[string]any:
			if seg == "*" {
				for _, k := range sortedKeys(node) {
					next = append(next, node[k])
				}
			} else if child, ok := node[seg]; ok {
				next = append(next, child)
			}
		case []any:
			if seg == "*" {
				next = append(next, node...)
			} else if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(node) {
				next = append(next, node[i])
			}
		}
	}
	return next
}

// filter keeps the objects whose field renders as the filter's value,
// looking through one level of array.
func filter(cur []any, f fieldFilter) []any {
	var next []any
	keep := func(v any) {
		if m, ok := v.(map[string]any); ok {
			if field, ok := m[f.field]; ok && scalarString(field) == f.value {
				next = append(next, m)
			}
		}
	}
	for _, v := range cur {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				keep(e)
			}
			continue
		}
		keep(v)
	}
	return next
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scalarString renders a selected value for comparison: a string as
// itself, a number in its JSON spelling, and anything else as compact
// JSON.
func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case nil:
		return ""
	case bool:
		return strconv.FormatBool(x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}
