// SPDX-License-Identifier: MIT

package gateway_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// TestStaticDiscovery is the §25.4 fixed-slice discovery used by
// tests and the v1 single-process degraded mode.
func TestStaticDiscovery(t *testing.T) {
	d := gateway.StaticDiscovery{"https://r1", "https://r2"}
	got, err := d.Endpoints(context.Background())
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(got) != 2 || got[0] != "https://r1" || got[1] != "https://r2" {
		t.Errorf("Endpoints = %v, want [https://r1 https://r2]", got)
	}
	// spec:§25.4: Endpoints must return a defensive copy so the
	// internal slice cannot be mutated by callers.
	got[0] = "mutated"
	got2, _ := d.Endpoints(context.Background())
	if got2[0] != "https://r1" {
		t.Errorf("internal slice mutated through returned copy")
	}
}

// TestHeadlessDiscovery_Lookup confirms §25.4 headless-Service DNS
// resolution yields one base URL per pod IP, formatted with the
// configured port and scheme.
func TestHeadlessDiscovery_Lookup(t *testing.T) {
	d := gateway.HeadlessDiscovery{
		Scheme:      "https",
		ServiceName: "lenny-gateway-pods",
		Namespace:   "lenny-system",
		Port:        8443,
		Resolver:    fakeResolver{ips: []string{"10.0.0.1", "10.0.0.2"}},
	}
	got, err := d.Endpoints(context.Background())
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 pod URLs", len(got))
	}
	for _, ep := range got {
		if !strings.HasPrefix(ep, "https://") || !strings.HasSuffix(ep, ":8443") {
			t.Errorf("endpoint = %q, want https://<ip>:8443 layout", ep)
		}
	}
}

// TestHeadlessDiscovery_DefaultPort confirms the §25.4 internal TLS
// default port (8443) is used when none is specified.
func TestHeadlessDiscovery_DefaultPort(t *testing.T) {
	d := gateway.HeadlessDiscovery{
		ServiceName: "lenny-gateway-pods",
		Namespace:   "lenny-system",
		Resolver:    fakeResolver{ips: []string{"10.0.0.1"}},
	}
	got, _ := d.Endpoints(context.Background())
	if !strings.HasSuffix(got[0], ":8443") {
		t.Errorf("default port = %q, want :8443 (TLS default per §25.4)", got[0])
	}
}

// TestHeadlessDiscovery_RequiresServiceAndNamespace catches a
// misconfigured chart: §25.4 keeps the headless-Service target an
// explicit configuration value.
func TestHeadlessDiscovery_RequiresServiceAndNamespace(t *testing.T) {
	d := gateway.HeadlessDiscovery{Namespace: "lenny-system"}
	if _, err := d.Endpoints(context.Background()); err == nil {
		t.Errorf("empty ServiceName should fail")
	}
	d2 := gateway.HeadlessDiscovery{ServiceName: "lenny-gateway-pods"}
	if _, err := d2.Endpoints(context.Background()); err == nil {
		t.Errorf("empty Namespace should fail")
	}
}

// TestHeadlessDiscovery_DNSFailure surfaces the §25.4 "no endpoints"
// failure path: a DNS error short-circuits the lookup so the caller
// can fall back to the ClusterIP path.
func TestHeadlessDiscovery_DNSFailure(t *testing.T) {
	d := gateway.HeadlessDiscovery{
		ServiceName: "lenny-gateway-pods",
		Namespace:   "lenny-system",
		Resolver:    fakeResolver{err: errors.New("dns unavailable")},
	}
	_, err := d.Endpoints(context.Background())
	if err == nil || !strings.Contains(err.Error(), "dns unavailable") {
		t.Errorf("Endpoints err = %v, want DNS surfacing", err)
	}
}

// fakeResolver is a DNSResolver test double.
type fakeResolver struct {
	ips []string
	err error
}

func (f fakeResolver) LookupHost(context.Context, string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ips, nil
}
