// SPDX-License-Identifier: MIT

// Fixture: a gate registered through the second channel the repository
// hard-gates, a check inside runValidate. It is fixture material rather
// than compiled source; the go tool does not read a testdata tree.

package main

func runValidate(args []string) int {
	results := []checkResult{
		validateExampleRegister(args),
	}
	for _, r := range results {
		if !r.ok {
			return 1
		}
	}
	return 0
}
