// SPDX-License-Identifier: MIT

package gatewaywiring

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
)

// Register is tests/registers/gateway-wiring.yaml: the crossings the check
// accepts until the step on each row routes them through the seam
// interface.
type Register struct {
	Version   int   `yaml:"version"`
	Crossings []Row `yaml:"crossings"`
}

// Row registers one crossing. The package paths are fields of their own,
// so the move driver rewrites them as it rewrites every register keyed by
// package path.
type Row struct {
	To        string `yaml:"to"`
	Interface string `yaml:"interface"`
	From      string `yaml:"from"`
	Type      string `yaml:"type"`
	Step      string `yaml:"step"`
}

func (r Row) key() string { return r.To + "\x00" + r.Interface + "\x00" + r.From + "\x00" + r.Type }

func (r Row) String() string {
	return fmt.Sprintf("%s.%s receives %s.%s", r.To, r.Interface, r.From, r.Type)
}

// LoadRegister reads a register file.
func LoadRegister(path string) (Register, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Register{}, err
	}
	return ParseRegister(data)
}

// ParseRegister decodes a register, refusing unknown keys.
func ParseRegister(data []byte) (Register, error) {
	var r Register
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return Register{}, err
	}
	return r, nil
}

var stepExpr = regexp.MustCompile(`^[0-9]{4}\.S[0-9]+$`)

// Result is the outcome of reconciling the crossings with the register.
type Result struct {
	Unregistered []Crossing
	Stale        []string
	Malformed    []string
}

// Failed reports whether the check refuses the tree.
func (r Result) Failed() bool {
	return len(r.Unregistered) > 0 || len(r.Stale) > 0 || len(r.Malformed) > 0
}

// Reconcile splits the crossings into those the register does not carry,
// lists the rows that match no crossing, and validates the rows.
func Reconcile(crossings []Crossing, reg Register) Result {
	var res Result
	if reg.Version != 1 {
		res.Malformed = append(res.Malformed, fmt.Sprintf("register version %d, want 1", reg.Version))
	}
	rows := map[string]bool{}
	for _, row := range reg.Crossings {
		if rows[row.key()] {
			res.Malformed = append(res.Malformed, "row "+row.String()+": duplicated")
		}
		rows[row.key()] = true
		if row.To == "" || row.Interface == "" || row.From == "" || row.Type == "" {
			res.Malformed = append(res.Malformed, "row "+row.String()+": names no target or no value")
		}
		if !stepExpr.MatchString(row.Step) {
			res.Malformed = append(res.Malformed, fmt.Sprintf("row %s: step %q is not a step identifier such as 0117.S7", row, row.Step))
		}
	}
	found := map[string]bool{}
	for _, c := range crossings {
		found[c.Key()] = true
		if !rows[c.Key()] {
			res.Unregistered = append(res.Unregistered, c)
		}
	}
	for _, row := range reg.Crossings {
		if !found[row.key()] {
			res.Stale = append(res.Stale, "row "+row.String()+": the composition root no longer makes the crossing")
		}
	}
	sort.Strings(res.Stale)
	return res
}

// Ratchet returns the rows the branch added since the merge base that the
// ratchet refuses. A row passes when mapping its package paths back through
// the moves gives a row of the base register, or when its step is the step
// that moves one of its packages.
func Ratchet(base, current Register, moves []gatewaylayout.Move) []string {
	baseRows := map[string]bool{}
	for _, row := range base.Crossings {
		baseRows[row.key()+"\x00"+row.Step] = true
	}
	var out []string
	for _, row := range current.Crossings {
		if baseRows[row.key()+"\x00"+row.Step] {
			continue
		}
		back := row
		back.To, back.From = gatewaylayout.MapBack(row.To, moves), gatewaylayout.MapBack(row.From, moves)
		if baseRows[back.key()+"\x00"+back.Step] ||
			gatewaylayout.MovedAtStep(row.To, row.Step, moves) || gatewaylayout.MovedAtStep(row.From, row.Step, moves) {
			continue
		}
		out = append(out, "row "+row.String()+": the row is new since the merge base; the register only shrinks, except in the step that moves one of its packages")
	}
	sort.Strings(out)
	return out
}
