// SPDX-License-Identifier: MIT

package supervisorbuild

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// spec: 4.7.11 (Adapter-Agent Security Boundary)
func TestCheckToolchainNamesEveryMissingTool(t *testing.T) {
	none := func(string) (string, error) { return "", exec.ErrNotFound }
	err := CheckToolchain(none)
	if !errors.Is(err, ErrToolchainMissing) {
		t.Fatalf("CheckToolchain with no tools = %v; want ErrToolchainMissing", err)
	}
	for _, tool := range requiredTools {
		if !strings.Contains(err.Error(), tool) {
			t.Errorf("error %q does not name %s", err, tool)
		}
	}
	all := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	if err := CheckToolchain(all); err != nil {
		t.Fatalf("CheckToolchain with every tool = %v; want nil", err)
	}
}
