// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/refactor/rewrite"
)

// audit runs the post-move pre-move-token audit (proposal §4 C4). It greps
// tests/spec-map.json, tests/change-graph.json, and every *.go file (test files
// included) for a surviving pre-move path token in a driver-rewritable
// boundary-anchored form and returns an error (aborting the move) when one
// survives, because the driver provably could fix that form and a survivor is a
// driver bug. It surfaces comment and informational-string occurrences in *.go
// and any pre-move token in a non-Go prose file (markdown, YAML) as a non-fatal
// warning, because the driver cannot rewrite those forms and aborting on them
// would make every C3 group move unsatisfiable (proposal Pass 4).
func (d *driver) audit() error {
	aborts, warns, err := d.collectSurvivors()
	if err != nil {
		return err
	}
	sort.Strings(warns)
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "refactor: WARN stale pre-move reference (manual sweep): %s\n", w)
	}
	if len(aborts) > 0 {
		sort.Strings(aborts)
		return fmt.Errorf("%d driver-rewritable pre-move token(s) survived; the driver should have rewritten these:\n  %s",
			len(aborts), strings.Join(aborts, "\n  "))
	}
	if len(warns) > 0 {
		fmt.Fprintf(os.Stderr, "refactor: %d non-fatal stale reference(s) recorded for manual sweep\n", len(warns))
	}
	return nil
}

// collectSurvivors classifies every surviving pre-move token across the JSON
// maps, the *.go sources, and the non-Go prose files into the abort and warn
// buckets.
func (d *driver) collectSurvivors() (aborts, warns []string, err error) {
	jsonAborts, jsonWarns, err := d.auditJSONMaps()
	if err != nil {
		return nil, nil, err
	}
	aborts = append(aborts, jsonAborts...)
	warns = append(warns, jsonWarns...)

	goAborts, goWarns, err := d.auditGoFiles()
	if err != nil {
		return nil, nil, err
	}
	aborts = append(aborts, goAborts...)
	warns = append(warns, goWarns...)

	proseWarns, err := d.auditProseFiles()
	if err != nil {
		return nil, nil, err
	}
	warns = append(warns, proseWarns...)
	return aborts, warns, nil
}

// auditJSONMaps classifies surviving tokens in the two JSON maps. A
// driver-rewritable quote/slash-bounded JSON string aborts; an in-manifest old
// path that survives only inside a larger informational string value (a "notes"
// sentence, where the token is bounded by a space or by '(', '.', and so on)
// warns, because the driver's JSONTokens never rewrites that form and aborting
// on it would make a group move unsatisfiable. The warn path mirrors the *.go
// and prose surfaces so the §4 C4 audit records the residual stale drift on the
// JSON surface for the optional manual sweep (proposal 0020 §4 C4).
func (d *driver) auditJSONMaps() (aborts, warns []string, err error) {
	for _, rel := range []string{"tests/spec-map.json", "tests/change-graph.json"} {
		path := filepath.Join(d.root, filepath.FromSlash(rel))
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, nil, readErr
		}
		text := string(content)
		for _, m := range d.moves {
			switch rewrite.ClassifyJSON(text, m) {
			case rewrite.Abort:
				aborts = append(aborts, fmt.Sprintf("%s: %s", rel, rewrite.RepoRel(m.Old)))
			case rewrite.Warn:
				warns = append(warns, fmt.Sprintf("%s: %s", rel, rewrite.RepoRel(m.Old)))
			case rewrite.None:
			}
		}
	}
	return aborts, warns, nil
}

// auditGoFiles walks every *.go file and classifies surviving tokens. A
// driver-rewritable form (import literal, runtime literal, split-segment run)
// aborts; a comment or informational-string occurrence warns.
func (d *driver) auditGoFiles() (aborts, warns []string, err error) {
	err = filepath.WalkDir(d.root, func(path string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if dirEntry.IsDir() {
			if d.skipWalkDir(path, dirEntry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(content)
		rel := d.relPath(path)
		for _, m := range d.moves {
			switch rewrite.ClassifyGo(text, m) {
			case rewrite.Abort:
				aborts = append(aborts, fmt.Sprintf("%s: %s", rel, rewrite.RepoRel(m.Old)))
			case rewrite.Warn:
				warns = append(warns, fmt.Sprintf("%s: %s", rel, rewrite.RepoRel(m.Old)))
			case rewrite.None:
			}
		}
		return nil
	})
	return aborts, warns, err
}

// auditProseFiles walks the non-Go prose files (markdown and YAML) and records
// any pre-move token as a non-fatal warning. The driver does not rewrite prose,
// and spec/ markdown is handled by the staged spec edits, so a stale prose
// reference is recorded for a manual sweep rather than aborting. The spec/ tree
// is excluded because its path edits land through implement-proposal, not this
// driver.
func (d *driver) auditProseFiles() ([]string, error) {
	var warns []string
	err := filepath.WalkDir(d.root, func(path string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if dirEntry.IsDir() {
			if d.skipWalkDir(path, dirEntry.Name()) || dirEntry.Name() == "spec" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isProseFile(path) {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(content)
		rel := d.relPath(path)
		for _, m := range d.moves {
			if rewrite.HasProseToken(text, m) {
				warns = append(warns, fmt.Sprintf("%s: %s", rel, rewrite.RepoRel(m.Old)))
			}
		}
		return nil
	})
	return warns, err
}

func isProseFile(path string) bool {
	switch filepath.Ext(path) {
	case ".md", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// relPath returns path relative to the repo root in slash form, falling back to
// the absolute path when the relativization fails.
func (d *driver) relPath(path string) string {
	rel, err := filepath.Rel(d.root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
