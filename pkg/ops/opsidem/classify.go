// SPDX-License-Identifier: MIT

package opsidem

import (
	"encoding/json"
	"net/http"
)

// longRunningPaths are the §25.4 multi-phase operations that use the 7d
// long-running TTL class because the agent may pause between steps.
//
// spec: §25.4 lines 2024-2027.
var longRunningPaths = map[string]bool{
	"/v1/admin/platform/upgrade/start":    true,
	"/v1/admin/platform/upgrade/proceed":  true,
	"/v1/admin/platform/upgrade/pause":    true,
	"/v1/admin/platform/upgrade/rollback": true,
	"/v1/admin/restore/execute":           true,
}

// classify returns whether the endpoint requires an idempotency key at
// Tier 2/3 and its §25.4 TTL class. The required set is the non-convergent
// operations §25.4 lines 2031-2035 enumerate; POST /v1/admin/backups is
// required only for a full backup (body type:"full").
//
// spec: §25.4 lines 2024-2035.
func classify(method, path string, body []byte) (required bool, class TTLClass) {
	if longRunningPaths[path] {
		class = ClassLongRunning
	}
	if method != http.MethodPost {
		return false, class
	}
	switch path {
	case "/v1/admin/platform/upgrade/start", "/v1/admin/restore/execute":
		return true, class
	case "/v1/admin/backups":
		return bodyBackupType(body) == "full", class
	}
	return false, class
}

// bodyBackupType extracts the "type" field from a §25.11 backup request
// body without consuming the reader (the caller buffered the bytes). A
// malformed or empty body yields "".
func bodyBackupType(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var b struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return ""
	}
	return b.Type
}
