// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestRenderCommentPassNoTiers(t *testing.T) {
	got := renderComment(map[string]any{
		"verdict":     "PASS",
		"run_id":      "abc123",
		"duration_ms": 12500.0,
	})
	if !strings.Contains(got, "lenny-test verdict: PASS") {
		t.Errorf("missing verdict heading: %q", got)
	}
	if !strings.Contains(got, "abc123") {
		t.Errorf("missing run id: %q", got)
	}
	if !strings.Contains(got, "12.50s") {
		t.Errorf("duration not formatted: %q", got)
	}
	if !strings.Contains(got, "_No tier results recorded._") {
		t.Errorf("no-tiers branch should print the fallback: %q", got)
	}
}

func TestRenderCommentEmojiOnPass(t *testing.T) {
	got := renderComment(map[string]any{"verdict": "PASS"})
	if !strings.Contains(got, "✅") {
		t.Errorf("PASS should render checkmark: %q", got)
	}
	if strings.Contains(got, "❌") {
		t.Errorf("PASS should not render fail emoji: %q", got)
	}
}

func TestRenderCommentEmojiOnFail(t *testing.T) {
	got := renderComment(map[string]any{"verdict": "FAIL"})
	if !strings.Contains(got, "❌") {
		t.Errorf("FAIL should render cross: %q", got)
	}
}

func TestRenderCommentEmojiOnInconclusive(t *testing.T) {
	got := renderComment(map[string]any{"verdict": "INCONCLUSIVE"})
	// Inconclusive is not PASS, so the renderer falls through to ❌.
	// The audit/PR pipeline can colorize differently downstream.
	if !strings.Contains(got, "❌") {
		t.Errorf("INCONCLUSIVE renders fail emoji today: %q", got)
	}
}

func TestRenderCommentTierTableOrdered(t *testing.T) {
	got := renderComment(map[string]any{
		"verdict": "FAIL",
		"tiers": map[string]any{
			"docs":   map[string]any{"status": "skipped", "detail": "no docs yet"},
			"static": map[string]any{"status": "pass"},
			"unit":   map[string]any{"status": "fail", "detail": "TestX failed"},
		},
	})
	staticIdx := strings.Index(got, "`static`")
	unitIdx := strings.Index(got, "`unit`")
	docsIdx := strings.Index(got, "`docs`")
	if staticIdx < 0 || unitIdx < 0 || docsIdx < 0 {
		t.Fatalf("missing one of static/unit/docs: %q", got)
	}
	if !(staticIdx < unitIdx && unitIdx < docsIdx) {
		t.Fatalf("canonical tier order broken: static@%d unit@%d docs@%d", staticIdx, unitIdx, docsIdx)
	}
	if !strings.Contains(got, "✓ pass") {
		t.Errorf("pass marker missing: %q", got)
	}
	if !strings.Contains(got, "✗ fail") {
		t.Errorf("fail marker missing: %q", got)
	}
	if !strings.Contains(got, "↷ skipped") {
		t.Errorf("skipped marker missing: %q", got)
	}
}

func TestRenderCommentTruncatesLongDetail(t *testing.T) {
	long := strings.Repeat("x", 300)
	got := renderComment(map[string]any{
		"verdict": "FAIL",
		"tiers": map[string]any{
			"unit": map[string]any{"status": "fail", "detail": long},
		},
	})
	// 120-char truncation + ellipsis. Should NOT contain the full 300-char string.
	if strings.Contains(got, long) {
		t.Errorf("detail not truncated; got full %d-char string", len(long))
	}
	if !strings.Contains(got, "…") {
		t.Errorf("missing ellipsis marker: %q", got)
	}
}

func TestRenderCommentEscapesPipe(t *testing.T) {
	got := renderComment(map[string]any{
		"verdict": "FAIL",
		"tiers": map[string]any{
			"unit": map[string]any{"status": "fail", "detail": "value | with | pipe"},
		},
	})
	// Pipes inside the detail cell are escaped so the markdown table
	// renders correctly.
	if !strings.Contains(got, `\|`) {
		t.Errorf("pipe not escaped: %q", got)
	}
}

func TestRenderCommentFlattensNewlinesInDetail(t *testing.T) {
	got := renderComment(map[string]any{
		"verdict": "FAIL",
		"tiers": map[string]any{
			"unit": map[string]any{"status": "fail", "detail": "first\nsecond\nthird"},
		},
	})
	// Detail line must be flattened so the markdown table stays
	// single-row per tier.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "| `unit`") {
			if strings.Count(line, "\n") != 0 {
				t.Errorf("tier row contains a newline: %q", line)
			}
			if !strings.Contains(line, "first second third") {
				t.Errorf("multi-line detail not flattened: %q", line)
			}
		}
	}
}

func TestRenderCommentIncludesNextAction(t *testing.T) {
	got := renderComment(map[string]any{
		"verdict":     "FAIL",
		"next_action": "Fix unit-tier failures.",
		"tiers":       map[string]any{"unit": map[string]any{"status": "fail"}},
	})
	if !strings.Contains(got, "**Next:**") {
		t.Errorf("missing Next line: %q", got)
	}
	if !strings.Contains(got, "Fix unit-tier failures.") {
		t.Errorf("next_action text dropped: %q", got)
	}
}

func TestRenderCommentOmitsNextActionWhenEmpty(t *testing.T) {
	got := renderComment(map[string]any{
		"verdict": "PASS",
		"tiers":   map[string]any{"unit": map[string]any{"status": "pass"}},
	})
	if strings.Contains(got, "**Next:**") {
		t.Errorf("Next line emitted on PASS: %q", got)
	}
}

func TestRenderCommentExtraTiersAppendedAfterCanonical(t *testing.T) {
	got := renderComment(map[string]any{
		"verdict": "PASS",
		"tiers": map[string]any{
			"unit":          map[string]any{"status": "pass"},
			"custom_tier_z": map[string]any{"status": "pass"},
			"custom_tier_a": map[string]any{"status": "pass"},
		},
	})
	unitIdx := strings.Index(got, "`unit`")
	aIdx := strings.Index(got, "`custom_tier_a`")
	zIdx := strings.Index(got, "`custom_tier_z`")
	if !(unitIdx < aIdx && aIdx < zIdx) {
		t.Fatalf("extras must follow canonical tiers in sorted order; got unit@%d a@%d z@%d", unitIdx, aIdx, zIdx)
	}
}

func TestRenderCommentMissingVerdictRendersQuestion(t *testing.T) {
	got := renderComment(map[string]any{})
	if !strings.Contains(got, "verdict: ?") {
		t.Errorf("missing verdict should render '?': %q", got)
	}
}
