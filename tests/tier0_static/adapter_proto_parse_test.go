// SPDX-License-Identifier: MIT

package tier0_static

import (
	"regexp"
	"strings"
)

// The adapter proto's text parse, shared by the tier-0 gates that read it.
// Two gates read the same file for different questions: one joins the claim
// register to the fields the proto declares, and one joins the §4.1
// message-scope classification table to the request messages the two services
// declare. They share one parse so a change to the proto's spelling moves both
// gates together.

// adapterProtoPath is the repo-relative path of the gateway-adapter protocol.
const adapterProtoPath = "schemas/lenny-adapter.proto"

var (
	// protoMessageOpen matches the opening line of a top-level message.
	protoMessageOpen = regexp.MustCompile(`^message (\w+) \{`)
	// protoField matches a field declaration inside a message body, including
	// the arms of a oneof.
	protoField = regexp.MustCompile(`^\s*(?:repeated\s+)?[\w.]+\s+(\w+)\s*=\s*\d+\s*;`)
	// protoServiceOpen matches the opening line of a service.
	protoServiceOpen = regexp.MustCompile(`^service (\w+) \{`)
	// protoRPC matches one method declaration and captures its request type,
	// with or without the client-streaming marker.
	protoRPC = regexp.MustCompile(`^\s*rpc \w+\s*\(\s*(?:stream\s+)?([\w.]+)\s*\)`)
)

// protoFields returns, per message, the set of field names the proto declares.
// The adapter proto declares every message at the top level, so a brace depth
// counter is enough to bound a body.
func protoFields(body string) map[string]map[string]bool {
	fields := map[string]map[string]bool{}
	var current string
	depth := 0
	for _, line := range strings.Split(body, "\n") {
		if current == "" {
			if m := protoMessageOpen.FindStringSubmatch(line); m != nil {
				fields[m[1]] = map[string]bool{}
				// A message declared and closed on one line, as an empty
				// message is, opens no body to scan.
				if depth = braceDelta(line); depth > 0 {
					current = m[1]
				}
			}
			continue
		}
		depth += braceDelta(line)
		if depth <= 0 {
			current = ""
			continue
		}
		if m := protoField.FindStringSubmatch(line); m != nil {
			fields[current][m[1]] = true
		}
	}
	return fields
}

// protoServiceRequests returns, per request message name, the service whose
// method declares it. The parse is service-aware because the message name
// alone does not say which service carries it, and the §4.1 table names the
// service on every row.
func protoServiceRequests(body string) map[string]string {
	requests := map[string]string{}
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
			requests[m[1]] = current
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
