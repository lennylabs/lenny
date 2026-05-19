// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"
	"runtime"
	"sort"
)

// PlatformVersion is the §25.3 GET /v1/admin/platform/version
// response — the gateway's own compiled-in metadata. Component
// versions that need a K8s / Postgres query are aggregated by
// lenny-ops (§25.8), not here.
//
// OpsServiceURL is the §25.14 lenny-ctl auto-discovery field: the
// public URL of the lenny-ops service (configured via the
// ops.ingress.host Helm value). lenny-ctl reads it on first use so an
// operator does not have to configure the gateway URL and the ops URL
// separately. It is omitted when the deployment did not configure an
// ops Ingress.
type PlatformVersion struct {
	GatewayVersion string `json:"gatewayVersion"`
	GitCommit      string `json:"gitCommit"`
	BuildDate      string `json:"buildDate"`
	GoVersion      string `json:"goVersion"`
	OpsServiceURL  string `json:"opsServiceURL,omitempty"`
}

// PlatformInfo carries the build metadata the gateway was compiled
// with. cmd/lenny-gateway populates it from -ldflags; the zero
// value is acceptable (the fields default to "dev"/"unknown").
type PlatformInfo struct {
	Version   string
	GitCommit string
	BuildDate string

	// OpsServiceURL is the §25.14 public lenny-ops URL, configured via
	// the ops.ingress.host Helm value. Empty when no ops Ingress is
	// configured.
	OpsServiceURL string
}

// PlatformConfigEntry is one effective-config key/value. Secret
// values are pre-redacted by the caller; the handler does not see
// raw secrets.
type PlatformConfigEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// WithPlatformInfo wires the §25.3 platform version + config
// endpoints onto the Router. configEntries is the effective merged
// configuration with every secret value already redacted to "***".
func (r *Router) WithPlatformInfo(info PlatformInfo, configEntries map[string]string) *Router {
	r.platformInfo = info
	r.platformConfig = configEntries
	r.platformWired = true
	return r
}

func (r *Router) handlePlatformVersion(w http.ResponseWriter, _ *http.Request) {
	version := r.platformInfo.Version
	if version == "" {
		version = "dev"
	}
	commit := r.platformInfo.GitCommit
	if commit == "" {
		commit = "unknown"
	}
	build := r.platformInfo.BuildDate
	if build == "" {
		build = "unknown"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PlatformVersion{
		GatewayVersion: version,
		GitCommit:      commit,
		BuildDate:      build,
		GoVersion:      runtime.Version(),
		OpsServiceURL:  r.platformInfo.OpsServiceURL,
	})
}

func (r *Router) handlePlatformConfig(w http.ResponseWriter, _ *http.Request) {
	entries := make([]PlatformConfigEntry, 0, len(r.platformConfig))
	for k, v := range r.platformConfig {
		entries = append(entries, PlatformConfigEntry{Key: k, Value: redactSecret(k, v)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"config": entries})
}

// redactSecret returns "***" when the key names a secret-bearing
// config field per §25.3 ("Secret values are redacted to ***").
// Defensive belt-and-suspenders even though WithPlatformInfo
// callers are expected to pre-redact.
func redactSecret(key, value string) string {
	lk := lowerASCII(key)
	for _, marker := range []string{"secret", "password", "token", "key", "credential"} {
		if containsASCII(lk, marker) {
			return "***"
		}
	}
	return value
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func containsASCII(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
