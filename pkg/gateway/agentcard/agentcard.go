// SPDX-License-Identifier: MIT

// Package agentcard generates the §5.1 A2A agent card from a runtime's
// agentInterface descriptor. The gateway generates the card at runtime
// write time and stores it as a publishedMetadata entry under the key
// agent-card; the card is opaque pass-through on every read thereafter.
package agentcard

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// Key is the publishedMetadata key a generated card is stored under.
const Key = "agent-card"

// contentType is the media type of a generated card.
const contentType = "application/json"

// Card is the §5.1 A2A agent card generated from a runtime's
// agentInterface. The field names follow the A2A AgentCard convention.
// GeneratedAt and GeneratorVersion are the §5.1 envelope fields the
// generator injects; they are not drawn from agentInterface.
type Card struct {
	Name               string       `json:"name"`
	Description        string       `json:"description,omitempty"`
	DefaultInputModes  []string     `json:"defaultInputModes,omitempty"`
	DefaultOutputModes []string     `json:"defaultOutputModes,omitempty"`
	Capabilities       Capabilities `json:"capabilities"`
	Skills             []Skill      `json:"skills,omitempty"`
	Examples           []Example    `json:"examples,omitempty"`
	GeneratedAt        string       `json:"generatedAt"`
	GeneratorVersion   string       `json:"generatorVersion"`
}

// Capabilities is the A2A card capabilities block. The v1 A2A path has
// elicitation disabled (§21.1), so a generated card advertises false.
type Capabilities struct {
	Elicitation bool `json:"elicitation"`
}

// Skill is one entry in Card.Skills, mapped from an agentInterface skill.
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Example is one entry in Card.Examples, mapped from an agentInterface
// example.
type Example struct {
	Description string `json:"description,omitempty"`
	Input       string `json:"input,omitempty"`
}

// Generate builds the §5.1 A2A agent card for a runtime. generatedAt is
// recorded as the card's generation instant and generatorVersion as the
// gateway semantic version; both are the §5.1 envelope fields. The
// agentInterface input/output modes map to the card's default mode
// lists, and its skills and examples map onto the card verbatim.
func Generate(name string, iface runtimestore.AgentInterface, generatedAt time.Time, generatorVersion string) Card {
	card := Card{
		Name:             name,
		Description:      iface.Description,
		GeneratedAt:      generatedAt.UTC().Format(time.RFC3339),
		GeneratorVersion: generatorVersion,
	}
	for _, m := range iface.InputModes {
		card.DefaultInputModes = append(card.DefaultInputModes, m.Type)
	}
	for _, m := range iface.OutputModes {
		card.DefaultOutputModes = append(card.DefaultOutputModes, m.Type)
	}
	for _, s := range iface.Skills {
		card.Skills = append(card.Skills, Skill{ID: s.ID, Name: s.Name, Description: s.Description})
	}
	for _, e := range iface.Examples {
		card.Examples = append(card.Examples, Example{Description: e.Description, Input: e.Input})
	}
	return card
}

// NeedsRegen reports whether a card stamped storedVersion should be
// regenerated under the §5.1 regenerate-cards threshold. An empty
// threshold regenerates unconditionally. Otherwise the card is
// regenerated when storedVersion is strictly older than the threshold;
// a stored version that is empty or unparseable sorts below any real
// version and so is regenerated.
func NeedsRegen(storedVersion, threshold string) bool {
	if threshold == "" {
		return true
	}
	return compareVersions(storedVersion, threshold) < 0
}

// parseVersion parses a MAJOR.MINOR.PATCH version into its numeric
// components, tolerating a leading "v" and a pre-release or build
// suffix. Missing trailing components default to zero. ok is false when
// a component is non-numeric or the version is empty.
func parseVersion(v string) (parts [3]int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return parts, false
	}
	for i, seg := range strings.SplitN(v, ".", 3) {
		n, err := strconv.Atoi(seg)
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

// compareVersions orders two version strings, returning -1, 0, or 1. An
// unparseable version sorts below any parseable one; two unparseable
// versions compare equal.
func compareVersions(a, b string) int {
	pa, aok := parseVersion(a)
	pb, bok := parseVersion(b)
	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return -1
	case !bok:
		return 1
	}
	for i := range pa {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

// Entry generates the §5.1 A2A agent card and returns it as a
// publishedMetadata entry under the key agent-card with public
// visibility, ready to merge into a runtime's PublishedMetadata list.
func Entry(name string, iface runtimestore.AgentInterface, generatedAt time.Time, generatorVersion string) runtimestore.PublishedMetadataEntry {
	card := Generate(name, iface, generatedAt, generatorVersion)
	// A Card holds only strings, bools, and slices of the same, so the
	// encoding cannot fail.
	body, _ := json.Marshal(card)
	return runtimestore.PublishedMetadataEntry{
		Key:         Key,
		ContentType: contentType,
		Visibility:  runtimestore.VisibilityPublic,
		Content:     string(body),
	}
}
