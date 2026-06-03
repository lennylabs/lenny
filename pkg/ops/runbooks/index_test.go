// SPDX-License-Identifier: MIT

package runbooks_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/runbooks"
)

const sample = `---
title: "Warm Pool Exhaustion"
triggers:
  - alert: WarmPoolLow
    severity: warning
  - alert: WarmPoolExhausted
    severity: critical
components:
  - warmPools
symptoms:
  - "session creation returns RUNTIME_UNAVAILABLE"
  - "idle pod count drops to zero"
tags:
  - scaling
  - capacity
requires:
  - admin-api
related:
  - docs/runbooks/gateway-replica-failure.md
---

# Warm Pool Exhaustion

Trigger → Diagnosis → Remediation.
`

func TestParseFrontMatter(t *testing.T) {
	fm, err := runbooks.Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(fm.Triggers) != 2 {
		t.Fatalf("got %d triggers, want 2", len(fm.Triggers))
	}
	if fm.Triggers[1].Alert != "WarmPoolExhausted" || fm.Triggers[1].Severity != "critical" {
		t.Errorf("trigger[1] = %+v, want WarmPoolExhausted/critical", fm.Triggers[1])
	}
	if len(fm.Components) != 1 || fm.Components[0] != "warmPools" {
		t.Errorf("components = %v, want [warmPools]", fm.Components)
	}
	if len(fm.Tags) != 2 {
		t.Errorf("tags = %v, want 2", fm.Tags)
	}
	// spec: §25.7 line 3143 — title is parsed so the `q` filter can
	// search it.
	if fm.Title != "Warm Pool Exhaustion" {
		t.Errorf("title = %q, want %q", fm.Title, "Warm Pool Exhaustion")
	}
}

func TestParseRejectsMissingFrontMatter(t *testing.T) {
	if _, err := runbooks.Parse([]byte("# Just a heading\n\nNo front matter.\n")); err == nil {
		t.Error("Parse of a document without front matter = nil error, want an error")
	}
}

func TestParseRejectsUnterminatedFrontMatter(t *testing.T) {
	if _, err := runbooks.Parse([]byte("---\ntriggers:\n  - alert: X\n")); err == nil {
		t.Error("Parse of unterminated front matter = nil error, want an error")
	}
}

func TestMatches(t *testing.T) {
	fm, err := runbooks.Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cases := []struct {
		name   string
		filter runbooks.Filter
		want   bool
	}{
		{"empty filter matches", runbooks.Filter{}, true},
		{"matching alert", runbooks.Filter{Alert: "WarmPoolExhausted"}, true},
		{"non-matching alert", runbooks.Filter{Alert: "PostgresDown"}, false},
		{"matching component", runbooks.Filter{Component: "warmPools"}, true},
		{"non-matching component", runbooks.Filter{Component: "redis"}, false},
		{"matching tag", runbooks.Filter{Tag: "capacity"}, true},
		// spec: §25.7 line 3142 — `requires` filters to executable runbooks.
		{"matching requires", runbooks.Filter{Requires: "admin-api"}, true},
		{"non-matching requires", runbooks.Filter{Requires: "cluster-access"}, false},
		// spec: §25.7 line 3143 — `q` full-text over symptoms, tags, title.
		{"q matches symptom substring", runbooks.Filter{Query: "RUNTIME_UNAVAILABLE"}, true},
		{"q matches symptom case-insensitive", runbooks.Filter{Query: "idle pod count"}, true},
		{"q matches tag", runbooks.Filter{Query: "scaling"}, true},
		{"q matches title", runbooks.Filter{Query: "warm pool"}, true},
		{"q all terms must match", runbooks.Filter{Query: "idle disk"}, false},
		{"q no match", runbooks.Filter{Query: "kafka"}, false},
		{"all set and matching", runbooks.Filter{Alert: "WarmPoolLow", Component: "warmPools", Tag: "scaling"}, true},
		{"one of several not matching", runbooks.Filter{Alert: "WarmPoolLow", Tag: "credentials"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runbooks.Matches(fm, c.filter); got != c.want {
				t.Errorf("Matches(%+v) = %v, want %v", c.filter, got, c.want)
			}
		})
	}
}
