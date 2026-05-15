// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// §7 tier-status values. Every tierStat.Status field carries one
// of these, and the verdict-level Verdict carries one of the
// corresponding PASS/FAIL/INCONCLUSIVE summary strings. Treating
// them as constants keeps callsites consistent and gives IDEs
// a real symbol to rename through.
const (
	statusPass         = "pass"
	statusFail         = "fail"
	statusSkipped      = "skipped"
	statusInconclusive = "inconclusive"
	statusNotSelected  = "not-selected"

	verdictPASS         = "PASS"
	verdictFAIL         = "FAIL"
	verdictINCONCLUSIVE = "INCONCLUSIVE"
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
		Verdict:    verdictPASS,
		SpecStatus: map[string]string{},
		startedAt:  now,
	}
	if s.cached {
		v.Infra.ContainerCache = "warm"
	}
	if sha := currentGitRevision(); sha != "" {
		v.Trigger.GitRevision = sha
	}
	if s.changed {
		v.Trigger.ChangedPaths = currentChangedPaths(s.since)
	}
	if len(s.specs) > 0 {
		v.Trigger.ResolvedSpecs = append([]string{}, s.specs...)
	}
	if len(s.pkgs) > 0 {
		v.Trigger.ResolvedPackages = append([]string{}, s.pkgs...)
	}
	return v
}

// currentGitRevision returns the HEAD SHA, or "" when git is
// unavailable or the working directory is not a checkout. The
// verdict omits the field rather than carrying an obviously wrong
// value.
func currentGitRevision() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// currentChangedPaths returns the union of files reported by
// `git status --porcelain` (uncommitted changes) and, when `since`
// is set, `git diff --name-only <since>...HEAD`. Returns nil when
// git is not available.
func currentChangedPaths(since string) []string {
	root := repoRoot()
	seen := map[string]bool{}
	collect := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// porcelain output prefixes XY status; --name-only does not.
			if len(line) > 3 && (line[2] == ' ' || line[1] == ' ') {
				line = strings.TrimSpace(line[2:])
			}
			seen[line] = true
		}
	}
	collect("status", "--porcelain")
	if since != "" {
		collect("diff", "--name-only", since+"...HEAD")
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
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

// recordTierWithResult is the §7 counterpart of recordTier: it
// stores per-tier total/passed/failed/skipped counts and the
// per-test failure entries that came out of `go test -json`. Status
// reclassification (FAIL → INCONCLUSIVE on infra-class detail) and
// the overall verdict update happen in recordTier; this method
// delegates to it for that bookkeeping.
func (v *verdict) recordTierWithResult(name, status string, dur time.Duration, detail string, r *tierResult) {
	v.recordTier(name, status, dur, detail)
	if r == nil {
		return
	}
	t := v.Tiers[name]
	t.Total = r.Total
	t.Passed = r.Passed
	t.Failed = r.Failed
	t.Skipped = r.Skipped
	if len(r.Failures) > 0 {
		t.Failures = r.Failures
	}
	v.Tiers[name] = t
	// Promote per-failure spec sections to the verdict-level
	// spec_section_status map per §7: each section with at least
	// one failing test is reported FAIL.
	for _, f := range r.Failures {
		for _, s := range f.SpecSections {
			v.SpecStatus[s] = verdictFAIL
		}
	}
}

func (v *verdict) recordTier(name, status string, dur time.Duration, detail string) {
	// Reclassify a "fail" whose detail looks like an infrastructure
	// blow-up into "inconclusive" per §7 + §21.3. A genuine test
	// failure stays FAIL; a docker-daemon crash or a kind bring-up
	// timeout does not.
	if status == statusFail && classifyInfraFailure(detail) {
		status = statusInconclusive
	}
	v.Tiers[name] = tierStat{
		Status:     status,
		DurationMS: dur.Milliseconds(),
		Reason:     reasonFromStatus(status, detail),
		Detail:     detail,
	}
	switch status {
	case statusFail:
		v.Verdict = verdictFAIL
	case statusInconclusive:
		// FAIL outranks INCONCLUSIVE; a real failure earlier in the
		// run stays surfaced.
		if v.Verdict != verdictFAIL {
			v.Verdict = verdictINCONCLUSIVE
		}
	}
}

func reasonFromStatus(status, detail string) string {
	switch status {
	case statusSkipped:
		if detail != "" {
			return detail
		}
		return statusSkipped
	case statusInconclusive:
		if detail != "" {
			return detail
		}
		return "infrastructure failure"
	}
	return ""
}

// classifyInfraFailure reports whether `detail` describes a known
// infrastructure-class failure rather than a test failure. The list
// is conservative: matches against the lower-cased detail string so
// known patterns (compose down, docker daemon, kind cluster, cloud
// auth, image pull, container exec, network unreachable) reclassify
// as INCONCLUSIVE. Unknown failures stay FAIL.
func classifyInfraFailure(detail string) bool {
	if detail == "" {
		return false
	}
	d := strings.ToLower(detail)
	patterns := []string{
		"docker daemon",
		"docker compose: services did not reach healthy",
		"compose up:",
		"compose down:",
		"kind cluster",
		"kind: failed to create cluster",
		"kubeconfig",
		"could not reach the apiserver",
		"context deadline exceeded waiting for",
		"image pull",
		"unable to authenticate",
		"failed to acquire cloud credentials",
		"network unreachable",
		"no route to host",
		"connection refused",
		"container exec",
		"container exited",
	}
	for _, p := range patterns {
		if strings.Contains(d, p) {
			return true
		}
	}
	return false
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
	v.fillNotSelected()
}

// fillNotSelected emits a "not-selected" tier entry for every
// canonical tier the run did not include. §7 enumerates four tier
// statuses (pass, fail, skipped, not-selected); downstream tooling
// can distinguish "ran and passed" from "did not run" only when the
// map contains every tier.
func (v *verdict) fillNotSelected() {
	for _, name := range []string{
		tierStatic, tierUnit, tierComponent, tierContract, tierIntegration,
		tierE2EKind, tierE2ECloud, tierLoad, tierChaos, tierSecurity,
		tierConformance, tierDocs,
	} {
		if _, already := v.Tiers[name]; already {
			continue
		}
		v.Tiers[name] = tierStat{
			Status: statusNotSelected,
			Reason: "outside the resolved selector",
		}
	}
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
	pruneOldVerdicts(dir, verdictRotationDepth)
	// Append a one-line summary to history.jsonl per §21.2 so the
	// flake root-cause analyzer has a stable, append-only source.
	if err := appendHistory(filepath.Join(dir, "history.jsonl"), v); err != nil {
		// Non-fatal: history is observational, not gating.
		_, _ = fmt.Fprintf(os.Stderr, "lenny-test: append history: %v\n", err)
	}
	return nil
}

// appendHistory adds a one-line JSON record summarizing v to the
// history file. Format mirrors §21.2: just enough fields to feed
// the root-cause heuristics (run_id, started_at, finished_at,
// verdict, trigger.mode, per-tier status). The file is open-append;
// concurrent writers race only on the os.Write boundary, which is
// atomic for sub-PIPE_BUF writes on POSIX.
func appendHistory(path string, v *verdict) error {
	tierStatus := map[string]string{}
	for name, t := range v.Tiers {
		tierStatus[name] = t.Status
	}
	rec := map[string]any{
		"run_id":       v.RunID,
		"started_at":   v.StartedAt,
		"finished_at":  v.FinishedAt,
		"duration_ms":  v.DurationMS,
		"verdict":      v.Verdict,
		"trigger_mode": v.Trigger.Mode,
		"tiers":        tierStatus,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return err
	}
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
