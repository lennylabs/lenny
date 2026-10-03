// SPDX-License-Identifier: MIT

package tier0_static

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file loads the two registers the deployment-boundary gate reads. The
// exception register carries the shared entry schema, which validate-maps
// holds to the expiry and blocker rules on every tier-0 run, so the loader
// here reads its subjects alone. The defaults register carries a schema the
// gate owns, so the loader here holds it to that schema.

const (
	boundaryExceptionsPath = "tests/registers/exceptions-deployment-boundary.yaml"
	boundaryDefaultsPath   = "tests/registers/deployment-boundary-defaults.yaml"
	boundaryDefaultsKind   = "deployment-boundary-defaults"
	boundaryExceptionKind  = "exception-register"
	// boundaryUnsetDefault is the default an env row records: the variable
	// is absent from the pod, and the binary's own fallback applies.
	boundaryUnsetDefault = "unset"
)

// boundaryDefaultBases are the reasons a default row may give for accepting
// a compiled default.
var boundaryDefaultBases = map[string]bool{
	"mount-path":    true,
	"spec-value":    true,
	"no-spec-value": true,
	"dev-only":      true,
	"superseded":    true,
}

// boundaryDefaultRow is one entry of the defaults register.
type boundaryDefaultRow struct {
	Subject      string `yaml:"subject"`
	Default      string `yaml:"default"`
	Basis        string `yaml:"basis"`
	SupersededBy string `yaml:"superseded_by,omitempty"`
	Reason       string `yaml:"reason"`
}

// isEnvSubject reports whether a subject names an environment variable.
func isEnvSubject(subject string) bool {
	_, rest, ok := strings.Cut(subject, " ")
	return ok && strings.HasPrefix(rest, "env ")
}

// parseExceptionSubjects returns the subjects of an exception register after
// checking its kind and version. A missing, empty, or unparseable body is an
// error, so a register that cannot be read exempts nothing silently.
func parseExceptionSubjects(body []byte) ([]string, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("exception register is empty; a file that declares nothing exempts nothing")
	}
	var doc struct {
		Kind    string `yaml:"kind"`
		Version int    `yaml:"version"`
		Entries []struct {
			Subject string `yaml:"subject"`
		} `yaml:"entries"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse exception register: %w", err)
	}
	if doc.Kind != boundaryExceptionKind {
		return nil, fmt.Errorf("exception register declares kind %q, want %q", doc.Kind, boundaryExceptionKind)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("exception register declares version %d, want 1", doc.Version)
	}
	subjects := make([]string, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		subjects = append(subjects, strings.TrimSpace(e.Subject))
	}
	return subjects, nil
}

// parseDefaultRows decodes the defaults register and holds every row to the
// gate-owned schema.
func parseDefaultRows(body []byte) ([]boundaryDefaultRow, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("defaults register is empty; a file that declares nothing exempts nothing")
	}
	var doc struct {
		Kind    string               `yaml:"kind"`
		Version int                  `yaml:"version"`
		Entries []boundaryDefaultRow `yaml:"entries"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse defaults register: %w", err)
	}
	if doc.Kind != boundaryDefaultsKind {
		return nil, fmt.Errorf("defaults register declares kind %q, want %q", doc.Kind, boundaryDefaultsKind)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("defaults register declares version %d, want 1", doc.Version)
	}
	if doc.Entries == nil {
		return nil, errors.New("defaults register carries no entries list")
	}
	seen := map[string]bool{}
	for i, row := range doc.Entries {
		if err := checkDefaultRow(row); err != nil {
			return nil, fmt.Errorf("defaults register entry %d: %w", i, err)
		}
		if seen[row.Subject] {
			return nil, fmt.Errorf("defaults register entry %d: subject %q appears twice", i, row.Subject)
		}
		seen[row.Subject] = true
	}
	return doc.Entries, nil
}

// checkDefaultRow holds one row to the defaults schema.
func checkDefaultRow(row boundaryDefaultRow) error {
	switch {
	case strings.TrimSpace(row.Subject) == "":
		return errors.New("missing subject")
	case !boundaryDefaultBases[row.Basis]:
		return fmt.Errorf("subject %q: unknown basis %q", row.Subject, row.Basis)
	case strings.TrimSpace(row.Reason) == "":
		return fmt.Errorf("subject %q: empty reason", row.Subject)
	case row.Basis == "superseded" && strings.TrimSpace(row.SupersededBy) == "":
		return fmt.Errorf("subject %q: a superseded row names the flag that supersedes it in superseded_by", row.Subject)
	case row.Basis != "superseded" && row.SupersededBy != "":
		return fmt.Errorf("subject %q: superseded_by belongs on a superseded row only", row.Subject)
	case isEnvSubject(row.Subject) && row.Default != boundaryUnsetDefault:
		return fmt.Errorf("subject %q: an env row records default %q, want %q", row.Subject, row.Default, boundaryUnsetDefault)
	}
	return nil
}
