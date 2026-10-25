// SPDX-License-Identifier: MIT

package superviseprobe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ProcessToken returns "PID:STARTTIME" for the process whose /proc/<pid>/stat
// content is stat. The start time is field 22 of stat, counted in clock
// ticks since boot, so the pair names one process even across PID reuse.
func ProcessToken(pid int, stat string) (string, error) {
	start, err := statStartTime(stat)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(pid) + ":" + start, nil
}

// statStartTime returns field 22 of a /proc/<pid>/stat line. The comm field
// (2) is parenthesized and may hold spaces, so fields are counted from the
// last closing parenthesis.
func statStartTime(stat string) (string, error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return "", fmt.Errorf("stat has no comm field: %q", stat)
	}
	// The fields after comm start at field 3 (state); start time is field 22.
	fields := strings.Fields(stat[i+1:])
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex {
		return "", fmt.Errorf("stat has %d fields after comm, want more than %d", len(fields), startTimeIndex)
	}
	return fields[startTimeIndex], nil
}

// selfToken is the calling process's token.
func selfToken() (string, error) {
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return "", fmt.Errorf("read /proc/self/stat: %w", err)
	}
	return ProcessToken(os.Getpid(), string(stat))
}

// manifestPath is the adapter manifest path the SDK reads.
func manifestPath() string {
	if p := strings.TrimSpace(os.Getenv(manifestEnvVar)); p != "" {
		return p
	}
	return defaultManifestPath
}

// ReadManifestView reads the manifest at path and reduces it to a
// ManifestView. A missing or unparseable file is a view with Present false.
func ReadManifestView(path string) ManifestView {
	data, err := os.ReadFile(path)
	if err != nil {
		return ManifestView{Error: err.Error()}
	}
	var m struct {
		MCPNonce          string `json:"mcpNonce"`
		PlatformMcpServer *struct {
			Socket string `json:"socket"`
		} `json:"platformMcpServer"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return ManifestView{Error: err.Error()}
	}
	return ManifestView{
		Present:     true,
		NonceDigest: nonceDigest(m.MCPNonce),
		PlatformMCP: m.PlatformMcpServer != nil && m.PlatformMcpServer.Socket != "",
	}
}

// nonceDigest is the first 16 hex digits of the SHA-256 of nonce, or empty
// for an empty nonce.
func nonceDigest(nonce string) string {
	if nonce == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:8])
}

// ParseDialDelay parses the DialDelayEnvVar value. Empty is no delay; a
// negative or unparseable value is an error, so a misconfigured pool fails
// visibly rather than racing the warm-up wait.
func ParseDialDelay(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: %w", DialDelayEnvVar, v, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s=%q is negative", DialDelayEnvVar, v)
	}
	return d, nil
}
