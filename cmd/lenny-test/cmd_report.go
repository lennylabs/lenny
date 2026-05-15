// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// runReport aggregates verdict JSON files from a directory into a
// single roll-up. Useful for nightly / weekly workflows that emit
// one verdict per matrix job.
//
// Usage:
//
//	lenny-test report --dir tests/results          # human summary
//	lenny-test report --dir tests/results --output markdown
//	lenny-test report --dir tests/results --output json
func runReport(args []string) int {
	fs_ := flag.NewFlagSet("report", flag.ExitOnError)
	dir := fs_.String("dir", resultsDir, "directory containing verdict JSON files")
	output := fs_.String("output", "human", "human | markdown | json")
	if err := fs_.Parse(args); err != nil {
		return 2
	}

	verdicts, err := loadVerdicts(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "report: %v\n", err)
		return 1
	}
	if len(verdicts) == 0 {
		fmt.Fprintf(os.Stderr, "report: no verdict files in %s\n", *dir)
		return 1
	}

	roll := rollup(verdicts)

	switch *output {
	case "json":
		b, _ := json.MarshalIndent(roll, "", "  ")
		fmt.Println(string(b))
	case "markdown":
		fmt.Print(renderRollupMarkdown(roll))
	default:
		fmt.Print(renderRollupHuman(roll))
	}
	if roll.OverallStatus != "PASS" {
		return 1
	}
	return 0
}

type verdictDoc struct {
	Version  int    `json:"version"`
	RunID    string `json:"run_id"`
	Verdict  string `json:"verdict"`
	Duration int64  `json:"duration_ms"`
	Tiers    map[string]struct {
		Status string `json:"status"`
		Detail string `json:"detail"`
	} `json:"tiers"`
	Source string `json:"-"` // populated by loader; the file name
}

// supportedVerdictVersion is the §7 schema version this harness was
// built to consume. The report subcommand warns when a verdict
// carries a version higher than this so silently-incompatible
// aggregation doesn't go unnoticed when v2 ships.
const supportedVerdictVersion = 1

type rolledUp struct {
	OverallStatus string                `json:"verdict"`
	GeneratedAt   string                `json:"generated_at"`
	TotalVerdicts int                   `json:"total_verdicts"`
	PerTier       map[string]tierRollup `json:"per_tier"`
	PerVerdict    []verdictRow          `json:"per_verdict"`
}

type tierRollup struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Skipped int `json:"skipped"`
}

type verdictRow struct {
	Source  string `json:"source"`
	RunID   string `json:"run_id"`
	Verdict string `json:"verdict"`
}

func loadVerdicts(dir string) ([]verdictDoc, error) {
	root := dir
	if !filepath.IsAbs(dir) {
		root = filepath.Join(repoRoot(), dir)
	}
	out := []verdictDoc{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		// Skip files that don't look like verdicts (e.g.
		// cover.out.json from go-coverage tooling) by sniffing
		// the top-level keys.
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var probe map[string]any
		if err := json.Unmarshal(body, &probe); err != nil {
			return nil
		}
		if _, ok := probe["tiers"]; !ok {
			return nil
		}
		var v verdictDoc
		if err := json.Unmarshal(body, &v); err != nil {
			return nil
		}
		if v.Version > supportedVerdictVersion {
			fmt.Fprintf(os.Stderr,
				"report: %s carries verdict version %d but this harness supports up to %d; aggregation may misread newer fields\n",
				path, v.Version, supportedVerdictVersion)
		}
		v.Source = filepath.Base(path)
		out = append(out, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func rollup(verdicts []verdictDoc) rolledUp {
	r := rolledUp{
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		TotalVerdicts: len(verdicts),
		PerTier:       map[string]tierRollup{},
	}
	overall := "PASS"
	for _, v := range verdicts {
		row := verdictRow{Source: v.Source, RunID: v.RunID, Verdict: v.Verdict}
		r.PerVerdict = append(r.PerVerdict, row)
		if v.Verdict != "PASS" {
			overall = "FAIL"
		}
		for name, t := range v.Tiers {
			cur := r.PerTier[name]
			switch t.Status {
			case "pass":
				cur.Pass++
			case "fail":
				cur.Fail++
			case "skipped":
				cur.Skipped++
			}
			r.PerTier[name] = cur
		}
	}
	r.OverallStatus = overall
	return r
}

func renderRollupHuman(r rolledUp) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "lenny-test report: %s\n", r.OverallStatus)
	fmt.Fprintf(&sb, "  verdicts:    %d\n", r.TotalVerdicts)
	fmt.Fprintf(&sb, "  generated:   %s\n", r.GeneratedAt)
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "per tier:")
	names := orderedTierNamesByKey(r.PerTier)
	for _, n := range names {
		t := r.PerTier[n]
		fmt.Fprintf(&sb, "  %-13s  pass=%d  fail=%d  skipped=%d\n", n, t.Pass, t.Fail, t.Skipped)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "per verdict file:")
	for _, v := range r.PerVerdict {
		marker := "✓"
		if v.Verdict != "PASS" {
			marker = "✗"
		}
		fmt.Fprintf(&sb, "  %s %s  %s\n", marker, v.Source, v.RunID)
	}
	return sb.String()
}

func renderRollupMarkdown(r rolledUp) string {
	var sb strings.Builder
	emoji := "✅"
	if r.OverallStatus != "PASS" {
		emoji = "❌"
	}
	fmt.Fprintf(&sb, "## %s lenny-test report: %s\n\n", emoji, r.OverallStatus)
	fmt.Fprintf(&sb, "Verdicts: %d  \n", r.TotalVerdicts)
	fmt.Fprintf(&sb, "Generated: %s\n\n", r.GeneratedAt)
	fmt.Fprintln(&sb, "| Tier | Pass | Fail | Skipped |")
	fmt.Fprintln(&sb, "| --- | ---: | ---: | ---: |")
	for _, n := range orderedTierNamesByKey(r.PerTier) {
		t := r.PerTier[n]
		fmt.Fprintf(&sb, "| `%s` | %d | %d | %d |\n", n, t.Pass, t.Fail, t.Skipped)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "| Source | Run ID | Verdict |")
	fmt.Fprintln(&sb, "| --- | --- | --- |")
	for _, v := range r.PerVerdict {
		fmt.Fprintf(&sb, "| %s | `%s` | %s |\n", v.Source, v.RunID, v.Verdict)
	}
	return sb.String()
}

func orderedTierNamesByKey(m map[string]tierRollup) []string {
	preferred := allTiers()
	out := []string{}
	seen := map[string]bool{}
	for _, p := range preferred {
		if _, ok := m[p]; ok {
			out = append(out, p)
			seen[p] = true
		}
	}
	extras := []string{}
	for n := range m {
		if !seen[n] {
			extras = append(extras, n)
		}
	}
	sort.Strings(extras)
	return append(out, extras...)
}
