// SPDX-License-Identifier: MIT

package pdbwatcher

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the PodDisruptionBudget poller reads.
type Flags struct {
	// PDBName is the name of the gateway's PodDisruptionBudget.
	PDBName *string
}

// RegisterFlags registers the poller's flags on fs and returns their
// values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.PDBName = fs.String("gateway-pdb-name", envdefault.String("LENNY_GATEWAY_PDB_NAME", "lenny-gateway"),
		"§10.4 name of the gateway PodDisruptionBudget object for the periodic poller. Override via LENNY_GATEWAY_PDB_NAME.")
	return f
}
