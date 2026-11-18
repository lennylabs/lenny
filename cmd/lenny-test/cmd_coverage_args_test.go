// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"io"
	"reflect"
	"testing"
)

// The argument parsing of `coverage --spec` is contributor tooling, so
// these cases carry no specification annotation.

func TestParseInterspersedKeepsQueriesBetweenFlags(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	spec := fs.Bool("spec", false, "")
	jsonOut := fs.Bool("json", false, "")
	got, err := parseInterspersed(fs, []string{"--spec", "POOL-R5", "--json", sec + "4.7"})
	if err != nil {
		t.Fatal(err)
	}
	if !*spec || !*jsonOut || !reflect.DeepEqual(got, []string{"POOL-R5", sec + "4.7"}) {
		t.Fatalf("spec %v, json %v, queries %v", *spec, *jsonOut, got)
	}
	fs = flag.NewFlagSet("coverage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if _, err := parseInterspersed(fs, []string{"--unknown"}); err == nil {
		t.Fatal("an unknown flag is an error")
	}
}

func TestRunCoverageRefusesQueriesWithoutSpec(t *testing.T) {
	t.Parallel()
	if code := runCoverage([]string{"--go", "POOL-R5"}); code != 2 {
		t.Fatalf("a query without --spec exits 2, got %d", code)
	}
}
