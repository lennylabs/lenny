// SPDX-License-Identifier: MIT

package ctlcli

import (
	"flag"
	"fmt"
	"io"
	"os"

	chartvalues "github.com/lennylabs/lenny/pkg/chart/values"
)

// cmdValues dispatches the `lenny-ctl values` subcommands. The group runs
// offline; it issues no gateway calls. spec: §24.20 line 303, §17.6 line
// 666.
func cmdValues(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny-ctl values: expected a subcommand (validate)")
		return 2
	}
	switch args[0] {
	case "validate":
		return cmdValuesValidate(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny-ctl values: unknown subcommand %q (expected validate)\n", args[0])
		return 2
	}
}

// cmdValuesValidate validates a values.yaml against the chart's
// values.schema.json. It exits 0 on success and prints a JSON Schema
// validation report on failure (exit 1). The schema is generated from the
// same pkg/chart/values source of truth the committed
// charts/lenny/values.schema.json is built from, so the CLI needs no
// chart checkout to run. spec: §17.6 line 666 ("recommended check for CI
// pipelines that render values but do not run helm install"), §24.20 line
// 303.
func cmdValuesValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("values validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	config := fs.String("config", "", "path to the values.yaml to validate (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *config == "" {
		fmt.Fprintln(stderr, "lenny-ctl values validate: --config <values.yaml> is required")
		return 2
	}

	data, err := os.ReadFile(*config)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl values validate: read %s: %v\n", *config, err)
		return 1
	}
	schema, err := chartvalues.Generate()
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl values validate: generate schema: %v\n", err)
		return 1
	}
	if err := chartvalues.ValidateYAML(schema, data); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl values validate: %s does not conform to the Lenny chart values schema:\n%v\n", *config, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s conforms to the Lenny chart values schema\n", *config)
	return 0
}
