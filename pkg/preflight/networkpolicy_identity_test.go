// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
)

func TestCheckSPIFFETrustDomainUniquenessPassesOnDistinctValue(t *testing.T) {
	existing := []preflight.GatewayIdentity{
		{Namespace: "lenny-prod", Name: "lenny-gateway", SPIFFETrustDomain: "lenny-prod-cluster"},
	}
	d := preflight.CheckSPIFFETrustDomainUniqueness("lenny-staging-cluster", "lenny-staging", existing)
	if !d.Passed {
		t.Errorf("check failed a distinct trust domain: %s", d.Reason)
	}
}

func TestCheckSPIFFETrustDomainUniquenessFailsOnCollision(t *testing.T) {
	// NET-064: another deployment already uses this trust domain.
	existing := []preflight.GatewayIdentity{
		{Namespace: "lenny-prod", Name: "lenny-gateway", SPIFFETrustDomain: "lenny-shared"},
	}
	d := preflight.CheckSPIFFETrustDomainUniqueness("lenny-shared", "lenny-staging", existing)
	if d.Passed {
		t.Fatal("check passed a trust domain already in use by another deployment")
	}
	if !strings.Contains(d.Reason, "NET-064") || !strings.Contains(d.Reason, "lenny-prod/lenny-gateway") {
		t.Errorf("reason %q does not cite NET-064 and the colliding Deployment", d.Reason)
	}
}

func TestCheckSPIFFETrustDomainUniquenessSkipsSameNamespace(t *testing.T) {
	// A helm upgrade re-installs into the same namespace; the existing
	// gateway there carries the same value and must not self-collide.
	existing := []preflight.GatewayIdentity{
		{Namespace: "lenny-system", Name: "lenny-gateway", SPIFFETrustDomain: "lenny-prod"},
	}
	d := preflight.CheckSPIFFETrustDomainUniqueness("lenny-prod", "lenny-system", existing)
	if !d.Passed {
		t.Errorf("check flagged a same-namespace re-install as a collision: %s", d.Reason)
	}
}

func TestCheckSPIFFETrustDomainUniquenessPassesOnEmptyValue(t *testing.T) {
	// The chart's required guard fails templating before preflight; an
	// empty value reaching the check is not treated as a collision.
	d := preflight.CheckSPIFFETrustDomainUniqueness("", "lenny-system", nil)
	if !d.Passed {
		t.Errorf("check failed on an empty trust domain: %s", d.Reason)
	}
}

func TestCheckSATokenAudienceUniquenessPassesOnDistinctValue(t *testing.T) {
	existing := []preflight.GatewayIdentity{
		{Namespace: "lenny-prod", Name: "lenny-gateway", SATokenAudience: "lenny-gateway-prod"},
	}
	d := preflight.CheckSATokenAudienceUniqueness("lenny-gateway-staging", "lenny-staging", existing)
	if !d.Passed {
		t.Errorf("check failed a distinct SA token audience: %s", d.Reason)
	}
}

func TestCheckSATokenAudienceUniquenessFailsOnCollision(t *testing.T) {
	// NET-064: a shared audience enables cross-deployment token replay.
	existing := []preflight.GatewayIdentity{
		{Namespace: "lenny-a", Name: "lenny-gateway", SATokenAudience: "lenny-gateway-shared"},
		{Namespace: "lenny-b", Name: "lenny-gateway", SATokenAudience: "lenny-gateway-other"},
	}
	d := preflight.CheckSATokenAudienceUniqueness("lenny-gateway-shared", "lenny-c", existing)
	if d.Passed {
		t.Fatal("check passed an SA token audience already in use by another deployment")
	}
	if !strings.Contains(d.Reason, "NET-064") || !strings.Contains(d.Reason, "lenny-a/lenny-gateway") {
		t.Errorf("reason %q does not cite NET-064 and the colliding Deployment", d.Reason)
	}
}

func TestCheckSATokenAudienceUniquenessSkipsSameNamespace(t *testing.T) {
	existing := []preflight.GatewayIdentity{
		{Namespace: "lenny-system", Name: "lenny-gateway", SATokenAudience: "lenny-gateway-prod"},
	}
	d := preflight.CheckSATokenAudienceUniqueness("lenny-gateway-prod", "lenny-system", existing)
	if !d.Passed {
		t.Errorf("check flagged a same-namespace re-install as a collision: %s", d.Reason)
	}
}

func TestCheckSATokenAudienceUniquenessPassesOnEmptyValue(t *testing.T) {
	d := preflight.CheckSATokenAudienceUniqueness("", "lenny-system", nil)
	if !d.Passed {
		t.Errorf("check failed on an empty SA token audience: %s", d.Reason)
	}
}
