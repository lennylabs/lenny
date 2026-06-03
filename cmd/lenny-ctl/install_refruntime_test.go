// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// spec: §17.6 lines 688-689 — the reference-runtime multi-select narrows
// the §26 catalog to a deployer-chosen subset, composed into
// referenceRuntimes.include. F-17.6.10.
func TestComposeValuesEmitsReferenceRuntimeInclude_spec_17_6_688(t *testing.T) {
	a := installAnswers{
		Release:           installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment:       "prod",
		Tier:              "tier2",
		Auth:              installAuth{Mode: "oidc", OIDCIssuer: "x", OIDCClientID: "y"},
		ReferenceRuntimes: []string{"claude-code", "chat"},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	rr, ok := v["referenceRuntimes"].(map[string]any)
	if !ok {
		t.Fatalf("referenceRuntimes block missing: %+v", v)
	}
	inc, ok := rr["include"].([]any)
	if !ok || len(inc) != 2 || inc[0] != "claude-code" || inc[1] != "chat" {
		t.Fatalf("referenceRuntimes.include = %+v, want [claude-code chat]", rr["include"])
	}
	if _, present := rr["enabled"]; present {
		t.Errorf("a narrowing selection must not set enabled: %+v", rr)
	}
}

// "none" disables the catalog entirely via referenceRuntimes.enabled=false.
func TestComposeValuesDisablesReferenceRuntimesForNone_spec_17_6_689(t *testing.T) {
	a := installAnswers{
		Release:           installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment:       "local",
		Tier:              "tier1",
		Auth:              installAuth{Mode: "dev"},
		ReferenceRuntimes: []string{"none"},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	rr, ok := v["referenceRuntimes"].(map[string]any)
	if !ok {
		t.Fatalf("referenceRuntimes block missing: %+v", v)
	}
	if rr["enabled"] != false {
		t.Fatalf("referenceRuntimes.enabled = %v, want false", rr["enabled"])
	}
	if _, present := rr["include"]; present {
		t.Errorf("none must not also set include: %+v", rr)
	}
}

// An empty selection leaves the chart default (the whole catalog), so the
// composed values omit the referenceRuntimes key entirely.
func TestComposeValuesOmitsReferenceRuntimesWhenAll_spec_17_6_688(t *testing.T) {
	a := installAnswers{
		Release:     installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment: "local",
		Tier:        "tier1",
		Auth:        installAuth{Mode: "dev"},
	}
	out, err := composeValues(a)
	if err != nil {
		t.Fatalf("composeValues: %v", err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(out, &v); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	if _, present := v["referenceRuntimes"]; present {
		t.Errorf("an empty selection must keep the chart default: %+v", v["referenceRuntimes"])
	}
}

func TestValidateAnswersRejectsUnknownReferenceRuntime_spec_17_6_688(t *testing.T) {
	a := installAnswers{
		Release:           installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment:       "local",
		Tier:              "tier1",
		Auth:              installAuth{Mode: "dev"},
		DevMode:           true,
		ReferenceRuntimes: []string{"claude-code", "not-a-runtime"},
	}
	errs := validateAnswers(a)
	if !strings.Contains(strings.Join(errs, " "), "not-a-runtime") {
		t.Fatalf("expected an unknown-runtime error, got %v", errs)
	}
}

func TestValidateAnswersRejectsNoneWithOthers_spec_17_6_688(t *testing.T) {
	a := installAnswers{
		Release:           installRelease{Name: "lenny", Namespace: "lenny-system"},
		Environment:       "local",
		Tier:              "tier1",
		Auth:              installAuth{Mode: "dev"},
		DevMode:           true,
		ReferenceRuntimes: []string{"none", "chat"},
	}
	errs := validateAnswers(a)
	if !strings.Contains(strings.Join(errs, " "), "'none' cannot be combined") {
		t.Fatalf("expected a none-combination error, got %v", errs)
	}
}

func TestParseReferenceRuntimeAnswer(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"none", []string{"none"}},
		{"NONE", []string{"none"}},
		{"claude-code, chat", []string{"claude-code", "chat"}},
		{"claude-code,,chat ,", []string{"claude-code", "chat"}},
	}
	for _, tc := range cases {
		got := parseReferenceRuntimeAnswer(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("parseReferenceRuntimeAnswer(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("parseReferenceRuntimeAnswer(%q) = %v, want %v", tc.in, got, tc.want)
			}
		}
	}
}
