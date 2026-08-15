// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// This gate joins the claim register to the wire contract it cites. §28.4 makes
// a register row a statement about the tree, so a row that names a proto field
// the proto does not declare, or that records a capability as unimplemented by
// asserting the absence of a field the proto does declare, states something the
// tree contradicts. Two rows disagreeing about the same field is the failure
// this gate exists to catch, because the register is read as the work queue for
// the steps that follow and a queue that contradicts itself cannot be worked.
//
// The gate also holds the coverage half: a gateway-to-pod request message that
// carries the §10.1 generation fence and whose fence no handler compares is a
// field the register has to track, so the streaming checkpoint path cannot gain
// the fence without gaining its row.
//
// The check reads the register and the proto text alone. Whether a tracked field
// has acquired a production reader is the reachability question §28.4 leaves to
// the step that builds that gate.

const adapterProtoPath = "schemas/lenny-adapter.proto"

// generationFenceField is the fence field §10.1's handoff protocol carries on a
// gateway-to-pod request, and slotIDField is the §6.4 per-slot dimension.
const (
	generationFenceField = "coordination_generation"
	slotIDField          = "slot_id"
)

// fenceReadersExempt names the request messages whose generation fence a handler
// already compares, so the fence is not an unread field the register tracks.
// `CoordinatorFence` carries the acquiring coordinator's generation as the whole
// point of the RPC and `pkg/adapter/coordination.go` compares it on arrival.
var fenceReadersExempt = map[string]bool{"CoordinatorFenceRequest": true}

var (
	// protoMessageOpen matches the opening line of a top-level message.
	protoMessageOpen = regexp.MustCompile(`^message (\w+) \{`)
	// protoField matches a field declaration inside a message body, including
	// the arms of a oneof.
	protoField = regexp.MustCompile(`^\s*(?:repeated\s+)?[\w.]+\s+(\w+)\s*=\s*\d+\s*;`)
	// claimFieldRow matches a row that names one message field, in either of the
	// two spellings the register uses: `Message.field` and `Message <name> field`.
	claimQualifiedField = regexp.MustCompile(`^(\w+)\.(\w+)`)
	claimSlotField      = regexp.MustCompile(`^(\w+) slot identifier field$`)
	// absenceAssertion matches a row that records a capability as unimplemented
	// by naming a field the message does not carry.
	absenceAssertion = regexp.MustCompile("no `(\\w+)` on `(\\w+)`")
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

// braceDelta is how far one line moves the brace depth.
func braceDelta(line string) int {
	return strings.Count(line, "{") - strings.Count(line, "}")
}

// registerProtoDisagreements returns the findings the register's own text and
// the proto's own text support together.
func registerProtoDisagreements(registerBody []byte, protoBody string) []string {
	var doc claimRegister
	if err := json.Unmarshal(registerBody, &doc); err != nil {
		return []string{fmt.Sprintf("the register does not parse as JSON: %v", err)}
	}
	if doc.Claims == nil {
		return []string{"the register declares no claims block"}
	}
	fields := protoFields(protoBody)

	var findings []string
	tracked := map[string]bool{}
	for _, c := range *doc.Claims {
		if strings.Contains(c.Surface, adapterProtoPath) {
			if msg, field, ok := namedField(c.Claim); ok {
				tracked[msg+"."+field] = true
				if !fields[msg][field] {
					findings = append(findings, fmt.Sprintf(
						"%q names %s as the surface for %s.%s, which the proto does not declare",
						c.Claim, adapterProtoPath, msg, field,
					))
				}
			}
		}
		for _, text := range []string{c.Surface, c.Note} {
			for _, m := range absenceAssertion.FindAllStringSubmatch(text, -1) {
				if fields[m[2]][m[1]] {
					findings = append(findings, fmt.Sprintf(
						"%q says there is no %s on %s, which the proto declares",
						c.Claim, m[1], m[2],
					))
				}
			}
		}
	}

	for msg, declared := range fields {
		if !declared[generationFenceField] || fenceReadersExempt[msg] {
			continue
		}
		if !tracked[msg+"."+generationFenceField] {
			findings = append(findings, fmt.Sprintf(
				"the proto declares %s.%s and the register carries no row for it",
				msg, generationFenceField,
			))
		}
	}
	sort.Strings(findings)
	return findings
}

// namedField reports the message and field a row's claim names, in either of the
// two spellings the register uses for a field row.
func namedField(claimText string) (string, string, bool) {
	if m := claimSlotField.FindStringSubmatch(claimText); m != nil {
		return m[1], slotIDField, true
	}
	if m := claimQualifiedField.FindStringSubmatch(claimText); m != nil {
		return m[1], m[2], true
	}
	return "", "", false
}

// spec: 28.4 (claim register), 6.4 (concurrent sessions per pod), 10.1 (coordinator handoff)
// diagnosis: the claim register and the adapter proto disagree. Either a row
// names a field the proto does not declare, a row records a capability as
// unimplemented by asserting the absence of a field the proto declares, or a
// request message carries the generation fence with no row tracking it. A
// register that contradicts the wire contract cannot be read as a work queue.
func TestClaimRegisterAgreesWithTheAdapterProto(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	registerBody, err := os.ReadFile(filepath.Join(root, claimRegisterPath))
	if err != nil {
		t.Fatalf("%s: %v", claimRegisterPath, err)
	}
	protoBody, err := os.ReadFile(filepath.Join(root, adapterProtoPath))
	if err != nil {
		t.Fatalf("%s: %v", adapterProtoPath, err)
	}
	for _, f := range registerProtoDisagreements(registerBody, string(protoBody)) {
		t.Errorf("%s: %s", claimRegisterPath, f)
	}
}

// spec: 28.4 (claim register), 6.4 (concurrent sessions per pod), 10.1 (coordinator handoff)
// diagnosis: the register-to-proto check stopped reporting a disagreement it
// must refuse, so a register naming an absent field, contradicting itself about
// a declared field, or omitting a fence row would be accepted.
func TestClaimRegisterProtoCheckRefusesADisagreeingRegister(t *testing.T) {
	t.Parallel()
	const proto = `message InterruptRequest {
  SessionId session_id = 1;
  int64 coordination_generation = 4;
  SlotId slot_id = 5;
}

message CheckpointRequest {
  oneof msg {
    CheckpointStart start = 1;
  }
  int64 coordination_generation = 4;
}
`
	row := func(claimText, surface, note string) string {
		return fmt.Sprintf(
			`{"claim":%q,"status":"UNWIRED","surface":%q,"note":%q,"deferral_id":"R16"}`,
			claimText, surface, note,
		)
	}
	register := func(rows ...string) string {
		return `{"kind":"claim-register","version":1,"claims":[` + strings.Join(rows, ",") + `]}`
	}
	fenceRows := []string{
		row("InterruptRequest.coordination_generation generation fence field", adapterProtoPath, ""),
		row("CheckpointRequest.coordination_generation generation fence field", adapterProtoPath, ""),
	}

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"row naming a field the proto does not declare",
			register(append(append([]string{}, fenceRows...),
				row("ResumeRequest.slot_id", adapterProtoPath, ""))...),
			"which the proto does not declare",
		},
		{
			"slot-identifier row naming a message that carries no slot",
			register(append(append([]string{}, fenceRows...),
				row("ReportUsageRequest slot identifier field", adapterProtoPath, ""))...),
			"ReportUsageRequest.slot_id, which the proto does not declare",
		},
		{
			"row asserting the absence of a declared field in its surface",
			register(append(append([]string{}, fenceRows...),
				row("Slot-qualified interrupt", "no `slot_id` on `InterruptRequest`", ""))...),
			"which the proto declares",
		},
		{
			"row asserting the absence of a declared field in its note",
			register(append(append([]string{}, fenceRows...),
				row("Slot-qualified interrupt", "`pkg/adapter/lifecycle.go:21`", "no `slot_id` on `InterruptRequest`"))...),
			"which the proto declares",
		},
		{
			"fence field carried on a request with no row",
			register(fenceRows[0]),
			"the proto declares CheckpointRequest.coordination_generation and the register carries no row for it",
		},
		{
			"register that does not parse",
			"{",
			"does not parse",
		},
		{
			"register with no claims block",
			`{"kind":"claim-register","version":1}`,
			"declares no claims block",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := registerProtoDisagreements([]byte(c.body), proto)
			if len(got) == 0 {
				t.Fatalf("the check accepted a register it must refuse")
			}
			if !strings.Contains(strings.Join(got, "\n"), c.want) {
				t.Errorf("findings %q do not name %q", got, c.want)
			}
		})
	}
}

// spec: 28.4 (claim register), 6.4 (concurrent sessions per pod), 10.1 (coordinator handoff)
// diagnosis: the register-to-proto check started refusing a register that agrees
// with the wire contract, so a correct register cannot be landed.
func TestClaimRegisterProtoCheckAcceptsAnAgreeingRegister(t *testing.T) {
	t.Parallel()
	const proto = `message InterruptRequest {
  SessionId session_id = 1;
  int64 coordination_generation = 4;
  SlotId slot_id = 5;
}

message CoordinatorFenceRequest {
  SessionId session_id = 1;
  int64 coordination_generation = 2;
}
`
	body := `{"kind":"claim-register","version":1,"claims":[
      {"claim":"InterruptRequest.coordination_generation generation fence field","status":"UNWIRED","surface":"schemas/lenny-adapter.proto","deferral_id":"R16"},
      {"claim":"InterruptRequest slot identifier field","status":"UNWIRED","surface":"schemas/lenny-adapter.proto","deferral_id":"R22"},
      {"claim":"Slot-qualified interrupt","status":"ABSENT","surface":"` + "`pkg/adapter/lifecycle.go:21`" + ` ignores the request's slot identifier","deferral_id":"R22"},
      {"claim":"` + "`CoordinatorFence`" + `","status":"WIRED","surface":"pkg/adapter/coordination.go:85"}]}`
	if got := registerProtoDisagreements([]byte(body), proto); len(got) != 0 {
		t.Errorf("the check refused an agreeing register: %q", got)
	}
}
