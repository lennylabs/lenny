// SPDX-License-Identifier: MIT

// Command lenny-chart-schema-gen regenerates charts/lenny/values.schema.json
// from the Go struct definitions in pkg/chart/values. Helm validates -f /
// --set inputs against the committed schema on every install and upgrade.
// CI runs this with -check and fails when the committed file drifts from
// the regenerated output, so the published schema cannot fall out of sync
// with the Go types.
//
// Regenerate:  go run ./cmd/lenny-chart-schema-gen
// Drift check: go run ./cmd/lenny-chart-schema-gen -check
//
// spec: §17.6 lines 651-666.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lennylabs/lenny/pkg/chart/values"
)

func main() {
	out := flag.String("out", "charts/lenny/values.schema.json",
		"path to write the chart values schema")
	check := flag.Bool("check", false,
		"verify the committed file matches the generated output; exit 1 on drift")
	flag.Parse()

	data, err := values.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-chart-schema-gen: %v\n", err)
		os.Exit(1)
	}

	if *check {
		existing, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "lenny-chart-schema-gen: read %s: %v\n", *out, err)
			os.Exit(1)
		}
		if !bytes.Equal(existing, data) {
			fmt.Fprintf(os.Stderr,
				"lenny-chart-schema-gen: %s is stale; run `go run ./cmd/lenny-chart-schema-gen`\n", *out)
			os.Exit(1)
		}
		return
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-chart-schema-gen: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-chart-schema-gen: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", *out, len(data))
}
