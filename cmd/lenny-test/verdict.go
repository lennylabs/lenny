// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// verdict is the §7 JSON shape, simplified for Phase 0.
type verdict struct {
	Version    int                 `json:"version"`
	RunID      string              `json:"run_id"`
	StartedAt  string              `json:"started_at"`
	FinishedAt string              `json:"finished_at"`
	DurationMS int64               `json:"duration_ms"`
	Command    string              `json:"command"`
	Trigger    trigger             `json:"trigger"`
	Infra      infrastructureInfo  `json:"infrastructure"`
	Tiers      map[string]tierStat `json:"tiers"`
	Verdict    string              `json:"verdict"`
	NextAction string              `json:"next_action,omitempty"`
	SpecStatus map[string]string   `json:"spec_section_status,omitempty"`
	startedAt  time.Time
	finishedAt time.Time
}

type trigger struct {
	Mode             string   `json:"mode"`
	GitRevision      string   `json:"git_revision,omitempty"`
	ChangedPaths     []string `json:"changed_paths,omitempty"`
	ResolvedPackages []string `json:"resolved_packages,omitempty"`
	ResolvedSpecs    []string `json:"resolved_specs,omitempty"`
}

type infrastructureInfo struct {
	ComposeProfile string `json:"compose_profile,omitempty"`
	KindCluster    string `json:"kind_cluster,omitempty"`
	ContainerCache string `json:"container_cache,omitempty"`
}

type tierStat struct {
	Status     string         `json:"status"`
	DurationMS int64          `json:"duration_ms"`
	Reason     string         `json:"reason,omitempty"`
	Total      int            `json:"total,omitempty"`
	Passed     int            `json:"passed,omitempty"`
	Failed     int            `json:"failed,omitempty"`
	Skipped    int            `json:"skipped,omitempty"`
	Cached     int            `json:"cached,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	Failures   []failureEntry `json:"failures,omitempty"`
}

// failureEntry captures one failed test with the §7 structure: a
// PR comment links to it, a CI dashboard groups by spec_sections,
// the rerun_command is copy-paste-ready.
type failureEntry struct {
	Test         string   `json:"test"`
	Package      string   `json:"package,omitempty"`
	File         string   `json:"file,omitempty"`
	Line         int      `json:"line,omitempty"`
	SpecSections []string `json:"spec_sections,omitempty"`
	Diagnosis    string   `json:"diagnosis,omitempty"`
	Error        string   `json:"error,omitempty"`
	DurationMS   int64    `json:"duration_ms,omitempty"`
	StdoutTail   string   `json:"stdout_tail,omitempty"`
	RerunCommand string   `json:"rerun_command,omitempty"`
}

func newVerdict(s selector) *verdict {
	now := time.Now().UTC()
	v := &verdict{
		Version:    1,
		RunID:      newRunID(),
		StartedAt:  now.Format(time.RFC3339Nano),
		Command:    "lenny-test " + describe(s),
		Trigger:    trigger{Mode: triggerMode(s)},
		Infra:      infrastructureInfo{ContainerCache: "cold"},
		Tiers:      map[string]tierStat{},
		Verdict:    "PASS",
		SpecStatus: map[string]string{},
		startedAt:  now,
	}
	if s.cached {
		v.Infra.ContainerCache = "warm"
	}
	return v
}

func triggerMode(s selector) string {
	switch {
	case s.group != "":
		return "group:" + s.group
	case s.tier != "":
		return "tier:" + s.tier
	case s.maxTier != "":
		return "max_tier:" + s.maxTier
	case s.changed:
		return "changed"
	case len(s.specs) > 0:
		return "spec"
	case len(s.pkgs) > 0:
		return "pkg"
	}
	return "unknown"
}

func (v *verdict) recordTier(name, status string, dur time.Duration, detail string) {
	v.Tiers[name] = tierStat{
		Status:     status,
		DurationMS: dur.Milliseconds(),
		Reason:     reasonFromStatus(status, detail),
		Detail:     detail,
	}
	if status == "fail" {
		v.Verdict = "FAIL"
	}
}

func reasonFromStatus(status, detail string) string {
	if status == "skipped" {
		if detail != "" {
			return detail
		}
		return "skipped"
	}
	return ""
}

func (v *verdict) next(action string) {
	v.NextAction = action
}

// synthesizeNextAction derives a §7-style next_action sentence from
// the recorded failures when present. Format:
//
//	"Fix N <tier>-tier failure(s) in <packages>. See spec sections <ids>."
//
// Falls back to the generic message when no Failures are recorded
// (the harness populates Failures only when go test -json output is
// parsed; incremental work).
func (v *verdict) synthesizeNextAction(tierName, fallback string) string {
	t, ok := v.Tiers[tierName]
	if !ok || len(t.Failures) == 0 {
		return fallback
	}
	pkgs := map[string]bool{}
	specs := map[string]bool{}
	for _, f := range t.Failures {
		if f.Package != "" {
			pkgs[f.Package] = true
		}
		for _, s := range f.SpecSections {
			specs[s] = true
		}
	}
	pkgList := setToSortedSlice(pkgs)
	specList := setToSortedSlice(specs)
	msg := fmt.Sprintf("Fix %d %s-tier failure(s)", len(t.Failures), tierName)
	if len(pkgList) > 0 {
		msg += " in " + strings.Join(pkgList, ", ")
	}
	if len(specList) > 0 {
		msg += ". See spec sections " + strings.Join(specList, ", ")
	}
	return msg + "."
}

func setToSortedSlice(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (v *verdict) finalize() {
	v.finishedAt = time.Now().UTC()
	v.FinishedAt = v.finishedAt.Format(time.RFC3339Nano)
	v.DurationMS = v.finishedAt.Sub(v.startedAt).Milliseconds()
}

func (v *verdict) write(path string) error {
	v.finalize()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Rotate: write verdict-<run_id>.json alongside latest.json so
	// recent runs survive. The latest.json file is overwritten with
	// the same content for tools that consume the canonical path.
	dir := filepath.Dir(path)
	rotated := filepath.Join(dir, "verdict-"+v.RunID+".json")
	payload, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	for _, p := range []string{path, rotated} {
		if err := writeFileAtomic(p, payload, 0o644); err != nil {
			return err
		}
	}
	// Bound the on-disk history: keep the 20 most recent rotated
	// files; older verdicts are removed.
	pruneOldVerdicts(dir, 20)
	return nil
}

// writeFileAtomic writes data to a temp file in the same directory,
// fsyncs it, then renames into place. A reader of `path` sees either
// the prior verdict or the new one — never a truncated half-write.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// pruneOldVerdicts deletes all but the `keep` most recent
// verdict-<id>.json files in dir. Errors are ignored — rotation is
// best-effort.
func pruneOldVerdicts(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type rotated struct {
		Path    string
		ModTime time.Time
	}
	files := []rotated{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "verdict-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, rotated{Path: filepath.Join(dir, name), ModTime: info.ModTime()})
	}
	if len(files) <= keep {
		return
	}
	// Sort by mtime descending; drop everything past `keep`.
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			if files[j].ModTime.After(files[i].ModTime) {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
	for _, f := range files[keep:] {
		_ = os.Remove(f.Path)
	}
}

func (v *verdict) json() string {
	v.finalize()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(b)
}

func newRunID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return time.Now().UTC().Format("20060102T150405Z")
	}
	return hex.EncodeToString(b)
}
