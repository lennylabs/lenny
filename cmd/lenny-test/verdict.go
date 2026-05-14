// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// failureEntry captures one failed test with enough structure for a
// PR comment to link to it.
type failureEntry struct {
	Test     string `json:"test"`
	Package  string `json:"package,omitempty"`
	Location string `json:"location,omitempty"`
	Message  string `json:"message,omitempty"`
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
	for _, p := range []string{path, rotated} {
		f, err := os.Create(p)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			_ = f.Close()
			return err
		}
		_ = f.Close()
	}
	// Bound the on-disk history: keep the 20 most recent rotated
	// files; older verdicts are removed.
	pruneOldVerdicts(dir, 20)
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
