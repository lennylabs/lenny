// SPDX-License-Identifier: MIT

package pdbwatcher

import (
	"flag"
	"io"
	"testing"
)

// The flag registration is wiring that no requirement states, so this test
// carries no specification annotation.
func TestRegisterFlagsDefaultsThePDBNameToTheGatewayRelease(t *testing.T) {
	t.Setenv("LENNY_GATEWAY_PDB_NAME", "")
	fs := flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := RegisterFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if *f.PDBName != "lenny-gateway" {
		t.Errorf("PDBName = %q, want lenny-gateway", *f.PDBName)
	}
	t.Setenv("LENNY_GATEWAY_PDB_NAME", " acme-gw ")
	if f = RegisterFlags(flag.NewFlagSet("lenny-gateway", flag.ContinueOnError)); *f.PDBName != "acme-gw" {
		t.Errorf("PDBName = %q, want the trimmed variable", *f.PDBName)
	}
}
