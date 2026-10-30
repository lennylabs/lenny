// SPDX-License-Identifier: MIT

package translator

import (
	"strconv"
	"strings"
)

// formatResponseID returns the Open Responses response id of the n-th turn
// on sessionID: the session id, a ".", and the 1-based ordinal.
//
// spec: §15 (Open Responses conversation sessions)
func formatResponseID(sessionID string, n int) string {
	return sessionID + "." + strconv.Itoa(n)
}

// parseResponseID splits a response id on its last "." into the session id
// and the 1-based ordinal. It reports ok == false for an id with no
// separator, an empty session-id prefix, or an ordinal that is not a decimal
// integer of at least 1, so the caller answers the native 404 before any
// store call. The prefix is not validated as a UUID: the tenant-scoped
// session-store read already treats a malformed id as not found, so a
// malformed, an unknown, and a cross-tenant prefix answer the same 404.
//
// spec: §15 (Open Responses conversation sessions); §4.2 (session-store
// tenant isolation)
func parseResponseID(id string) (sessionID string, n int, ok bool) {
	i := strings.LastIndexByte(id, '.')
	if i <= 0 {
		return "", 0, false
	}
	digits := id[i+1:]
	if digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 {
		return "", 0, false
	}
	return id[:i], n, true
}
