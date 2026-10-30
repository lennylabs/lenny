// SPDX-License-Identifier: MIT

package mcptools

import "github.com/lennylabs/lenny/pkg/api/v1/session"

// randomSessionID returns a fresh §12.6 UUIDv8 session identifier.
func randomSessionID() string {
	return session.NewID()
}
