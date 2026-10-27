// SPDX-License-Identifier: MIT

package stackkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// UserBearer signs, through signer, a user_bearer for subject in tenantID
// carrying audience, roles, a fresh jti, and a ten-minute lifetime. The
// §13.3 audience rule refuses an exchange that requests an audience the
// subject does not already carry, so a test whose exchange names an
// audience passes that audience here; nil leaves the claim empty. Signed with the
// signer Signer returns for the stack's key, every process of the stack
// verifies it. It has no issued_tokens row, so step (1a) of the §13.3
// "Write-before-issue ordering" passes it as a subject_token, which an
// impersonation bearer no longer is.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation; Write-before-issue ordering), §10.2.
func UserBearer(t testing.TB, signer jwt.Signer, tenantID, subject string, audience []string, roles ...auth.Role) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("stackkey: user bearer jti: %v", err)
	}
	now := time.Now()
	tok, err := signer.Sign(context.Background(), jwt.Claims{
		Subject:    subject,
		Audience:   audience,
		TenantID:   tenantID,
		Roles:      roles,
		Typ:        auth.TokenUserBearer,
		CallerType: "human",
		IssuedAt:   now.Unix(),
		NotBefore:  now.Unix(),
		Expiry:     now.Add(10 * time.Minute).Unix(),
		JWTID:      "stack-user-" + hex.EncodeToString(b[:]),
	})
	if err != nil {
		t.Fatalf("stackkey: sign user bearer for %s/%s: %v", tenantID, subject, err)
	}
	return tok
}
