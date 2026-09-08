// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// This gate holds the addressing convention §4.1 derives a request message's
// scope from. §4.1 classifies a request message session-scoped exactly when it
// declares a top-level `session_id` field of type `SessionId`, and classifies a
// stream envelope through the one frame that declares that address. The
// derivation is sound only while the protocol spells the address one way in
// both directions and an envelope carries exactly one addressing frame, so this
// gate refuses a protocol definition in which a field named `session_id` is not
// of type `SessionId`, a field of type `SessionId` is not named `session_id`, a
// stream envelope declares an address of its own, or an envelope's frames
// declare other than exactly one address.
//
// The gate reads the proto text alone. What a handler does with the address is
// a runtime question the tier-1 and tier-3 suites own, and a session addressed
// under both an unconventional name and an unconventional type is outside what
// any reading of the proto text can see.

// sessionAddressField is the one field name a request on this protocol
// addresses a session with.
const sessionAddressField = "session_id"

// sessionAddressType is the type that field carries.
const sessionAddressType = "SessionId"

// declaresTheAddress reports whether a message declares the session address at
// its own top level. The type is what makes a field the address, so the check
// reads the declared type rather than the field name; holding the two spellings
// together is the job of the convention arms below.
func declaresTheAddress(fields map[string]protoFieldDecl) bool {
	for _, decl := range fields {
		if decl.OneOf == "" && decl.Type == sessionAddressType {
			return true
		}
	}
	return false
}

// frameTypes returns the message types a stream envelope carries in its oneof,
// in declaration-name order, and reports whether the message carries a oneof at
// all. A message that carries one is a stream envelope.
func frameTypes(fields map[string]protoFieldDecl) ([]string, bool) {
	names := make([]string, 0, len(fields))
	for name, decl := range fields {
		if decl.OneOf != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, false
	}
	sort.Strings(names)
	types := make([]string, 0, len(names))
	for _, name := range names {
		types = append(types, fields[name].Type)
	}
	return types, true
}

// addressingConventionDisagreements returns the findings the proto's own text
// supports against the addressing convention the scope derivation rests on.
func addressingConventionDisagreements(protoBody string) []string {
	fields := protoFields(protoBody)

	var findings []string
	for msg, declared := range fields {
		for name, decl := range declared {
			if name == sessionAddressField && decl.Type != sessionAddressType {
				findings = append(findings, fmt.Sprintf(
					"%s.%s is named %s and is of type %s, and a field named %s is of type %s",
					msg, name, sessionAddressField, decl.Type, sessionAddressField, sessionAddressType,
				))
			}
			if decl.Type == sessionAddressType && name != sessionAddressField {
				findings = append(findings, fmt.Sprintf(
					"%s.%s is of type %s and is named %s, and a field of type %s is named %s",
					msg, name, sessionAddressType, name, sessionAddressType, sessionAddressField,
				))
			}
		}
	}

	for msg := range protoServiceRequests(protoBody) {
		frames, envelope := frameTypes(fields[msg])
		if !envelope {
			continue
		}
		if declaresTheAddress(fields[msg]) {
			findings = append(findings, fmt.Sprintf(
				"%s carries its frames in a oneof and declares an address of its own, and a stream envelope takes the scope of the frame that addresses it",
				msg,
			))
		}
		addressed := 0
		for _, frame := range frames {
			if declaresTheAddress(fields[frame]) {
				addressed++
			}
		}
		if addressed != 1 {
			findings = append(findings, fmt.Sprintf(
				"the frames of the stream envelope %s declare %d addresses, and exactly one frame declares the address that opens the stream",
				msg, addressed,
			))
		}
	}
	sort.Strings(findings)
	return findings
}

// spec: 4.1
// diagnosis: the gateway-adapter protocol departs from the addressing
// convention a request message's scope is derived from. Either a field named
// session_id is not of type SessionId, a field of type SessionId is not named
// session_id, a stream envelope declares an address of its own, or an
// envelope's frames declare other than exactly one address. A message that
// departs carries a scope the derivation does not compute, so the class it is
// handled under and the class the specification states are no longer the same.
func TestAdapterProtoAddressesASessionOneWay(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	protoBody, err := os.ReadFile(filepath.Join(root, adapterProtoPath))
	if err != nil {
		t.Fatalf("%s: %v", adapterProtoPath, err)
	}
	for _, f := range addressingConventionDisagreements(string(protoBody)) {
		t.Errorf("%s: %s", adapterProtoPath, f)
	}
}

// spec: 4.1
// diagnosis: the addressing-convention gate's own predicate is broken. It
// accepted a protocol definition it must refuse, or refused one it must
// accept, so a green run of the gate above says nothing about the tree.
func TestAddressingConventionGateRefusesAnUnconventionalAddress(t *testing.T) {
	t.Parallel()
	const proto = `
service Adapter {
  rpc Interrupt(InterruptRequest) returns (InterruptResponse) {}
  rpc Checkpoint(stream CheckpointRequest) returns (stream CheckpointResponse) {}
}
service GatewayControl {
  rpc ReportPodScrub(ReportPodScrubRequest) returns (ReportPodScrubResponse) {}
}

message InterruptRequest {
  SessionId session_id = 1;
}

message CheckpointRequest {
  oneof msg {
    CheckpointStart start = 1;
    CheckpointGrant grant = 2;
  }
  int64 coordination_generation = 4;
}

message CheckpointStart {
  string checkpoint_id = 1;
  SessionId session_id = 7;
}

message CheckpointGrant {
  uint32 index = 1;
}

message ReportPodScrubRequest {
  string pod_id = 1;
}

message CheckpointResponse {
  oneof msg {
    CheckpointAck ack = 1;
    CheckpointDeny deny = 2;
  }
}

message CheckpointAck {
  string checkpoint_id = 1;
}

message CheckpointDeny {
  string reason = 1;
}
`

	// The fixture carries a response envelope whose frames address nothing, so
	// a green run here also pins the population the envelope clause reads: §4.1
	// states that clause over a request message, and a message the proto
	// declares as a response carries a oneof without being an envelope the
	// derivation classifies.
	if got := addressingConventionDisagreements(proto); len(got) != 0 {
		t.Fatalf("the gate refused a protocol definition that keeps the addressing convention: %v", got)
	}

	cases := map[string]struct {
		proto string
		want  string
	}{
		"a field named session_id under another type": {
			proto: strings.Replace(proto, "  SessionId session_id = 1;", "  string session_id = 1;", 1),
			want:  "InterruptRequest.session_id is named session_id and is of type string, and a field named session_id is of type SessionId",
		},
		"a field of the address type under another name": {
			proto: strings.Replace(proto, "  SessionId session_id = 1;", "  SessionId interrupted_session = 1;", 1),
			want:  "InterruptRequest.interrupted_session is of type SessionId and is named interrupted_session, and a field of type SessionId is named session_id",
		},
		"a stream envelope declaring an address of its own": {
			proto: strings.Replace(proto, "  int64 coordination_generation = 4;", "  int64 coordination_generation = 4;\n  SessionId session_id = 5;", 1),
			want:  "CheckpointRequest carries its frames in a oneof and declares an address of its own, and a stream envelope takes the scope of the frame that addresses it",
		},
		"a stream envelope whose frames declare no address": {
			proto: strings.Replace(proto, "  SessionId session_id = 7;", "  string checkpoint_ref = 7;", 1),
			want:  "the frames of the stream envelope CheckpointRequest declare 0 addresses, and exactly one frame declares the address that opens the stream",
		},
		"a stream envelope whose frames declare two addresses": {
			proto: strings.Replace(proto, "  uint32 index = 1;", "  uint32 index = 1;\n  SessionId session_id = 2;", 1),
			want:  "the frames of the stream envelope CheckpointRequest declare 2 addresses, and exactly one frame declares the address that opens the stream",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := addressingConventionDisagreements(tc.proto)
			for _, f := range got {
				if f == tc.want {
					return
				}
			}
			t.Errorf("the gate accepted a protocol definition it must refuse; findings=%v, want %q", got, tc.want)
		})
	}
}
