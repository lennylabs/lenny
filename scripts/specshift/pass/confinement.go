// SPDX-License-Identifier: MIT

package pass

import "strings"

// Confinement is the set of tracked paths one run of a pass may write. It
// narrows the walk and never widens it: every exclusion the write domain
// already computes still holds, and the confinement removes further paths
// from what the domain returned.
//
// A run is confined because one invocation of a pass may have to stay
// inside a commit scope narrower than the pass's whole write domain, so
// the same pass, the same register, and the same fail-closed contract run
// once per part of a partition of that domain rather than once over the
// whole of it.
//
// spec: §28.1 (N3 and N4, the naming law the passes carrying this
// confinement apply across the tree)
type Confinement struct {
	// only admits a tracked path. When it is empty every path is
	// admitted, so a confinement stating exclusions alone covers
	// everything the exclusions do not name.
	only []string
	// except excludes a tracked path the admissions let through.
	except []string
}

// NewConfinement returns the confinement two flag value sets state, or
// nil when both are empty. A nil confinement covers the whole write
// domain, which is what the domain measurement prints and what a caller
// driving a pass directly gets when it sets no confinement.
func NewConfinement(only, except []string) *Confinement {
	if len(only) == 0 && len(except) == 0 {
		return nil
	}
	return &Confinement{
		only:   append([]string(nil), only...),
		except: append([]string(nil), except...),
	}
}

// Covers reports whether the run writes a tracked path. A path is covered
// when some admission matches it, or none was given, and no exclusion
// matches it.
func (c *Confinement) Covers(path string) bool {
	if c == nil {
		return true
	}
	if len(c.only) > 0 && !matchesAny(path, c.only) {
		return false
	}
	return !matchesAny(path, c.except)
}

// Filter returns the members of a domain the confinement covers, in the
// order the domain gave them.
func (c *Confinement) Filter(domain []string) []string {
	if c == nil {
		return domain
	}
	confined := make([]string, 0, len(domain))
	for _, target := range domain {
		if c.Covers(target) {
			confined = append(confined, target)
		}
	}
	return confined
}

// String renders the confinement for an operator-facing message, so a
// run names what it ran under rather than leaving it to be inferred from
// the command line.
func (c *Confinement) String() string {
	if c == nil {
		return "the whole write domain"
	}
	parts := make([]string, 0, len(c.only)+len(c.except))
	for _, v := range c.only {
		parts = append(parts, "-only "+v)
	}
	for _, v := range c.except {
		parts = append(parts, "-except "+v)
	}
	return strings.Join(parts, " ")
}

// matchesAny reports whether a tracked path matches some confinement
// value.
func matchesAny(path string, values []string) bool {
	for _, v := range values {
		if matches(path, v) {
			return true
		}
	}
	return false
}

// matches reports whether a tracked path equals a confinement value or
// sits under it as a directory. The prefix is terminated by the path
// separator, so the match is by whole path segment: a value naming a
// directory covers no sibling directory whose name merely begins with the
// same characters, and a value naming a file covers no saved copy beside
// it. Matching by character prefix would let a run confined to one
// subtree write outside it.
func matches(path, value string) bool {
	dir := strings.TrimSuffix(value, "/")
	if dir == "" {
		return false
	}
	return path == dir || strings.HasPrefix(path, dir+"/")
}
