// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// runComment emits a Markdown summary of a verdict file suitable
// for posting as a PR comment. The format is:
//
//	## lenny-test verdict: PASS
//	Run ID: …
//	Duration: …
//
//	| Tier | Status | Detail |
//	| --- | --- | --- |
//	| static | ✓ pass |  |
//	| unit   | ✓ pass |  |
//	...
//
// Usage:
//
//	lenny-test comment --verdict tests/results/latest.json
//	lenny-test comment --verdict latest.json --output comment.md
func runComment(args []string) int {
	fs := flag.NewFlagSet("comment", flag.ExitOnError)
	verdictPath := fs.String("verdict", "tests/results/latest.json", "path to the verdict JSON")
	out := fs.String("output", "", "write to this file (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	body, err := os.ReadFile(*verdictPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "comment: read %s: %v\n", *verdictPath, err)
		return 1
	}
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		fmt.Fprintf(os.Stderr, "comment: parse: %v\n", err)
		return 1
	}

	rendered := renderComment(v)
	if *out == "" {
		fmt.Print(rendered)
		return 0
	}
	if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "comment: write %s: %v\n", *out, err)
		return 1
	}
	fmt.Printf("comment: wrote %d bytes to %s\n", len(rendered), *out)
	return 0
}

func renderComment(v map[string]any) string {
	var sb strings.Builder

	verdict, _ := v["verdict"].(string)
	if verdict == "" {
		verdict = "?"
	}
	emoji := "✅"
	if verdict != "PASS" {
		emoji = "❌"
	}
	runID, _ := v["run_id"].(string)
	durMs := 0.0
	if d, ok := v["duration_ms"].(float64); ok {
		durMs = d
	}

	fmt.Fprintf(&sb, "## %s lenny-test verdict: %s\n\n", emoji, verdict)
	if runID != "" {
		fmt.Fprintf(&sb, "**Run ID:** `%s`  \n", runID)
	}
	fmt.Fprintf(&sb, "**Duration:** %.2fs\n\n", durMs/1000.0)

	tiers, _ := v["tiers"].(map[string]any)
	if len(tiers) == 0 {
		sb.WriteString("_No tier results recorded._\n")
		return sb.String()
	}
	// Stable tier ordering.
	preferred := []string{
		"static", "unit", "component", "contract", "integration",
		"e2e_kind", "e2e_cloud", "load", "chaos", "security",
		"conformance", "docs",
	}
	names := []string{}
	seen := map[string]bool{}
	for _, p := range preferred {
		if _, ok := tiers[p]; ok {
			names = append(names, p)
			seen[p] = true
		}
	}
	extras := []string{}
	for n := range tiers {
		if !seen[n] {
			extras = append(extras, n)
		}
	}
	sort.Strings(extras)
	names = append(names, extras...)

	sb.WriteString("| Tier | Status | Detail |\n")
	sb.WriteString("| --- | --- | --- |\n")
	for _, name := range names {
		t, _ := tiers[name].(map[string]any)
		status, _ := t["status"].(string)
		detail, _ := t["detail"].(string)
		marker := ""
		switch status {
		case "pass":
			marker = "✓ pass"
		case "fail":
			marker = "✗ fail"
		case "skipped":
			marker = "↷ skipped"
		default:
			marker = status
		}
		oneLineDetail := strings.ReplaceAll(detail, "\n", " ")
		if len(oneLineDetail) > 120 {
			oneLineDetail = oneLineDetail[:120] + "…"
		}
		// Escape pipe in detail to avoid breaking the markdown table.
		oneLineDetail = strings.ReplaceAll(oneLineDetail, "|", "\\|")
		fmt.Fprintf(&sb, "| `%s` | %s | %s |\n", name, marker, oneLineDetail)
	}

	if next, ok := v["next_action"].(string); ok && next != "" {
		fmt.Fprintf(&sb, "\n**Next:** %s\n", next)
	}
	return sb.String()
}
