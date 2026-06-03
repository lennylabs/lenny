// SPDX-License-Identifier: MIT

package compliance

import (
	_ "embed"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// referenceCatalogYAML is the §26 reference-runtime catalog manifest
// embedded at build time. The manifest enumerates every first-party
// reference runtime the spec ships; the nightly conformance run
// reads it to know which images to pull and which integration level
// to drive each runtime at.
//
//go:embed reference_catalog.yaml
var referenceCatalogYAML []byte

// CategoryCodingAgent is the §26.1 catalog category for the coding-agent
// reference runtimes (claude-code, gemini-cli, codex, cursor-cli). The
// §26.2 line 38 isolation rule keys off this category.
const CategoryCodingAgent = "coding-agent"

// ReferenceRuntime is one row in the §26 catalog. Image is the OCI
// reference relative to the configured registry; Level is the §15.4
// integration level the runtime declares.
type ReferenceRuntime struct {
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
	Level    Level  `yaml:"level"`
	Image    string `yaml:"image"`
	Notes    string `yaml:"notes"`
}

// referenceCatalogDoc is the YAML envelope.
type referenceCatalogDoc struct {
	Version  int                `yaml:"version"`
	Runtimes []ReferenceRuntime `yaml:"runtimes"`
}

// ReferenceCatalog returns the parsed §26 reference-runtime catalog.
// The embedded manifest is the canonical record; the test surface
// asserts every spec-listed runtime is present and well-formed.
func ReferenceCatalog() ([]ReferenceRuntime, error) {
	var doc referenceCatalogDoc
	if err := yaml.Unmarshal(referenceCatalogYAML, &doc); err != nil {
		return nil, fmt.Errorf("compliance: parse reference catalog: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("compliance: reference catalog version = %d, want 1", doc.Version)
	}
	if len(doc.Runtimes) == 0 {
		return nil, errors.New("compliance: reference catalog is empty")
	}
	for i, r := range doc.Runtimes {
		if r.Name == "" {
			return nil, fmt.Errorf("compliance: reference runtime %d has no name", i)
		}
		if !r.Level.IsValid() {
			return nil, fmt.Errorf("compliance: reference runtime %q has invalid level %q", r.Name, r.Level)
		}
		if r.Image == "" {
			return nil, fmt.Errorf("compliance: reference runtime %q has no image reference", r.Name)
		}
	}
	return doc.Runtimes, nil
}
