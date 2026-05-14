// SPDX-License-Identifier: MIT

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Reason     string `json:"reason,omitempty"`
	Total      int    `json:"total,omitempty"`
	Passed     int    `json:"passed,omitempty"`
	Failed     int    `json:"failed,omitempty"`
	Skipped    int    `json:"skipped,omitempty"`
	Detail     string `json:"detail,omitempty"`
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
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
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
