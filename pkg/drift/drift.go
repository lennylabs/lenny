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

// Classify ranks a field path's drift severity per §25.10 line 3773:
// image, isolation profile, and security fields are high; scaling
// parameters and quota values are medium; labels, descriptions, and
// metadata are low. A field matching none of these defaults to medium.
//
// The classification is segment-aware rather than substring-only. A
// nested path like "pool.scaling.labelSelector" carries a structural
// "scaling" segment that matches medium first, so the inner "label"
// token does not pull the severity down to low. The low-severity
// keywords match only when the inner path token equals (or, for the
// "labels"/"annotations" maps, starts a sub-key under) one of the
// reserved metadata field names; a token like "labelSelector" or
// "labelExpression" stays at medium because it is structural config,
// not a metadata bag. F-25.10.11.
func Classify(path string) Severity {
	p := strings.ToLower(path)
	// §25.10 line 3773: image, isolation, and security fields drift at
	// high severity. The substring match is faithful: any path segment
	// that contains one of these keywords (e.g. "securityContext",
	// "isolationProfile") is structural-security and high.
	for _, kw := range []string{"image", "isolation", "security", "privileged", "capabilit"} {
		if strings.Contains(p, kw) {
			return SeverityHigh
		}
	}
	// §25.10 line 3773: scaling parameters and quota values drift at
	// medium severity. The keyword set covers the common §6.x / §17.x
	// names operators use; matching scaling/quota before the low-bucket
	// keywords keeps a path like "pool.scaling.labelSelector" at medium
	// (its "label" token is structural, not a metadata bag).
	for _, kw := range []string{
		"scaling", "quota", "replica", "warm", "size",
		"limit", "maxsurge", "maxunavailable",
	} {
		if strings.Contains(p, kw) {
			return SeverityMedium
		}
	}
	// §25.10 line 3773: labels, descriptions, annotations, and metadata
	// drift at low severity. The match is segment-anchored so the
	// keyword applies only to a metadata bag — a path segment that is
	// the literal keyword, or that starts a sub-key under one of the
	// reserved map names. A structural token like "labelSelector",
	// "annotationFilter", or "metadataValidator" stays at medium.
	for _, kw := range []string{"label", "description", "annotation", "metadata"} {
		if hasLowSegment(p, kw) {
			return SeverityLow
		}
	}
	return SeverityMedium
}

// hasLowSegment reports whether path carries kw as a whole segment, or
// as the root of a sub-key under one of the §25.10 metadata bags
// ("labels.team" — yes; "labelSelector.app" — no). F-25.10.11.
func hasLowSegment(path, kw string) bool {
	for _, seg := range strings.Split(path, ".") {
		// Bare segment: e.g. "description", "metadata", "labels".
		if seg == kw || seg == kw+"s" {
			return true
		}
	}
	return false
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
