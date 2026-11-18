// SPDX-License-Identifier: MIT

package playground

import (
	"flag"
	"os"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the playground reads. They mirror the
// playground.* Helm values, which the gateway reads from its own
// configuration so the playground is gated without a separate deployment
// target.
type Flags struct {
	// Enabled is playground.enabled.
	Enabled *bool
	// AuthMode is playground.authMode.
	AuthMode *string
	// DevTenantID is playground.devTenantId.
	DevTenantID *string
	// AllowedRuntimes is playground.allowedRuntimes, a comma-separated
	// glob list.
	AllowedRuntimes *string
	// MaxSessionMinutes is playground.maxSessionMinutes.
	MaxSessionMinutes *int
	// MaxIdleTimeSeconds is playground.maxIdleTimeSeconds.
	MaxIdleTimeSeconds *int
	// OIDCSessionTTLSeconds is playground.oidcSessionTtlSeconds.
	OIDCSessionTTLSeconds *int
	// GatewayHost is the public gateway host the UI connects to.
	GatewayHost *string
	// SessionLabels is playground.sessionLabels as comma-separated
	// key=value pairs.
	SessionLabels *string
}

// RegisterFlags registers the playground's flags on fs and returns their
// values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.Enabled = fs.Bool("playground-enabled", envdefault.Flag("LENNY_PLAYGROUND_ENABLED"),
		"§27.2 playground.enabled: serve the web playground at /playground. When false, /playground/* returns 404. Override via LENNY_PLAYGROUND_ENABLED.")
	f.AuthMode = fs.String("playground-auth-mode", envdefault.String("LENNY_PLAYGROUND_AUTH_MODE", "oidc"),
		"§27.2 playground.authMode: one of oidc, apiKey, or dev. Override via LENNY_PLAYGROUND_AUTH_MODE.")
	f.DevTenantID = fs.String("playground-dev-tenant-id", envdefault.String("LENNY_PLAYGROUND_DEV_TENANT_ID", "default"),
		"§27.2 playground.devTenantId: the tenant bound to the dev HMAC JWT when playground.authMode=dev. Override via LENNY_PLAYGROUND_DEV_TENANT_ID.")
	f.AllowedRuntimes = fs.String("playground-allowed-runtimes", envdefault.String("LENNY_PLAYGROUND_ALLOWED_RUNTIMES", "*"),
		"§27.2 playground.allowedRuntimes: a comma-separated glob list of runtime IDs visible in the playground runtime picker. Override via LENNY_PLAYGROUND_ALLOWED_RUNTIMES.")
	f.MaxSessionMinutes = fs.Int("playground-max-session-minutes", envdefault.Int("LENNY_PLAYGROUND_MAX_SESSION_MINUTES", 30),
		"§27.6 playground.maxSessionMinutes: the hard cap on a playground-initiated session's active time, which pauses while the session is `suspended`. Override via LENNY_PLAYGROUND_MAX_SESSION_MINUTES.")
	f.MaxIdleTimeSeconds = fs.Int("playground-max-idle-time-seconds", envdefault.Int("LENNY_PLAYGROUND_MAX_IDLE_TIME_SECONDS", 300),
		"§27.2 playground.maxIdleTimeSeconds: the hard idle-timeout override for playground-initiated sessions. Override via LENNY_PLAYGROUND_MAX_IDLE_TIME_SECONDS.")
	f.OIDCSessionTTLSeconds = fs.Int("playground-oidc-session-ttl-seconds", envdefault.Int("LENNY_PLAYGROUND_OIDC_SESSION_TTL_SECONDS", 3600),
		"§27.2 playground.oidcSessionTtlSeconds: the lifetime of the server-side playground session record and cookie. Override via LENNY_PLAYGROUND_OIDC_SESSION_TTL_SECONDS.")
	f.GatewayHost = fs.String("playground-gateway-host", os.Getenv("LENNY_PLAYGROUND_GATEWAY_HOST"),
		"§27.7 the public gateway host the playground UI connects to over the MCP WebSocket; interpolated into the playground connect-src CSP directive. Override via LENNY_PLAYGROUND_GATEWAY_HOST.")
	f.SessionLabels = fs.String("playground-session-labels", os.Getenv("LENNY_PLAYGROUND_SESSION_LABELS"),
		"§27.2 playground.sessionLabels: comma-separated key=value pairs stamped on every playground session record and audit event. Empty applies the default {origin: \"playground\"}; the load-bearing origin entry is re-stamped at startup regardless of the supplied value. Override via LENNY_PLAYGROUND_SESSION_LABELS.")
	return f
}

// Config returns the enabled playground's configuration from the flags and
// the two deployment-wide values the composition root reads: the
// capability token lifetime and whether the deployment is multi-tenant.
// The caller validates it with Config.Validate.
func (f *Flags) Config(capabilityTokenTTL time.Duration, multiTenant bool) Config {
	return Config{
		Enabled:            true,
		AuthMode:           AuthMode(*f.AuthMode),
		DevTenantID:        *f.DevTenantID,
		AllowedRuntimes:    splitList(*f.AllowedRuntimes),
		MaxSessionMinutes:  *f.MaxSessionMinutes,
		MaxIdleTimeSeconds: *f.MaxIdleTimeSeconds,
		OIDCSessionTTL:     time.Duration(*f.OIDCSessionTTLSeconds) * time.Second,
		CapabilityTokenTTL: capabilityTokenTTL,
		MultiTenant:        multiTenant,
		GatewayHost:        *f.GatewayHost,
		SessionLabels:      parseKeyValueList(*f.SessionLabels),
	}
}

// splitList splits a comma-separated flag value, dropping blank entries
// and trimming the rest.
func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseKeyValueList splits a comma-separated key=value flag value into a
// map. Trimmed empty entries, entries without `=`, and entries with an
// empty key are skipped, and an empty input yields a nil map. The
// playground.sessionLabels flag uses this encoding, so a Helm value such
// as `{origin: playground, env: stage}` renders to
// `--playground-session-labels=origin=playground,env=stage`.
func parseKeyValueList(raw string) map[string]string {
	var out map[string]string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[k] = v
	}
	return out
}
