// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// verifyPostgresSchema fails fast when the gateway is pointed at a
// database that has not had the migrations/ schema applied. It probes
// for the sessions table; the fuller §11.7 startup grant-verification
// check lands with the audit pipeline.
func verifyPostgresSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = 'sessions')`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("postgres: schema probe failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("postgres: schema not migrated (the sessions table is absent); apply migrations/ before starting the gateway")
	}
	return nil
}

// platformConfigMissing is one §10.3 LENNY_CONFIG_MISSING
// violation: a required platform configuration key that is absent or
// invalid. The fields mirror the structured-log fields §10.3
// mandates (config_key, scope, remediation).
type platformConfigMissing struct {
	configKey   string
	scope       string
	remediation string
}

// validatePlatformConfig returns the §10.3 required-key
// violations for the platform keys gated at this point in gateway
// startup: the OIDC issuer URL and client ID (both exempt in dev mode
// per the line 373 dev-mode symmetry and §17.4), and the
// defaultMaxSessionDuration (always required to be a positive duration).
// The remaining required keys fail closed elsewhere so each key is
// gated before the replica is marked ready: noEnvironmentPolicy by
// resolveNoEnvironmentPolicy, playground.devTenantId by
// playground.Config.Validate. Extracted from main() so the
// TestGatewayConfigValidation regression test can cover the §10.3
// contract without booting a gateway. spec: §10.3;
// §17.4 dev mode.
func validatePlatformConfig(devMode bool, oidcIssuerURL, oidcClientID string, defaultMaxSessionSeconds int) []platformConfigMissing {
	var missing []platformConfigMissing
	if !devMode {
		switch issuer := strings.TrimSpace(oidcIssuerURL); {
		case issuer == "":
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.issuerUrl",
				scope:       "platform",
				remediation: "set auth.oidc.issuerUrl (Helm) / --oidc-issuer-url / LENNY_OIDC_ISSUER_URL to the OIDC issuer URL, or run with LENNY_DEV_MODE=true",
			})
		case !isAbsoluteURL(issuer):
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.issuerUrl",
				scope:       "platform",
				remediation: "auth.oidc.issuerUrl must be an absolute URL (scheme://host); fix --oidc-issuer-url / LENNY_OIDC_ISSUER_URL",
			})
		}
		if strings.TrimSpace(oidcClientID) == "" {
			missing = append(missing, platformConfigMissing{
				configKey:   "auth.oidc.clientId",
				scope:       "platform",
				remediation: "set auth.oidc.clientId (Helm) / --oidc-client-id / LENNY_OIDC_CLIENT_ID, or run with LENNY_DEV_MODE=true",
			})
		}
	}
	if defaultMaxSessionSeconds <= 0 {
		missing = append(missing, platformConfigMissing{
			configKey:   "defaultMaxSessionDuration",
			scope:       "platform",
			remediation: "set gateway.maxSessionAgeSeconds (Helm) / --max-session-age-seconds / LENNY_MAX_SESSION_AGE_SECONDS to a positive number of seconds",
		})
	}
	return missing
}

// isAbsoluteURL reports whether s parses as an absolute URL with a
// scheme and host — the §10.3 acceptance
// criterion for auth.oidc.issuerUrl.
func isAbsoluteURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && u.IsAbs() && u.Host != ""
}

// buildStartupProbeTLSConfig assembles the §10.3 startup TLS
// probe's client config from the optional CA bundle and client
// certificate. An empty CA uses the system trust store; an empty
// cert/key presents no client certificate. spec: §10.3.
func buildStartupProbeTLSConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read --startup-tls-probe-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--startup-tls-probe-ca %s contains no PEM certificates", caFile)
		}
		cfg.RootCAs = pool
	}
	if certFile != "" || keyFile != "" {
		crt, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("load --startup-tls-probe-cert/--startup-tls-probe-key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{crt}
	}
	return cfg, nil
}

// resolveNoEnvironmentPolicy returns the resolved §10.6 / §11.1
// platform-wide noEnvironmentPolicy or a fatal-startup error. An
// empty value outside dev mode returns the
// "LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform"
// error §10.3's configuration validation table mandates. Dev mode
// derives allow-all for local convenience. Any value other than
// deny-all / allow-all returns a typed validation error. Extracted
// from main() so the §11.1 TestGatewayConfigValidation test can
// regression-cover the §10.3 contract. spec: §10.6;
// §11.1; §10.3 configuration validation table.
func resolveNoEnvironmentPolicy(value string, devMode bool) (string, error) {
	resolved := value
	if resolved == "" && devMode {
		resolved = tenantstore.NoEnvPolicyAllowAll
	}
	if resolved == "" {
		return "", fmt.Errorf("LENNY_CONFIG_MISSING config_key=noEnvironmentPolicy scope=platform: " +
			"set --no-environment-policy or LENNY_NO_ENVIRONMENT_POLICY to deny-all or allow-all (§10.6)")
	}
	if resolved != tenantstore.NoEnvPolicyDenyAll && resolved != tenantstore.NoEnvPolicyAllowAll {
		return "", fmt.Errorf("--no-environment-policy must be deny-all or allow-all, got %q", resolved)
	}
	return resolved, nil
}
