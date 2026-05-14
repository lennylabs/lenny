// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// runCachedSubcommand is the CLI wrapper around the lenny-test-cached
// daemon. Today it dispatches status / ensure / endpoints / shutdown.
//
// Usage:
//
//	lenny-test cached status
//	lenny-test cached ensure
//	lenny-test cached endpoints
//	lenny-test cached shutdown
func runCachedSubcommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lenny-test cached <status|ensure|endpoints|shutdown>")
		return 2
	}
	op := args[0]
	switch op {
	case "status", "ensure", "endpoints", "shutdown":
	default:
		fmt.Fprintf(os.Stderr, "lenny-test cached: unknown op %q. Use status|ensure|endpoints|shutdown.\n", op)
		return 2
	}
	if op == "ensure" {
		eps, err := cachedEnsure()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cached ensure: %v\n", err)
			return 1
		}
		body, _ := json.MarshalIndent(eps, "", "  ")
		fmt.Println(string(body))
		return 0
	}
	resp, err := cachedRequest(op)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cached %s: %v\n", op, err)
		return 1
	}
	body, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(body))
	return 0
}
