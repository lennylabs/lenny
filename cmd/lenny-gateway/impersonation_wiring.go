// SPDX-License-Identifier: MIT

package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditscope"
	"github.com/lennylabs/lenny/pkg/gateway/audit/auditstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// impersonationCollaborators are the gateway objects the §13.3
// impersonation service runs over: the Postgres pool that holds
// issued_tokens, the bearer signer, the revocation propagator the end
// pushes the jti through, the durable audit store that emits
// cross_tenant_read, and the tenant-scope validator the started and ended
// events are appended through.
type impersonationCollaborators struct {
	pool     *pgxpool.Pool
	signer   *jwt.BreakerSigner
	cache    *revocationprop.Propagator
	audit    *auditstore.Store
	appender *auditscope.Validator
}

// complete reports whether every collaborator is present.
func (c impersonationCollaborators) complete() bool {
	return c.pool != nil && c.signer != nil && c.cache != nil && c.audit != nil && c.appender != nil
}

// newImpersonationService returns the impersonation service over c, or an
// untyped nil when any collaborator is absent. A gateway without one of
// them registers no impersonation route rather than minting a bearer
// nobody can revoke. The result is an interface so that the absent case is
// a true nil: a typed-nil *impersonation.Service would pass the admin
// router's nil check and register routes that panic on first use.
//
// spec: §13.3 (Credential Flow: Platform-admin impersonation), §16.7, §12.3.
func newImpersonationService(c impersonationCollaborators, cfg impersonation.Config) admin.ImpersonationService {
	if !c.complete() {
		return nil
	}
	return impersonation.New(issuedtokenstore.New(c.pool), c.cache, c.audit, c.appender, c.signer, cfg)
}
