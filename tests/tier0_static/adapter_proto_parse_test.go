// SPDX-License-Identifier: MIT

package tier0_static

import (
	"regexp"
	"strings"
)

// The adapter proto's text parse, shared by the tier-0 gates that read it.
// Two gates read the same file for different questions: one joins the claim
// register to the fields the proto declares, and one holds the addressing
// convention §4.1 derives a request message's scope from. They share one parse
// so a change to the proto's spelling moves both gates together.

// adapterProtoPath is the repo-relative path of the gateway-adapter protocol.
const adapterProtoPath = "schemas/lenny-adapter.proto"

var (
	// protoMessageOpen matches the opening line of a top-level message.
	protoMessageOpen = regexp.MustCompile(`^message (\w+) \{`)
	// protoField matches a field declaration inside a message body, including
	// the arms of a oneof, capturing the declared type and the field name.
	protoField = regexp.MustCompile(`^\s*(?:repeated\s+)?([\w.]+)\s+(\w+)\s*=\s*\d+\s*;`)
	// protoOneOfOpen matches the opening line of a oneof inside a message body.
	protoOneOfOpen = regexp.MustCompile(`^\s*oneof (\w+) \{`)
	// protoServiceOpen matches the opening line of a service.
	protoServiceOpen = regexp.MustCompile(`^service (\w+) \{`)
	// protoRPC matches one method declaration and captures its request type,
	// with or without the client-streaming marker.
	protoRPC = regexp.MustCompile(`^\s*rpc \w+\s*\(\s*(?:stream\s+)?([\w.]+)\s*\)`)
)

// protoFieldDecl is one field declaration as the proto text states it. Type is
// the declared type, and OneOf is the name of the oneof that carries the field,
// empty for a field declared at the message's top level. The scope gate reads
// both: a message's address is a top-level field of the address type, and a
// message whose frames sit in a oneof is a stream envelope.
type protoFieldDecl struct {
	Type  string
	OneOf string
}

// protoFields returns, per message, the field declarations the proto states,
// keyed by field name. The adapter proto declares every message at the top
// level, so a brace depth counter is enough to bound a body.
func protoFields(body string) map[string]map[string]protoFieldDecl {
	fields := map[string]map[string]protoFieldDecl{}
	var current, oneof string
	depth := 0
	for _, line := range strings.Split(body, "\n") {
		if current == "" {
			if m := protoMessageOpen.FindStringSubmatch(line); m != nil {
				fields[m[1]] = map[string]protoFieldDecl{}
				// A message declared and closed on one line, as an empty
				// message is, opens no body to scan.
				if depth = braceDelta(line); depth > 0 {
					current = m[1]
					oneof = ""
				}
			}
			continue
		}
		depth += braceDelta(line)
		if depth <= 0 {
			current, oneof = "", ""
			continue
		}
		if m := protoOneOfOpen.FindStringSubmatch(line); m != nil {
			oneof = m[1]
			continue
		}
		// The oneof's closing brace returns the body to the message's own
		// depth, so a field below it is declared at the top level again.
		if depth == 1 {
			oneof = ""
		}
		if m := protoField.FindStringSubmatch(line); m != nil {
			fields[current][m[2]] = protoFieldDecl{Type: m[1], OneOf: oneof}
		}
	}
	return fields
}

// protoServiceRequests returns the set of message names either service
// declares as the request type of an RPC. The addressing-convention gate reads
// it to select the messages its stream-envelope clause applies to, because
// §4.1 states that clause over a request message: a message the proto declares
// only as a frame or as a response can carry a oneof without being an envelope
// the derivation classifies. The declaring service is not part of the result,
// because no gate reads which service carries a message.
func protoServiceRequests(body string) map[string]bool {
	requests := map[string]bool{}
	var current string
	depth := 0
	for _, line := range strings.Split(body, "\n") {
		if current == "" {
			if m := protoServiceOpen.FindStringSubmatch(line); m != nil {
				if depth = braceDelta(line); depth > 0 {
					current = m[1]
				}
			}
			continue
		}
		depth += braceDelta(line)
		if m := protoRPC.FindStringSubmatch(line); m != nil {
			requests[m[1]] = true
		}
		if depth <= 0 {
			current = ""
		}
	}
	return requests
}

// braceDelta is how far one line moves the brace depth.
func braceDelta(line string) int {
	return strings.Count(line, "{") - strings.Count(line, "}")
}
