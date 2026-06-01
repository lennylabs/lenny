// SPDX-License-Identifier: MIT

// Command lenny-ocsf-mapping-gen regenerates schemas/ocsf-mapping.yaml
// from the live §11.7 OCSF class/activity catalog in pkg/audit/ocsf.
// The committed YAML mirror lets external SIEM verifiers consume the
// mapping without reading Go source. CI runs this and fails when the
// committed file drifts from the generated output (see
// pkg/audit/ocsf TestMappingYAMLInSync).
//
// spec: 11_security-trust-model.md line 414.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

func main() {
	out := flag.String("out", "schemas/ocsf-mapping.yaml",
		"path to write the OCSF mapping mirror")
	flag.Parse()

	data, err := ocsf.MarshalMappingYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lenny-ocsf-mapping-gen: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-ocsf-mapping-gen: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "lenny-ocsf-mapping-gen: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", *out, len(data))
}
