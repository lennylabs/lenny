// SPDX-License-Identifier: MIT

// Package drift implements the §25.10 configuration drift detection
// comparison: a field-by-field JSON diff between the desired platform
// state and the actual running state, with each drifted field
// classified by severity. The package is pure — no Postgres or
// gateway I/O — so the lenny-ops drift endpoints and their tests share
// one implementation.
package drift

import (
	"reflect"
	"sort"
	"strings"
)

// Kind classifies how a field drifted (§25.10).
type Kind string

const (
	// Added is a field present in the actual state but not the desired.
	Added Kind = "added"
	// Removed is a field present in the desired state but not the actual.
	Removed Kind = "removed"
	// Modified is a field present in both with differing values.
	Modified Kind = "modified"
)

// Severity ranks a drifted field's operational impact (§25.10).
type Severity string

const (
	// SeverityHigh covers image, isolation profile, and security fields.
	SeverityHigh Severity = "high"
	// SeverityMedium covers scaling parameters and quota values.
	SeverityMedium Severity = "medium"
	// SeverityLow covers labels, descriptions, and metadata.
	SeverityLow Severity = "low"
)

// Change is one drifted field.
type Change struct {
	Path     string
	Kind     Kind
	Desired  any
	Actual   any
	Severity Severity
}

// Diff computes the §25.10 field-by-field drift between the desired
// and actual state. Nested objects are compared recursively; a
// Change's Path is the dotted path to the field. The result is ordered
// by path so the report is deterministic.
func Diff(desired, actual map[string]any) []Change {
	var changes []Change
	diffMaps("", desired, actual, &changes)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func diffMaps(prefix string, desired, actual map[string]any, out *[]Change) {
	for k, dv := range desired {
		path := joinPath(prefix, k)
		av, present := actual[k]
		if !present {
			*out = append(*out, newChange(path, Removed, dv, nil))
			continue
		}
		dm, dIsMap := dv.(map[string]any)
		am, aIsMap := av.(map[string]any)
		if dIsMap && aIsMap {
			diffMaps(path, dm, am, out)
			continue
		}
		if !reflect.DeepEqual(dv, av) {
			*out = append(*out, newChange(path, Modified, dv, av))
		}
	}
	for k, av := range actual {
		if _, present := desired[k]; !present {
			*out = append(*out, newChange(joinPath(prefix, k), Added, nil, av))
		}
	}
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func newChange(path string, kind Kind, desired, actual any) Change {
	return Change{Path: path, Kind: kind, Desired: desired, Actual: actual, Severity: Classify(path)}
}

// Classify ranks a field path's drift severity per §25.10: image,
// isolation profile, and security fields are high; scaling parameters
// and quota values are medium; labels, descriptions, and metadata are
// low. A field matching none of these defaults to medium.
func Classify(path string) Severity {
	p := strings.ToLower(path)
	for _, kw := range []string{"image", "isolation", "security", "privileged", "capabilit"} {
		if strings.Contains(p, kw) {
			return SeverityHigh
		}
	}
	for _, kw := range []string{"label", "description", "annotation", "metadata"} {
		if strings.Contains(p, kw) {
			return SeverityLow
		}
	}
	return SeverityMedium
}

// SnapshotStale reports whether a desired-state snapshot of the given
// age in seconds is older than the §25.10 staleness threshold in days.
// A threshold of zero or less disables the warning.
func SnapshotStale(ageSeconds, thresholdDays int) bool {
	if thresholdDays <= 0 {
		return false
	}
	return ageSeconds > thresholdDays*86400
}
