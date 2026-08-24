// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance case for the SDK-free scaffold the platform ships.
// `lenny runtime scaffold --language binary --template minimal` emits a
// Basic-level skeleton with no SDK to inherit protocol behavior from, so
// the skeleton itself has to satisfy the Basic battery. The scaffold's
// entrypoint is Go text inside a `.tmpl` file, which compiles nowhere in
// the repository's own build, so a wrong or missing edit to it turns
// nothing red until a runtime author scaffolds it.
//
// The case scaffolds the cell, builds it, and runs the whole Basic
// battery against the resulting binary, requiring every check to pass.

package tier10_conformance_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-ctl/runtimescaffold"
)

// spec: 15.4.4 (the Basic-level skeleton the platform ships), 24.18
//
//	(lenny runtime scaffold emits a Basic-level-compliant skeleton for
//	the binary/minimal cell), 28.5.3 (the frames the skeleton reads and
//	emits)
//
// diagnosis: the shipped SDK-free skeleton no longer satisfies the
//
//	Basic battery, so `lenny runtime scaffold --language binary
//	--template minimal` hands a runtime author a starting point the
//	adapter rejects, and the command reference's
//	"Basic-level-compliant skeleton" claim is false. A failure naming
//	`response_echoes_session_id` means the skeleton stopped echoing the
//	per-session identifier it was handed, so its `response` frames are
//	rejected on any pod holding more than one slot. A failure naming a
//	schema check means the frames the skeleton emits no longer validate
//	against the JSON Lines and MessagePart schemas.
func TestScaffoldedBinaryMinimalPassesTheBasicBattery_spec_24_18(t *testing.T) {
	a := buildArtifacts(t)

	base := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := runtimescaffold.Generate(runtimescaffold.Spec{
		Name:     "scaffolded",
		Language: runtimescaffold.LangBinary,
		Template: runtimescaffold.TemplateMinimal,
	}, base, &stdout, &stderr); code != runtimescaffold.ExitOK {
		t.Fatalf("scaffold binary/minimal: exit %d, stderr=%q", code, stderr.String())
	}
	src := filepath.Join(base, "scaffolded")

	// The binary cell has no SDK dependency, so it builds as generated.
	bin := filepath.Join(t.TempDir(), "scaffolded")
	build := exec.Command("go", "build", "-o", bin, "./...")
	build.Dir = src
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the scaffolded skeleton: %v\n%s", err, out)
	}

	report := runCompliance(t, a, bin, "basic")
	if report.Level != "basic" {
		t.Errorf("report level = %q, want basic", report.Level)
	}
	assertAllPass(t, "scaffolded binary/minimal", "basic", report)

	// The battery's membership is asserted separately from its result:
	// a battery that stopped running the per-session echo check would
	// pass the whole-battery assertion above while leaving the echo
	// obligation unverified for the shipped skeleton.
	const echoCheck = "response_echoes_session_id"
	var seen bool
	for _, c := range report.Checks {
		if c.Name == echoCheck {
			seen = true
			break
		}
	}
	if !seen {
		t.Errorf("the basic battery ran no %q check; the per-session echo obligation is unverified for the shipped skeleton", echoCheck)
	}
}
