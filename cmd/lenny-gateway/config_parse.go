// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseKeyValueCSV splits a comma-separated key=value flag value into
// a map. Trimmed empty entries and entries without `=` are skipped.
// An empty input yields a nil map. The §27.2
// playground.sessionLabels flag uses this encoding so a Helm value
// like `{origin: playground, env: stage}` renders to
// `--playground-session-labels=origin=playground,env=stage`.
func parseKeyValueCSV(raw string) map[string]string {
	var out map[string]string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[k] = v
	}
	return out
}

// boolStr renders a bool as the lowercase string the §25.3
// platform-config endpoint surfaces.
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// envFlag returns true when the env var name is set to a truthy
// value (1, true, yes — case-insensitive). Used to default the
// --dev-mode flag from LENNY_DEV_MODE.
func envFlag(name string) bool {
	v := os.Getenv(name)
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// envFlagDefault returns true / false from the env var name, or def
// when the var is unset. Used for flags that default on (e.g., the
// §10.3 --jwks-publish endpoint) where envFlag's always-false-default
// semantics do not match the spec posture.
func envFlagDefault(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// envFloat returns the env var name parsed as a float64, or def when
// the var is unset or does not parse. Used to default the
// --billing-dual-control-threshold flag from the environment.
func envFloat(name string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// envOr returns the env var name, or def when the var is unset or
// empty. Used to default the §27.2 playground string flags.
func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// envInt returns the env var name parsed as an int, or def when the
// var is unset or does not parse. Used to default the §27.2
// playground integer flags.
func envInt(name string, def int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// envDuration mirrors envInt for time.Duration-valued flags. Accepts
// any value time.ParseDuration parses ("60s", "5m", "1h"); returns def
// on missing or unparseable env vars.
func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// envBool returns the env var parsed as a bool, or def when the var is
// unset or does not parse. Accepts the strconv.ParseBool truth values
// ("1", "true", "TRUE", "0", "false", ...).
func envBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// envInt64 mirrors envInt for int64-valued flags (idempotency body
// cap, size limits). Returns def on missing or unparseable env vars.
func envInt64(name string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// parseWindowOverrides parses the §25.3 recommendations window-override
// flag (comma-separated category=duration pairs, e.g.
// "warm_pool_sizing=12h,credential_pool_sizing=72h") into the map the
// recommendations.Config expects. Malformed pairs and unparseable
// durations are skipped so one bad entry does not drop the rest.
// spec: §25.3. F-25.3.12.
func parseWindowOverrides(raw string) map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, pair := range splitAndTrim(raw) {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || d <= 0 {
			continue
		}
		out[strings.TrimSpace(k)] = d
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitAndTrim splits a comma-separated string and drops empty entries
// after trimming whitespace. Used to parse the --redis-sentinel-addrs
// list.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
