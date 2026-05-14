// SPDX-License-Identifier: MIT

// Command echo is a reference implementation of the SDK contract
// harness helper protocol. It accepts one Command per stdin line,
// echoes the args back in result, and exits cleanly on `shutdown`.
//
// Tests under tests/tier3_contract/sdks/harness/ build and drive
// this binary to verify the harness wiring is correct before real
// language-native SDK helpers are written.
//
// Real SDK helpers (sdks/client/python/test-helper, etc.) follow
// the same protocol but talk to the gateway over HTTP instead of
// echoing.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type command struct {
	Op   string         `json:"op"`
	Args map[string]any `json:"args,omitempty"`
}

type response struct {
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Code   string         `json:"code,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var c command
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			writeErr(enc, fmt.Sprintf("decode: %v", err))
			continue
		}
		if c.Op == "shutdown" {
			_ = enc.Encode(response{OK: true})
			return
		}
		_ = enc.Encode(response{OK: true, Result: c.Args})
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, "echo helper: scanner err:", err)
		os.Exit(1)
	}
}

func writeErr(enc *json.Encoder, msg string) {
	_ = enc.Encode(response{OK: false, Error: msg, Code: "PROTOCOL_ERROR"})
}
