// SPDX-License-Identifier: MIT

package eventsubscription

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// Resolver is the DNS seam the §25.5 per-delivery resolution step uses.
// The production validator uses net.DefaultResolver; tests inject a stub
// so the private-range checks run without real DNS. spec: §25.5 line
// 2741.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// SSRFConfig configures the §25.5 callback-URL SSRF/DNS-rebinding
// validator. The zero value is the strict default: HTTPS only, no
// allowlist, no extra blocked CIDRs, and net.DefaultResolver. spec:
// §25.5 lines 2735-2745.
type SSRFConfig struct {
	// AllowHTTP permits http:// callback URLs (ops.webhooks.allowHTTP).
	// Off by default: HTTPS is required.
	AllowHTTP bool
	// DomainAllowlist, when non-empty, restricts callbacks to hosts that
	// match an entry by exact name or "*.suffix" wildcard
	// (ops.webhooks.domainAllowlist).
	DomainAllowlist []string
	// BlockedCIDRs are extra ranges rejected in addition to the built-in
	// private/reserved set, e.g. the Kubernetes service and pod CIDRs
	// (ops.webhooks.blockedCIDRs).
	BlockedCIDRs []netip.Prefix
	// Resolver is the DNS seam. A nil resolver uses net.DefaultResolver.
	Resolver Resolver
}

// SSRFValidator enforces the §25.5 callback-URL mitigations: scheme
// restriction, IP-literal and metadata-host rejection, the optional
// domain allowlist, and per-call DNS resolution with private/reserved
// and blocked-CIDR rejection. It runs at subscription create/update and
// at each delivery attempt. spec: §25.5 lines 2735-2745.
type SSRFValidator struct {
	cfg SSRFConfig
}

// NewSSRFValidator returns a validator with the supplied policy.
func NewSSRFValidator(cfg SSRFConfig) *SSRFValidator {
	return &SSRFValidator{cfg: cfg}
}

// defaultSSRFValidator is the strict default applied when a Service has
// no validator configured.
var defaultSSRFValidator = NewSSRFValidator(SSRFConfig{})

// metadataHosts are the §25.5 line 2743 cloud instance-metadata
// hostnames rejected regardless of resolved IP.
var metadataHosts = map[string]bool{
	"metadata.google.internal": true,
	"instance-data":            true,
}

// imdsAddrs are the §25.5 line 2743 cloud instance-metadata IPs blocked
// in addition to the private-range check (169.254.169.254 is already
// link-local; fd00:ec2::254 is already ULA; listed for defense-in-depth
// and to name the AWS/GCP/Azure endpoints explicitly).
var imdsAddrs = func() []netip.Addr {
	out := make([]netip.Addr, 0, 2)
	for _, s := range []string{"169.254.169.254", "fd00:ec2::254"} {
		if a, err := netip.ParseAddr(s); err == nil {
			out = append(out, a)
		}
	}
	return out
}()

// Validate applies the §25.5 SSRF mitigations to raw. On failure it
// returns a typed Error with ErrCodeWebhookValidation so the opsserver
// maps it to 422 WEBHOOK_VALIDATION_FAILED. spec: §25.5 lines 2735-2745,
// 2796.
func (v *SSRFValidator) Validate(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" {
		return webhookErr("callbackUrl is not a valid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https":
	case "http":
		if !v.cfg.AllowHTTP {
			return webhookErr("callbackUrl must use https (set ops.webhooks.allowHTTP to permit http)")
		}
	default:
		return webhookErr("callbackUrl scheme must be http or https")
	}

	host := u.Hostname()
	if host == "" {
		return webhookErr("callbackUrl must include a host")
	}
	// spec: §25.5 line 2740 — the host must be a registered domain, not a
	// raw IP literal. A URL whose userinfo or path embeds an IP literal
	// (https://example.com@127.0.0.1/) parses its real host here, so the
	// literal check covers that smuggling form.
	if _, perr := netip.ParseAddr(host); perr == nil {
		return webhookErr("callbackUrl host must be a registered domain, not an IP literal")
	}
	lowHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowHost == "localhost" || metadataHosts[lowHost] {
		return webhookErr("callbackUrl host is not permitted")
	}
	// spec: §25.5 line 2745 — optional deployer allowlist (suffix match).
	if len(v.cfg.DomainAllowlist) > 0 && !matchesDomainAllowlist(lowHost, v.cfg.DomainAllowlist) {
		return webhookErr("callbackUrl host is not in ops.webhooks.domainAllowlist")
	}

	// spec: §25.5 lines 2741-2743 — resolve the host on every call and
	// reject when any resolved IP is private, reserved, an IMDS address,
	// or inside a blocked CIDR. Re-resolving per call closes the DNS
	// rebinding gap.
	addrs, err := v.resolve(ctx, host)
	if err != nil {
		return webhookErr("callbackUrl host did not resolve: " + err.Error())
	}
	if len(addrs) == 0 {
		return webhookErr("callbackUrl host resolved to no address")
	}
	for _, a := range addrs {
		if reason := v.blockReason(a); reason != "" {
			return webhookErr(fmt.Sprintf("callbackUrl resolved to a blocked address %s (%s)", a, reason))
		}
	}
	return nil
}

func (v *SSRFValidator) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	r := v.cfg.Resolver
	if r == nil {
		r = defaultResolver{}
	}
	return r.LookupNetIP(ctx, "ip", host)
}

// blockReason returns a non-empty reason when a is not a globally
// routable address the webhook may target. spec: §25.5 lines 2741-2743.
func (v *SSRFValidator) blockReason(a netip.Addr) string {
	if a.Is4In6() {
		a = a.Unmap()
	}
	if !a.IsValid() {
		return "invalid"
	}
	for _, imds := range imdsAddrs {
		if a == imds {
			return "imds"
		}
	}
	switch {
	case a.IsLoopback():
		return "loopback"
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
		return "link-local"
	case a.IsMulticast(), a.IsInterfaceLocalMulticast():
		return "multicast"
	case a.IsUnspecified():
		return "unspecified"
	case a.IsPrivate():
		return "private"
	}
	// RFC 6598 carrier-grade-NAT 100.64.0.0/10 is not covered by
	// IsPrivate; reject it explicitly.
	if a.Is4() {
		b := a.As4()
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return "cgnat"
		}
	}
	for _, p := range v.cfg.BlockedCIDRs {
		if p.Contains(a) {
			return "blocked-cidr"
		}
	}
	return ""
}

// matchesDomainAllowlist reports whether host matches an allowlist entry.
// An entry is an exact hostname or a "*.suffix" wildcard matching any
// subdomain of suffix. spec: §25.5 line 2745.
func matchesDomainAllowlist(host string, entries []string) bool {
	for _, raw := range entries {
		e := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(raw, ".")))
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "*.") {
			suffix := e[1:] // ".pagerduty.com"
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		// A bare domain entry matches the domain and any subdomain of it,
		// so "pagerduty.com" admits "events.pagerduty.com".
		if host == e || strings.HasSuffix(host, "."+e) {
			return true
		}
	}
	return false
}

func webhookErr(msg string) *Error {
	return &Error{Code: ErrCodeWebhookValidation, Message: msg}
}
