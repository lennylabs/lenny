// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
)

// runBaseline diffs two baseline JSON documents and reports
// per-percentile deltas. Used by CI to enforce the §22.5 15%
// regression threshold across baseline runs.
//
// Usage:
//
//	lenny-test baseline diff --before old.json --after new.json
//	lenny-test baseline diff --before old.json --after new.json --threshold 0.15
//	lenny-test baseline diff ... --json
func runBaseline(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lenny-test baseline <diff> [flags]")
		return 2
	}
	switch args[0] {
	case "diff":
		return runBaselineDiff(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "lenny-test: unknown baseline subcommand %q\n", args[0])
		return 2
	}
}

func runBaselineDiff(args []string) int {
	fs := flag.NewFlagSet("baseline diff", flag.ExitOnError)
	before := fs.String("before", "", "path to the prior baseline JSON")
	after := fs.String("after", "", "path to the new baseline JSON")
	threshold := fs.Float64("threshold", 0.15, "fractional regression budget per percentile (default 0.15 = 15%)")
	jsonOut := fs.Bool("json", false, "emit JSON diff instead of human format")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *before == "" || *after == "" {
		fmt.Fprintln(os.Stderr, "baseline diff: --before and --after are required")
		return 2
	}
	a, err := loadBaseline(*before)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baseline diff: load before: %v\n", err)
		return 1
	}
	b, err := loadBaseline(*after)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baseline diff: load after: %v\n", err)
		return 1
	}
	diff := computeBaselineDiff(a, b)
	regressions := 0
	for _, e := range diff {
		if e.Delta > *threshold {
			regressions++
		}
	}
	if *jsonOut {
		out, _ := json.MarshalIndent(map[string]any{
			"before":      *before,
			"after":       *after,
			"threshold":   *threshold,
			"entries":     diff,
			"regressions": regressions,
		}, "", "  ")
		fmt.Println(string(out))
	} else {
		fmt.Printf("baseline diff: %s → %s (threshold %.1f%%)\n", *before, *after, *threshold*100)
		for _, e := range diff {
			marker := "  "
			if e.Delta > *threshold {
				marker = "✗ "
			} else if e.Delta < 0 {
				marker = "↓ "
			}
			fmt.Printf("%s%-8s %.2fms → %.2fms (%+.1f%%)\n", marker, e.Key, e.Before, e.After, e.Delta*100)
		}
		fmt.Printf("\n%d regression(s) beyond threshold\n", regressions)
	}
	if regressions > 0 {
		return 1
	}
	return 0
}

type baselineEntry struct {
	Key    string  `json:"key"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	Delta  float64 `json:"delta"`
}

func loadBaseline(path string) (map[string]float64, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// The expected schema has a metric_ms map; we accept either
	// that or a flat top-level map[string]number for flexibility.
	var doc struct {
		MetricMS map[string]float64 `json:"metric_ms"`
	}
	if err := json.Unmarshal(body, &doc); err == nil && len(doc.MetricMS) > 0 {
		return doc.MetricMS, nil
	}
	var flat map[string]any
	if err := json.Unmarshal(body, &flat); err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for k, v := range flat {
		if f, ok := v.(float64); ok {
			out[k] = f
		}
	}
	return out, nil
}

func computeBaselineDiff(a, b map[string]float64) []baselineEntry {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	out := []baselineEntry{}
	// Preferred order: p50, p90, p95, p99, p999, avg, then any
	// extra keys lexicographically.
	preferred := []string{"p50", "p90", "p95", "p99", "p999", "avg"}
	seen := map[string]bool{}
	for _, k := range preferred {
		if keys[k] {
			out = append(out, makeEntry(k, a[k], b[k]))
			seen[k] = true
		}
	}
	rest := []string{}
	for k := range keys {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	// Sort manually to avoid importing sort here too.
	for i := 0; i < len(rest); i++ {
		for j := i + 1; j < len(rest); j++ {
			if rest[i] > rest[j] {
				rest[i], rest[j] = rest[j], rest[i]
			}
		}
	}
	for _, k := range rest {
		out = append(out, makeEntry(k, a[k], b[k]))
	}
	return out
}

func makeEntry(key string, before, after float64) baselineEntry {
	delta := 0.0
	if before != 0 {
		delta = (after - before) / before
	} else if after != 0 {
		delta = math.Inf(1)
	}
	return baselineEntry{Key: key, Before: before, After: after, Delta: delta}
}
