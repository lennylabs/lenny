// SPDX-License-Identifier: MIT

package line

import (
	"fmt"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
)

// anchorFor returns the anchor citation that replaces the citation,
// which is the section it names together with the qualifier it carries.
// The qualifier is carried through because it names the sub-element the
// citation points at, and dropping it would send the reader to the
// section alone.
func anchorFor(sections *citation.Resolver, c citation.Citation) (string, error) {
	number, err := anchorNumber(sections, c)
	if err != nil {
		return "", err
	}
	anchor := "§" + number
	if c.Qualifier != "" {
		anchor += " " + c.Qualifier
	}
	return anchor, nil
}

// anchorNumber returns the section number the citation converts to.
//
// The section-number spelling names its own section, so it converts
// whether or not its line numbers still fall inside that section: a
// member that has drifted out of the section is the stale pointer the
// migration retires, and the anchor is what stops it drifting again. Two
// properties of the citation do block the conversion. A range whose
// endpoints straddle a section boundary means two sections and picking
// either is a guess. A citation whose head opened a parenthesis nothing
// closes has a span that cannot be replaced without stranding the
// carrier's closing parenthesis.
//
// The path spelling names no section, so the section that contains the
// cited line is what the anchor is inferred from and every failure the
// resolver reports blocks the conversion.
func anchorNumber(sections *citation.Resolver, c citation.Citation) (string, error) {
	if c.Unbalanced {
		return "", fmt.Errorf("the citation opens a parenthesis nothing closes, so converting it would strand the closing parenthesis")
	}
	if c.Section != "" {
		return sectionAnchor(sections, c)
	}
	return pathAnchor(sections, c)
}

// sectionAnchor returns the section the section-number spelling names,
// and fails on a straddling range.
func sectionAnchor(sections *citation.Resolver, c citation.Citation) (string, error) {
	for _, f := range sections.Resolve(c) {
		if f.Kind != citation.StraddlingRange {
			continue
		}
		return "", fmt.Errorf("the range %s straddles a section boundary, so no single anchor names what it cites: %s",
			f.Member.Text, f.Detail)
	}
	return c.Section, nil
}

// pathAnchor infers the section a path-form citation means from the
// lines it names. Every member has to land in the same section: members
// that land in different sections name no single anchor, the same way a
// straddling range does.
func pathAnchor(sections *citation.Resolver, c citation.Citation) (string, error) {
	if failures := sections.Resolve(c); len(failures) > 0 {
		return "", fmt.Errorf("the path-form citation does not resolve, so no anchor can be inferred: %s", failures[0])
	}
	anchor := ""
	for _, m := range c.Members {
		section, ok := sections.Containing(c.File, m.Start)
		if !ok {
			return "", fmt.Errorf("line %d falls in no section of %s", m.Start, c.File)
		}
		if anchor == "" {
			anchor = section.Number
			continue
		}
		if section.Number != anchor {
			return "", fmt.Errorf("the members name both §%s and §%s of %s, so no single anchor names what they cite",
				anchor, section.Number, c.File)
		}
	}
	if anchor == "" {
		return "", fmt.Errorf("the citation names %s and no line inside it", c.File)
	}
	return anchor, nil
}
