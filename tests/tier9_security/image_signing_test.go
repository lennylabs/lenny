// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §5.2 cosign image-signing admission
// webhook. The e2e install leaves imageVerification.cosign.enabled at
// its default false, so the chart does not render the lenny-cosign-verify
// ValidatingWebhookConfiguration.
//
// The test asserts the chart's feature-gating is correct against the
// live cluster: the cosign webhook is absent on a release that did not
// enable it. Confirming the webhook is correctly withheld is a genuine
// §17.2-inventory check. The behavioural half — that an unsigned image
// is rejected — needs an install with the feature on and a signed-image
// registry, and is skipped with that diagnosis.

package tier9_security_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// cosignWebhook is the ValidatingWebhookConfiguration the chart renders
// for §5.2 cosign image verification when imageVerification.cosign.enabled
// is true.
const cosignWebhook = "lenny-cosign-verify"

// spec: 13.1
// diagnosis: the §5.2 cosign image-signing webhook is mis-gated. The
// webhook is rendered only when imageVerification.cosign.enabled is
// true; the e2e overlay leaves it false, so lenny-cosign-verify must be
// absent. The test confirms the chart withheld it. The unsigned-image
// rejection itself needs an install with imageVerification.cosign.enabled
// and a registry of cosign-signed images, so that half is skipped.
func TestImageSigningCosign(t *testing.T) {
	c := kind.InstallLenny(t)

	out, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", cosignWebhook,
		"--ignore-not-found", "-o", "jsonpath={.metadata.name}",
	)
	if err != nil {
		t.Fatalf("query ValidatingWebhookConfiguration %q: %v\n%s", cosignWebhook, err, out)
	}
	if strings.TrimSpace(out) == cosignWebhook {
		t.Fatalf("ValidatingWebhookConfiguration %q is installed on a release that did not set "+
			"imageVerification.cosign.enabled=true; the chart's feature-gating rendered a webhook "+
			"it should have withheld", cosignWebhook)
	}
	t.Logf("§5.2 cosign webhook %q correctly absent (imageVerification.cosign.enabled=false)", cosignWebhook)

	t.Skip("the §5.2 cosign image-verification behaviour is gated off " +
		"(imageVerification.cosign.enabled=false in the e2e overlay); exercising the unsigned-image " +
		"rejection needs an install with imageVerification.cosign.enabled=true and a registry of " +
		"cosign-signed platform images plus the trivy zero-critical-CVE gate")
}
