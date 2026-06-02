// SPDX-License-Identifier: MIT

package eventsubscription_test

import (
	"context"
	"net/netip"
	"testing"

	es "github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// fixedResolver resolves a host to a caller-chosen address so the
// resolved-IP private/reserved checks run without real DNS.
type fixedResolver struct{ addr string }

func (f fixedResolver) LookupNetIP(_ context.Context, _, _ string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr(f.addr)}, nil
}

// spec: §25.5 lines 2735-2745 — the SSRF validator rejects non-HTTPS,
// IP literals, metadata hosts, and hosts that resolve into private,
// reserved, IMDS, or blocked-CIDR ranges; it accepts a public host.
func TestSSRFValidator_spec_25_5(t *testing.T) {
	public := es.NewSSRFValidator(es.SSRFConfig{Resolver: fixedResolver{"93.184.216.34"}})
	cases := []struct {
		name     string
		v        *es.SSRFValidator
		url      string
		wantPass bool
	}{
		{"public https", public, "https://acme.example/hook", true},
		{"http rejected by default", public, "http://acme.example/hook", false},
		{"ipv4 literal", public, "https://127.0.0.1/hook", false},
		{"ipv6 literal", public, "https://[::1]/hook", false},
		{"localhost", public, "https://localhost/hook", false},
		{"metadata host", public, "https://metadata.google.internal/hook", false},
		{"ftp scheme", public, "ftp://acme.example/hook", false},
		{"resolves loopback", es.NewSSRFValidator(es.SSRFConfig{Resolver: fixedResolver{"127.0.0.1"}}), "https://rebind.example/hook", false},
		{"resolves rfc1918", es.NewSSRFValidator(es.SSRFConfig{Resolver: fixedResolver{"10.0.0.5"}}), "https://internal.example/hook", false},
		{"resolves link-local imds", es.NewSSRFValidator(es.SSRFConfig{Resolver: fixedResolver{"169.254.169.254"}}), "https://imds.example/hook", false},
		{"resolves cgnat", es.NewSSRFValidator(es.SSRFConfig{Resolver: fixedResolver{"100.64.1.1"}}), "https://cgnat.example/hook", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.v.Validate(context.Background(), c.url)
			if c.wantPass && err != nil {
				t.Fatalf("Validate(%q) = %v, want pass", c.url, err)
			}
			if !c.wantPass {
				if err == nil {
					t.Fatalf("Validate(%q) passed, want reject", c.url)
				}
				if es.CodeOf(err) != es.ErrCodeWebhookValidation {
					t.Errorf("Validate(%q) code = %q, want WEBHOOK_VALIDATION_FAILED", c.url, es.CodeOf(err))
				}
			}
		})
	}
}

// spec: §25.5 line 2735 — ops.webhooks.allowHTTP permits http.
func TestSSRFValidatorAllowHTTP_spec_25_5(t *testing.T) {
	v := es.NewSSRFValidator(es.SSRFConfig{AllowHTTP: true, Resolver: fixedResolver{"93.184.216.34"}})
	if err := v.Validate(context.Background(), "http://acme.example/hook"); err != nil {
		t.Errorf("allowHTTP http reject = %v, want pass", err)
	}
}

// spec: §25.5 line 2745 — the domain allowlist restricts callbacks by
// suffix.
func TestSSRFValidatorDomainAllowlist_spec_25_5(t *testing.T) {
	v := es.NewSSRFValidator(es.SSRFConfig{
		DomainAllowlist: []string{"pagerduty.com", "*.slack.com"},
		Resolver:        fixedResolver{"93.184.216.34"},
	})
	for url, wantPass := range map[string]bool{
		"https://events.pagerduty.com/hook": true,
		"https://pagerduty.com/hook":        true,
		"https://hooks.slack.com/hook":      true,
		"https://acme.example/hook":         false,
	} {
		err := v.Validate(context.Background(), url)
		if (err == nil) != wantPass {
			t.Errorf("Validate(%q) = %v, wantPass=%v", url, err, wantPass)
		}
	}
}

// spec: §25.5 line 2742 — extra blocked CIDRs (k8s service/pod ranges)
// are rejected even though they are not in the built-in private set.
func TestSSRFValidatorBlockedCIDR_spec_25_5(t *testing.T) {
	v := es.NewSSRFValidator(es.SSRFConfig{
		BlockedCIDRs: []netip.Prefix{netip.MustParsePrefix("11.0.0.0/8")},
		Resolver:     fixedResolver{"11.2.3.4"},
	})
	if err := v.Validate(context.Background(), "https://svc.example/hook"); err == nil {
		t.Errorf("blocked-CIDR host passed, want reject")
	}
}
