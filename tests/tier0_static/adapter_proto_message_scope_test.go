// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// This gate joins the §4.1 message-scope classification table to the request
// messages the gateway-adapter protocol declares. §4.1 declares each request
// message's scope in the table rather than deriving it from the message's
// field set, because `session_id` appears on messages of both classes. A
// declared classification is only as good as its coverage: a message the
// protocol declares and the table omits carries no scope at all, and a row
// naming a message neither service declares classifies nothing.
//
// The gate reads the specification text and the proto text alone. Whether a
// handler enforces the scope a row declares is a runtime question the tier-1
// and tier-3 suites own.

// messageScopeSpecPath is the repo-relative path of the section carrying the
// classification table.
const messageScopeSpecPath = "spec/04_system-components.md"

// checkpointStartMessage is the stream-opening frame §4.1 classifies in its
// own right. It is not an RPC's request type, so the RPC parse does not reach
// it, and §4.1 states its row explicitly.
const checkpointStartMessage = "CheckpointStart"

// checkpointStartService is the service whose stream the frame opens.
const checkpointStartService = "Adapter"

// messageScopeRow matches one row of the classification table: the request
// message, the service, the direction, and the scope.
var messageScopeRow = regexp.MustCompile("^\\| `(\\w+)` \\| `(\\w+)` \\| ([^|]+) \\| ([^|]+) \\|$")

// scopeRow is one row of the §4.1 table.
type scopeRow struct {
	service string
	scope   string
}

// parseMessageScopeTable returns the table's rows keyed by request message,
// and the messages it names more than once. A message classified twice is a
// table that states two scopes for one address.
func parseMessageScopeTable(body string) (map[string]scopeRow, []string) {
	rows := map[string]scopeRow{}
	var duplicates []string
	for _, line := range strings.Split(body, "\n") {
		m := messageScopeRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, seen := rows[m[1]]; seen {
			duplicates = append(duplicates, m[1])
			continue
		}
		rows[m[1]] = scopeRow{service: m[2], scope: strings.TrimSpace(m[4])}
	}
	sort.Strings(duplicates)
	return rows, duplicates
}

// declaredScope reports whether a scope cell declares one of the two classes.
// The cell may qualify the class, as the stream envelope's row does, so the
// class is read from the cell's first word.
func declaredScope(cell string) bool {
	switch strings.Fields(cell)[0] {
	case "session", "pod":
		return true
	}
	return false
}

// messageScopeDisagreements returns the findings the classification table and
// the proto support together.
func messageScopeDisagreements(specBody, protoBody string) []string {
	rows, duplicates := parseMessageScopeTable(specBody)
	inScope := protoServiceRequests(protoBody)
	inScope[checkpointStartMessage] = checkpointStartService

	var findings []string
	for _, msg := range duplicates {
		findings = append(findings, fmt.Sprintf(
			"the table classifies %s more than once, so it declares two scopes for one request message", msg,
		))
	}
	for msg, service := range inScope {
		row, ok := rows[msg]
		if !ok {
			findings = append(findings, fmt.Sprintf(
				"%s is the request type of a %s method and the table carries no row for it",
				msg, service,
			))
			continue
		}
		if row.service != service {
			findings = append(findings, fmt.Sprintf(
				"the table names %s as a %s request message and %s declares it",
				msg, row.service, service,
			))
		}
		if !declaredScope(row.scope) {
			findings = append(findings, fmt.Sprintf(
				"the table's %s row declares the scope %q, which is neither session nor pod",
				msg, row.scope,
			))
		}
	}
	for msg := range rows {
		if _, ok := inScope[msg]; !ok {
			findings = append(findings, fmt.Sprintf(
				"the table carries a row for %s, which neither service declares as a request message", msg,
			))
		}
	}
	sort.Strings(findings)
	return findings
}

// spec: 4.1 (message-scope classification), 4.5 (one address per request), 28.5.3 (addressing)
// diagnosis: the §4.1 classification table and the adapter proto disagree.
// Either a request message one of the two services declares carries no row, a
// row names a message neither service declares, a row names the wrong service,
// or a row declares a scope that is neither session nor pod. A request message
// with no declared scope is addressed by convention rather than by the
// specification, which is the state the table exists to prevent.
func TestAdapterProtoRequestMessagesAreClassifiedByScope(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	specBody, err := os.ReadFile(filepath.Join(root, messageScopeSpecPath))
	if err != nil {
		t.Fatalf("%s: %v", messageScopeSpecPath, err)
	}
	protoBody, err := os.ReadFile(filepath.Join(root, adapterProtoPath))
	if err != nil {
		t.Fatalf("%s: %v", adapterProtoPath, err)
	}
	for _, f := range messageScopeDisagreements(string(specBody), string(protoBody)) {
		t.Errorf("%s vs %s: %s", messageScopeSpecPath, adapterProtoPath, f)
	}
}

// spec: 4.1 (message-scope classification)
// diagnosis: the classification gate's own predicate is broken. It accepted a
// table and a proto it must refuse, or refused a pair it must accept, so a
// green run of the gate above says nothing about the tree.
func TestMessageScopeGateRefusesAnUnclassifiedOrUnknownMessage(t *testing.T) {
	t.Parallel()
	const proto = `
service Adapter {
  rpc Interrupt(InterruptRequest) returns (InterruptResponse) {}
  rpc Checkpoint(stream CheckpointRequest) returns (stream CheckpointResponse) {}
}
service GatewayControl {
  rpc ReportPodScrub(ReportPodScrubRequest) returns (ReportPodScrubResponse) {}
}
`
	const table = "| Request message | Service | Direction | Scope |\n" +
		"|:--|:--|:--|:--|\n" +
		"| `InterruptRequest` | `Adapter` | gateway → adapter | session |\n" +
		"| `CheckpointRequest` | `Adapter` | gateway → adapter | session (stream envelope) |\n" +
		"| `CheckpointStart` | `Adapter` | gateway → adapter | session |\n" +
		"| `ReportPodScrubRequest` | `GatewayControl` | adapter → gateway | pod |\n"

	if got := messageScopeDisagreements(table, proto); len(got) != 0 {
		t.Fatalf("the gate refused a table that classifies every declared request message: %v", got)
	}

	cases := map[string]struct {
		table string
		want  string
	}{
		"a declared request message the table omits": {
			table: strings.Replace(table, "| `InterruptRequest` | `Adapter` | gateway → adapter | session |\n", "", 1),
			want:  "InterruptRequest is the request type of a Adapter method and the table carries no row for it",
		},
		"a row naming a message neither service declares": {
			table: table + "| `ShutdownRequest` | `Adapter` | gateway → adapter | session |\n",
			want:  "the table carries a row for ShutdownRequest, which neither service declares as a request message",
		},
		"a row naming the wrong service": {
			table: strings.Replace(table, "| `ReportPodScrubRequest` | `GatewayControl` |", "| `ReportPodScrubRequest` | `Adapter` |", 1),
			want:  "the table names ReportPodScrubRequest as a Adapter request message and GatewayControl declares it",
		},
		"a row declaring neither class": {
			table: strings.Replace(table, "| `InterruptRequest` | `Adapter` | gateway → adapter | session |", "| `InterruptRequest` | `Adapter` | gateway → adapter | slot |", 1),
			want:  `the table's InterruptRequest row declares the scope "slot", which is neither session nor pod`,
		},
		"a message classified twice": {
			table: table + "| `InterruptRequest` | `Adapter` | gateway → adapter | pod |\n",
			want:  "the table classifies InterruptRequest more than once, so it declares two scopes for one request message",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := messageScopeDisagreements(tc.table, proto)
			for _, f := range got {
				if f == tc.want {
					return
				}
			}
			t.Errorf("the gate accepted a table it must refuse; findings=%v, want %q", got, tc.want)
		})
	}
}
