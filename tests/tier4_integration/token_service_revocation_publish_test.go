//go:build integration

// SPDX-License-Identifier: MIT

package tier4_integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/tests/testinfra/opsprocess"
	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
	"github.com/lennylabs/lenny/tests/testinfra/tlsredis"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// rotationPropagationBound is how soon after a rotation lenny-ops must
// refuse the rotated token. It is far below the 30 s revocation set read,
// so only the Redis path meets it.
const rotationPropagationBound = 3 * time.Second

// rotationCallerClaims returns default-tenant user-bearer claims for a
// caller or subject of a token exchange.
func rotationCallerClaims(jti string) jwt.Claims {
	now := time.Now()
	return jwt.Claims{
		Subject:  "dave@acme.com",
		TenantID: auth.DefaultTenantID,
		Roles:    []auth.Role{auth.RoleTenantAdmin},
		Typ:      auth.TokenUserBearer,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(10 * time.Minute).Unix(),
		JWTID:    jti,
	}
}

// rotatedJTI returns the jti claim of a compact JWS.
func rotatedJTI(t *testing.T, tok string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var c struct {
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(raw, &c); err != nil || c.JTI == "" {
		t.Fatalf("payload carries no jti: %s (%v)", raw, err)
	}
	return c.JTI
}

// spec: 13.3 (Credential Flow) "Token rotation and revocation", 25.4 (The lenny-ops Service: Authentication, revocation set rule 2)
// diagnosis: no jti on the revocation channel after a rotation means the
// Token Service commits the revocation in Postgres but publishes nothing,
// so every gateway replica and lenny-ops learn of it only at their next
// 30-second read and keep admitting the rotated token until then. A jti
// on the channel with lenny-ops still admitting the token past the bound
// means lenny-ops does not apply the channel to its revocation set.
func TestTokenServiceRotationReachesPeersOverRedis_spec_13_3(t *testing.T) {
	tokensvc.SkipUnlessAvailable(t)
	refs := map[string]string{kms.AliasJWTSigning: "local-key-1"}
	_, signer := stackkey.Signer(t, stackkey.MasterKeyFile(t), refs)
	pg := startKeySetPostgres(t)
	rd := tlsredis.Start(t, "redis-secret")
	ts := tokensvc.StartWith(t, append(keySetTokenServiceArgs(pg.DSN, refs), "--redis-url="+rd.URL)...)
	ops := opsprocess.StartOpts(t, opsprocess.Options{
		Args: append(keySetOpsArgs(t, pg.DSN, refs), "--redis-url="+rd.URL),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sub := rd.Client.Subscribe(ctx, revocationprop.Channel)
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	sign := func(jti string) string {
		tok, err := signer.Sign(ctx, rotationCallerClaims(jti))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return tok
	}
	tok := mintExchanged(t, ts.BaseURL(), sign("rotation-caller"), sign("rotation-subject"), "")
	// The exchanged token carries no roles claim, so lenny-ops admits it
	// through authentication and the revocation set and its role gate
	// answers 403 (opsprocess.Outcome.Authenticated).
	ops.WaitAuthenticated(t, tok, 30*time.Second)

	// A self-rotation revokes tok in issued_tokens (§13.3).
	mintExchanged(t, ts.BaseURL(), tok, tok, "")
	select {
	case msg := <-sub.Channel():
		if want := rotatedJTI(t, tok); msg.Payload != want {
			t.Fatalf("the rotation published %q, want the rotated jti %q", msg.Payload, want)
		}
	case <-time.After(rotationPropagationBound):
		t.Fatalf("the rotation published no jti on %s within %s\n%s", revocationprop.Channel, rotationPropagationBound, ts.Logs())
	}
	ops.WaitRefused(t, tok, http.StatusUnauthorized, opsprocess.CodeRevoked, rotationPropagationBound)
}
