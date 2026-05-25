// SPDX-License-Identifier: MIT

package credential

import (
	"errors"
	"fmt"
	"time"
)

// CodeCredentialMaterializationError is the §4.9 internal error code for
// a direct-mode lease whose materializedConfig is missing a Required:yes
// field. The spec classifies it as category INTERNAL and surfaces it to
// the client as CREDENTIAL_POOL_EXHAUSTED.
//
// spec: §4.9 line 1298.
const CodeCredentialMaterializationError = "CREDENTIAL_MATERIALIZATION_ERROR"

// ErrCredentialMaterialization is the sentinel every MaterializationError
// matches under errors.Is, so a caller can branch on a materialization
// failure without naming the concrete type.
var ErrCredentialMaterialization = errors.New("credential: " + CodeCredentialMaterializationError)

// MaterializedConfig is the §4.9 direct-mode materializedConfig: the
// per-provider bundle of real upstream credential fields a direct-mode
// lease carries to the runtime. It is a discriminated map keyed on the
// enclosing lease's Provider; the runtime reads it from the credential
// file. Each built-in provider defines a fixed required-field set
// (spec/04 §4.9 "materializedConfig Schema by Provider"). Custom
// providers may carry any fields and bypass built-in field validation.
//
// All values are UTF-8 plaintext strings per the §4.9 encoding
// convention. The field is never serialized to JSON (`json:"-"` on the
// Lease) so the gateway's durable lease store never persists upstream
// secrets; the bundle reaches the pod through the adapter credential
// file and lives in the gateway only transiently.
//
// spec: §4.9 lines 1246-1298.
type MaterializedConfig map[string]string

// directRequiredFields lists the §4.9 Required:yes direct-mode
// materializedConfig fields for the built-in providers whose required
// set is a fixed list. azure_openai is variant (API-key vs Azure AD
// pool) and is validated separately; a provider absent from this map and
// not azure_openai is treated as custom and bypasses validation.
//
// spec: §4.9 lines 1267-1297.
var directRequiredFields = map[Provider][]string{
	ProviderAnthropicDirect: {"apiKey"},
	ProviderAWSBedrock:      {"accessKeyId", "secretAccessKey", "sessionToken", "region", "expiresAt"},
	ProviderVertexAI:        {"accessToken", "projectId", "region", "expiresAt"},
	ProviderGitHub:          {"token", "expiresAt"},
	ProviderVaultTransit:    {"vaultToken", "vaultAddr", "transitPath", "keyName", "expiresAt"},
}

// expiresAtField is the materializedConfig key carrying a built-in
// provider's own credential expiry (ISO 8601 UTC). Where present, §4.9
// requires it equal or precede the enclosing lease's expiresAt.
const expiresAtField = "expiresAt"

// MaterializationError reports a direct-mode lease whose
// materializedConfig is missing one or more §4.9 Required:yes fields, or
// carries an unparseable provider expiry. It satisfies errors.Is against
// the package sentinel so a caller can branch without naming the type.
//
// spec: §4.9 line 1298.
type MaterializationError struct {
	Provider Provider
	// Missing names the absent or empty Required:yes fields.
	Missing []string
	// Reason carries a non-presence materialization failure (an
	// unparseable expiresAt, or the azure_openai key-mode conflict).
	Reason string
}

func (e *MaterializationError) Error() string {
	switch {
	case len(e.Missing) > 0 && e.Reason != "":
		return fmt.Sprintf("credential: %s materializedConfig for provider %q is missing required fields %v and %s",
			CodeCredentialMaterializationError, e.Provider, e.Missing, e.Reason)
	case len(e.Missing) > 0:
		return fmt.Sprintf("credential: %s materializedConfig for provider %q is missing required fields %v",
			CodeCredentialMaterializationError, e.Provider, e.Missing)
	default:
		return fmt.Sprintf("credential: %s materializedConfig for provider %q is invalid: %s",
			CodeCredentialMaterializationError, e.Provider, e.Reason)
	}
}

// Is reports MaterializationError equal to the package's materialization
// sentinel so callers can errors.Is on it.
func (e *MaterializationError) Is(target error) bool {
	return target == ErrCredentialMaterialization
}

// ValidateMaterializedConfig checks that cfg carries every §4.9
// Required:yes field for a direct-mode lease on provider p, with a
// non-empty value, and that any provider expiry parses as RFC3339. It
// returns a *MaterializationError on a violation, or nil when the config
// is complete. A custom provider (any value outside the built-in enum)
// bypasses built-in validation and always returns nil, per §4.9 line
// 1298 ("Custom providers bypass built-in field validation").
//
// spec: §4.9 lines 1267-1298.
func ValidateMaterializedConfig(p Provider, cfg MaterializedConfig) error {
	if p == ProviderAzureOpenAI {
		return validateAzureOpenAI(cfg)
	}
	required, ok := directRequiredFields[p]
	if !ok {
		// Custom provider: pass through without validation.
		return nil
	}
	missing := missingFields(cfg, required)
	if len(missing) > 0 {
		return &MaterializationError{Provider: p, Missing: missing}
	}
	if reason := badExpiry(cfg); reason != "" {
		return &MaterializationError{Provider: p, Reason: reason}
	}
	return nil
}

// validateAzureOpenAI enforces the §4.9 azure_openai variant rules:
// endpoint and deploymentName are always required; exactly one of apiKey
// (API-key pool) or accessToken (Azure AD pool) must be present; an
// Azure AD pool additionally requires a parseable expiresAt.
//
// spec: §4.9 lines 1289-1294.
func validateAzureOpenAI(cfg MaterializedConfig) error {
	missing := missingFields(cfg, []string{"endpoint", "deploymentName"})
	hasAPIKey := cfg["apiKey"] != ""
	hasAccessToken := cfg["accessToken"] != ""
	switch {
	case hasAPIKey && hasAccessToken:
		return &MaterializationError{Provider: ProviderAzureOpenAI, Missing: missing,
			Reason: "exactly one of apiKey (API-key pool) or accessToken (Azure AD pool) is allowed, not both"}
	case !hasAPIKey && !hasAccessToken:
		return &MaterializationError{Provider: ProviderAzureOpenAI, Missing: missing,
			Reason: "one of apiKey (API-key pool) or accessToken (Azure AD pool) is required"}
	}
	if hasAccessToken {
		// Azure AD pool: the short-lived token carries an expiry.
		missing = append(missing, missingFields(cfg, []string{expiresAtField})...)
	}
	if len(missing) > 0 {
		return &MaterializationError{Provider: ProviderAzureOpenAI, Missing: missing}
	}
	if reason := badExpiry(cfg); reason != "" {
		return &MaterializationError{Provider: ProviderAzureOpenAI, Reason: reason}
	}
	return nil
}

// missingFields returns the subset of want absent from or empty in cfg,
// in the order want lists them.
func missingFields(cfg MaterializedConfig, want []string) []string {
	var missing []string
	for _, f := range want {
		if cfg[f] == "" {
			missing = append(missing, f)
		}
	}
	return missing
}

// badExpiry returns a non-empty reason when cfg carries an expiresAt
// that does not parse as RFC3339; an absent expiresAt is not bad here
// (presence is enforced by the required-field check).
func badExpiry(cfg MaterializedConfig) string {
	v, ok := cfg[expiresAtField]
	if !ok || v == "" {
		return ""
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return fmt.Sprintf("expiresAt %q is not an RFC3339 timestamp", v)
	}
	return ""
}

// MaterializedExpiry returns the provider credential expiry recorded in a
// direct-mode materializedConfig, when the provider carries one and it
// parses. ok is false when the config has no expiresAt or it does not
// parse. The §4.9 vault_transit TTL clamp and the general "materialized
// expiresAt must equal or precede the lease expiresAt" invariant read
// it.
//
// spec: §4.9 lines 1154, 1271.
func MaterializedExpiry(cfg MaterializedConfig) (time.Time, bool) {
	v, ok := cfg[expiresAtField]
	if !ok || v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
