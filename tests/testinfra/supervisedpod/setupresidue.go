// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// AdapterProbeExe is the probe binary the Kind e2e adapter image carries
// (cmd/runtimes/supervise-probe/adapter-overlay.Dockerfile), which a setup
// command runs in the adapter container.
const AdapterProbeExe = "/usr/local/bin/lenny-setup-probe"

// SetupResiduePlan is a workspace plan whose one setup command leaves the
// adapter-container residue of superviseprobe.SubSetupResidue under the
// System V key. The supervised keep runtime execs setup commands as argv,
// so the command string is the binary and its arguments.
// spec: §5.2 (Supervised scrub sequence); §7.5.
func SetupResiduePlan(key int) json.RawMessage {
	plan := map[string]any{
		"schemaVersion": 1,
		"sources": []map[string]string{{
			"type": "inlineFile", "path": "setup-residue.txt", "content": "setup residue case", "mode": "0644",
		}},
		"setupCommands": []map[string]any{{
			"cmd":            fmt.Sprintf("%s %s %d", AdapterProbeExe, superviseprobe.SubSetupResidue, key),
			"timeoutSeconds": 30,
		}},
	}
	b, err := json.Marshal(plan)
	if err != nil {
		panic(fmt.Sprintf("SetupResiduePlan: %v", err)) // encoding a static map never fails
	}
	return b
}

// AdapterSetupResidue reads, from pod's adapter container, the probe daemons
// there and whether the segment under key exists.
func AdapterSetupResidue(t testing.TB, c *kind.Cluster, pod string, key int) superviseprobe.SetupResidue {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", Namespace, "exec", pod, "-c", "adapter", "--",
		AdapterProbeExe, superviseprobe.SubSetupResidueCheck, strconv.Itoa(key))
	if err != nil {
		t.Fatalf("read setup residue in the adapter container of %s: %v\n%s", pod, err, out)
	}
	var r superviseprobe.SetupResidue
	if err := json.Unmarshal([]byte(lastLine(out)), &r); err != nil {
		t.Fatalf("decode setup residue %q: %v", out, err)
	}
	return r
}
