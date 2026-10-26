// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the reader-facing pages that mirror the
// shared ES256 signing key: the KMS providers and the reserved signing
// aliases, the capability token lifetime, the Token Service's refusal of a
// playground caller token, the lenny-ops revocation set during a Postgres
// outage, the KMS_SIGNING_UNAVAILABLE catalog row, the audit-lock remedy,
// and the embedded-mode store list. Each case pins a sentence or table row
// that the specification fixes and names the retired wording it replaced,
// so a page that drifts back to the per-process HMAC signer, the Vault
// Transit provider, or the removed lifetime keys fails. No external
// infrastructure.

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// docPageCheck is one expectation on a documentation page. When row is
// set, the substrings are matched against the single line that contains
// row (a table row or a one-line paragraph); otherwise against the whole
// page.
type docPageCheck struct {
	page       string
	row        string
	contains   []string
	notContain []string
}

// uniqueLineContaining returns the one line of body that contains needle,
// and fails when no line or more than one line does.
func uniqueLineContaining(t *testing.T, page, body, needle string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, needle) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("docs/%s: want exactly one line containing %q, found %d", page, needle, len(found))
	}
	return found[0]
}

// runDocPageChecks applies each check as a subtest named for its page and
// row.
func runDocPageChecks(t *testing.T, checks []docPageCheck) {
	t.Helper()
	for _, c := range checks {
		t.Run(c.page+"/"+c.row, func(t *testing.T) {
			text := readDocPage(t, filepath.Join(repoRoot(t), "docs", c.page))
			if c.row != "" {
				text = uniqueLineContaining(t, c.page, text, c.row)
			}
			for _, want := range c.contains {
				if !strings.Contains(text, want) {
					t.Errorf("docs/%s %q: missing %q", c.page, c.row, want)
				}
			}
			for _, bad := range c.notContain {
				if strings.Contains(text, bad) {
					t.Errorf("docs/%s %q: still carries retired wording %q", c.page, c.row, bad)
				}
			}
		})
	}
}

// TestReaderDocsStateSharedES256SigningKey_spec_10_2 asserts the
// configuration reference, the security guide, the architecture overview,
// and the glossary describe one ES256 key on AWS, GCP, or Azure (local in
// development), the reserved signing aliases, the gateway's in-process
// mints, the KMS grants of each ServiceAccount, and the automatic signing
// breaker on each signer process.
//
// diagnosis: A failure means a reader-facing page still names the retired
// Vault Transit provider or provider spellings, omits the
// platform:jwt-signing alias or the immutable key-version rule, describes
// the Token Service as the sole minter apart from the bootstrap admin
// credential, omits a ServiceAccount's KMS grant, or places every
// automatic breaker inside the gateway binary.
//
// spec: 10.2 (Session capability context, Signing topology, Verifier key
// set and rotation, KMS signing failure mode), 4.3 (Token Service role
// item 3), 11.6 (Circuit breakers), 13.2.4 (Gateway row KMS endpoint)
func TestReaderDocsStateSharedES256SigningKey_spec_10_2(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "reference/configuration.md",
			row:        "| `kms.provider` |",
			contains:   []string{"`aws`", "`gcp`", "`azure`", "`local`", "production"},
			notContain: []string{"vault", "aws-kms", "gcp-kms", "azure-keyvault"},
		},
		{
			page:     "reference/configuration.md",
			row:      "| `kms.aliases` |",
			contains: []string{"`platform:jwt-signing`", "`platform:jwt-verify`", "immutable key version", "/cryptoKeyVersions/<n>", "@<version>"},
		},
		{
			page:     "reference/configuration.md",
			row:      "| `kms.signQuotaPerSecond` |",
			contains: []string{"Required when `kms.provider` is not `local`", "KMSSignQuotaApproaching"},
		},
		{
			page:     "reference/configuration.md",
			row:      "| `kms.endpointCIDR` |",
			contains: []string{"`0.0.0.0/0` or `::/0`", "private KMS endpoint"},
		},
		{
			page:       "reference/configuration.md",
			row:        "| `playground.devTenantId` |",
			contains:   []string{"dev-mode playground JWT"},
			notContain: []string{"HMAC"},
		},
		{
			page:       "operator-guide/security.md",
			contains:   []string{"| Azure Key Vault | JWT signing", "ES256", "`ECC_NIST_P256`", "`EC_SIGN_P256_SHA256`"},
			notContain: []string{"Vault Transit"},
		},
		{
			page:       "operator-guide/security.md",
			row:        "3. **Bearer tokens**",
			contains:   []string{"platform-admin impersonation bearers", "web playground bearers", "session-capability tokens of the session-creation path"},
			notContain: []string{"session tokens", "is the exception"},
		},
		{
			page:     "operator-guide/security.md",
			row:      "| `lenny-gateway` |",
			contains: []string{"`Sign` and `GetPublicKey`"},
		},
		{
			page:     "operator-guide/security.md",
			row:      "| `lenny-token-service` |",
			contains: []string{"`Sign` and `GetPublicKey`"},
		},
		{
			page:     "operator-guide/security.md",
			row:      "| `lenny-ops-sa` |",
			contains: []string{"`GetPublicKey`", "no `Sign`"},
		},
		{
			page:       "getting-started/architecture.md",
			row:        "Externally, token issuance",
			contains:   []string{"platform-admin impersonation bearers", "web playground bearers", "same ES256 key"},
			notContain: []string{"is the exception"},
		},
		{
			page:       "reference/glossary.md",
			row:        "An automatic or operator-managed mechanism",
			contains:   []string{"KMS signing breakers of the gateway and the Token Service"},
			notContain: []string{"within the gateway binary"},
		},
	})
}

// TestReaderDocsStateCapabilityTokenLifetimeAndCallerRefusal_spec_13_3
// asserts the lifetime knob replaced playground.bearerTtlSeconds and the
// tokenExchange lifetime rows, the token exchange paragraph limits the
// default to refreshable tokens and exempts an actor_token delegation
// child minted without expires_in, the token exchange example returns the
// 900-second default, the admin API documents the playground caller
// refusal, and the playground guide states the subject_token residual.
//
// diagnosis: A failure means a page still documents a lifetime key the
// chart no longer reads, the configuration reference applies the default
// to a delegation child minted through actor_token (whose lifetime is
// capped by the subject and actor expiries and the per-dialect cap
// instead), an example response contradicts the 900-second
// default, the admin API omits the 401 invalid_client refusal of a
// playground caller token, or the playground guide implies that logout
// revokes a child token exchanged from a playground bearer.
//
// spec: 13.3 (Client authentication on /v1/oauth/token, RFC 8693
// parameter mapping expires_in row), 10.2 (JWT claims expiry), 27.3.1
// (OIDC cookie-to-MCP-bearer exchange)
func TestReaderDocsStateCapabilityTokenLifetimeAndCallerRefusal_spec_13_3(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:     "reference/configuration.md",
			row:      "| `auth.capabilityTokenTtlSeconds` |",
			contains: []string{"| int | 900 |", "`POST /v1/playground/token`", "without `expires_in`", "`120 <= ttl <= 3600`"},
		},
		{
			page:       "reference/configuration.md",
			notContain: []string{"bearerTtlSeconds", "tokenExchange.defaultTokenTTLSeconds", "tokenExchange.maxTokenTTLSeconds"},
		},
		{
			page: "reference/configuration.md",
			row:  "issues a JWT whose holder obtains a new token before expiry",
			contains: []string{
				"A rotation or scope-narrowing request that carries no `expires_in`",
				"A delegation child token minted with an `actor_token` and no `expires_in` does not take the `auth.capabilityTokenTtlSeconds` default",
				"the earliest of the subject token's expiry, the actor token's expiry, and the per-dialect cap",
				"Tokens whose holder has no refresh path do not take the default.",
			},
			notContain: []string{"A JWT issued from a request that carries no `expires_in` receives the default lifetime"},
		},
		{
			page:       "client-guide/authentication.md",
			contains:   []string{`"expires_in": 900`},
			notContain: []string{`"expires_in": 3600`},
		},
		{
			page:     "api/admin.md",
			row:      "The caller token (the `Authorization` bearer",
			contains: []string{`origin: "playground"`, "`401 invalid_client`", "`subject_token`"},
		},
		{
			page:       "operator-guide/web-playground.md",
			contains:   []string{"capabilityTokenTtlSeconds: 900", "Logout does not revoke that child token."},
			notContain: []string{"bearerTtlSeconds"},
		},
	})
}

// TestEmbeddedModeDocsListNoInMemoryKMS_spec_17_4 asserts the quickstart,
// the installation guide, and the local-development guide no longer list
// key management among the in-memory stores of lenny up, and keep the
// statement that no separate KMS process runs.
//
// diagnosis: A failure means a page still tells a reader that lenny up
// holds its KMS in memory, which contradicts the local provider keyed by
// the persisted master key Secret, or dropped the no-separate-process
// clause.
//
// spec: 17.4 (Embedded Mode dependency table KMS row, State and resets)
func TestEmbeddedModeDocsListNoInMemoryKMS_spec_17_4(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "getting-started/quickstart.md",
			row:        "The application data stores (",
			contains:   []string{"(Postgres and Redis)", "master key Secret"},
			notContain: []string{"KMS)"},
		},
		{
			page:       "operator-guide/installation.md",
			row:        "The application data stores (",
			contains:   []string{"no separate Postgres, Redis, KMS, or identity-provider process", "master key Secret"},
			notContain: []string{"key management,"},
		},
		{
			page:       "runtime-author-guide/local-development.md",
			row:        "The application data stores (",
			contains:   []string{"no separate Postgres, Redis, KMS, or identity-provider process", "master key Secret"},
			notContain: []string{"key management,"},
		},
		{
			page:       "runtime-author-guide/local-development.md",
			row:        "| **`lenny up`** (recommended) |",
			notContain: []string{"key management"},
		},
	})
}

// TestOperabilityDocsStateRevocationSetDuringPostgresOutage_spec_25_4
// asserts the agent-operability failure table states how lenny-ops treats
// Lenny-issued tokens while Postgres is down.
//
// diagnosis: A failure means the "Postgres down" or "Postgres + Redis both
// down" row no longer tells an operator that lenny-ops keeps accepting
// unrevoked tokens from its last read, refuses every token before its
// first read, and refuses playground bearers.
//
// spec: 25.4 (Authentication Revocation set rules 4, 5, and 7), 25.15
// (Postgres down and Postgres + Redis both down rows)
func TestOperabilityDocsStateRevocationSetDuringPostgresOutage_spec_25_4(t *testing.T) {
	want := []string{"in-memory revocation set", "has not yet completed a read refuses every Lenny-issued token", "Web playground bearers are refused"}
	runDocPageChecks(t, []docPageCheck{
		{page: "operator-guide/agent-operability.md", row: "| Postgres down |", contains: want},
		{page: "operator-guide/agent-operability.md", row: "| Postgres + Redis both down |", contains: want},
	})
}

// TestErrorCatalogKMSSigningUnavailableRow_spec_15 asserts the error
// catalog row names both signers, the throttling cause, and the
// Retry-After client action.
//
// diagnosis: A failure means the catalog still describes a gateway-only
// signer that mints session tokens, or tells a client to retry without the
// Retry-After header every KMS_SIGNING_UNAVAILABLE response carries.
//
// spec: 15.1 (error table KMS_SIGNING_UNAVAILABLE row), 10.2 (KMS signing
// failure mode)
func TestErrorCatalogKMSSigningUnavailableRow_spec_15(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "reference/error-catalog.md",
			row:        "| `KMS_SIGNING_UNAVAILABLE` |",
			contains:   []string{"| 503 |", "gateway or the Token Service", "throttling", "`Retry-After`"},
			notContain: []string{"session token could not be minted"},
		},
	})
}

// TestAuditLockRemedyLowersConcurrentSessionQuota_spec_17_7 asserts the
// audit-pipeline runbook relieves audit-lock contention through the
// tenant's concurrent-session quota on the admin API.
//
// diagnosis: A failure means the runbook still sends the operator to a
// Helm token-endpoint rate limit, which does not slow session creation,
// lost the admin API access marker for the quota update, or nests the
// quota under a "quota" object that the tenant update body does not
// define, so the PUT succeeds and the quota stays unchanged.
//
// spec: 17.7 (Audit pipeline degradation remediation), 11.2 (Budgets and
// quotas)
func TestAuditLockRemedyLowersConcurrentSessionQuota_spec_17_7(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "runbooks/audit-pipeline-degraded.md",
			contains:   []string{"<!-- access: api method=PUT path=/v1/admin/tenants/{id} -->", `{"maxConcurrentSessions": <lower value>}`, "confirm that `maxConcurrentSessions` carries the new value"},
			notContain: []string{"oauth.rateLimit.tenantPerSecond", `{"quota":`},
		},
	})
}
