// SPDX-License-Identifier: MIT

package gatewaylayout

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Register is tests/registers/gateway-layout.yaml: the violations the gate
// accepts until the step or change named on each row ends them.
type Register struct {
	Version  int          `yaml:"version"`
	Packages []PackageRow `yaml:"packages"`
	Imports  []ImportRow  `yaml:"imports"`
}

// PackageRow registers a package that sits outside the directories of the
// table: the part it belongs to, and either the step that moves it there
// or, for a removed package, the change that removes it.
type PackageRow struct {
	Package   string `yaml:"package"`
	Part      string `yaml:"part"`
	Step      string `yaml:"step,omitempty"`
	RemovedBy string `yaml:"removed-by,omitempty"`
}

// ImportRow registers one import that breaks a rule, with the step or the
// change that ends it.
type ImportRow struct {
	From      string `yaml:"from"`
	To        string `yaml:"to"`
	Rule      string `yaml:"rule"`
	Step      string `yaml:"step,omitempty"`
	RemovedBy string `yaml:"removed-by,omitempty"`
}

// LoadRegister reads and decodes a register file. Unknown keys are refused,
// so a misspelled key cannot silently drop a row's step.
func LoadRegister(path string) (Register, error) {
	f, err := os.Open(path)
	if err != nil {
		return Register{}, err
	}
	defer f.Close()
	var r Register
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return Register{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return r, nil
}

var (
	stepExpr   = regexp.MustCompile(`^[0-9]{4}\.S[0-9]+$`)
	changeExpr = regexp.MustCompile(`^[0-9]{4}$`)
)

// validate reports the rows that are malformed: an unknown version, part,
// or rule, a missing or doubled end condition, and a duplicated key.
func (r Register) validate(table []Dir) []string {
	var out []string
	if r.Version != 1 {
		out = append(out, fmt.Sprintf("register version %d, want 1", r.Version))
	}
	seen := map[string]bool{}
	for _, row := range r.Packages {
		where := "package row " + row.Package
		if seen["p:"+row.Package] {
			out = append(out, where+": duplicated")
		}
		seen["p:"+row.Package] = true
		if _, ok := partKind(table, row.Part); !ok {
			out = append(out, fmt.Sprintf("%s: unknown part %q", where, row.Part))
		}
		if row.Part == PartRemoved {
			out = append(out, endCondition(where, row.Step, row.RemovedBy, false)...)
		} else {
			out = append(out, endCondition(where, row.Step, row.RemovedBy, true)...)
		}
	}
	for _, row := range r.Imports {
		where := fmt.Sprintf("import row %s -> %s", row.From, row.To)
		key := "i:" + row.From + " " + row.To
		if seen[key] {
			out = append(out, where+": duplicated")
		}
		seen[key] = true
		if !knownRule(row.Rule) {
			out = append(out, fmt.Sprintf("%s: unknown rule %q", where, row.Rule))
		}
		if row.Step != "" && row.RemovedBy != "" {
			out = append(out, where+": names both a step and a removing change")
		} else if row.Step == "" && row.RemovedBy == "" {
			out = append(out, where+": names neither a step nor a removing change")
		}
		out = append(out, formatErrors(where, row.Step, row.RemovedBy)...)
	}
	return out
}

// endCondition checks the step or removing change of a package row: a
// moved package names a step and no change, and a removed package names a
// change and no step.
func endCondition(where, step, removedBy string, wantStep bool) []string {
	var out []string
	switch {
	case wantStep && step == "":
		out = append(out, where+": names no step that moves it")
	case wantStep && removedBy != "":
		out = append(out, where+": a package that moves names no removing change")
	case !wantStep && removedBy == "":
		out = append(out, where+": a removed package names the change that removes it")
	case !wantStep && step != "":
		out = append(out, where+": a removed package names no step")
	}
	return append(out, formatErrors(where, step, removedBy)...)
}

// formatErrors checks the spelling of a step identifier and a change
// number.
func formatErrors(where, step, removedBy string) []string {
	var out []string
	if step != "" && !stepExpr.MatchString(step) {
		out = append(out, fmt.Sprintf("%s: step %q is not a step identifier such as 0117.S3", where, step))
	}
	if removedBy != "" && !changeExpr.MatchString(removedBy) {
		out = append(out, fmt.Sprintf("%s: removing change %q is not a change number such as 0124", where, removedBy))
	}
	return out
}
