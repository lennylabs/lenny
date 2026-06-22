// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §13 credential-free posture check for the §17.4 Embedded Mode echo
// runtime proposal 0016 activates. The §4.7 pod path is now reached in Embedded
// Mode, so the runtime that runs out of the box must carry no LLM-provider
// credential surface: a credential-free runtime leases no credentials at the
// §4.7 boundary because its catalog entry declares no provider and no credential
// capabilities, so the §13.1 credential-delivery path is never engaged for it.
// This is a focused assertion against the credential-free catalog surface; it
// adds no new isolation surface and needs no cluster, complementing the
// cluster-bound credential-leakage probe in credential_leakage_test.go.
package tier9_security_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/embedded/stack"
)

// TestEmbeddedEchoRuntimeLeasesNoCredentials_spec_13 asserts the §15.4.4 echo
// runtime the Embedded Mode bring-up seeds (and runs on a §4.7 pod out of the
// box) carries no LLM-provider credential surface: no supportedProviders and no
// credentialCapabilities. A runtime with no provider and no credential
// capabilities never engages the §13.1 credential-delivery path, so the
// credential-free echo session leases no LLM credentials at the §4.7 boundary.
// The reference runtimes that do declare providers (chat, the coding agents)
// require their credentials and are placeholder-pinned out of the box, so echo
// is the only runtime that runs credential-free.
//
// spec: §13.1 (credential delivery engages only for a runtime that declares a
// provider), §15.4.4 (echo conformance exemplar is credential-free), §17.4
// (Embedded Mode runs the credential-free echo out of the box).
func TestEmbeddedEchoRuntimeLeasesNoCredentials_spec_13(t *testing.T) {
	echo := stack.EchoRuntime()

	if len(echo.SupportedProviders) != 0 {
		t.Errorf("echo runtime supportedProviders = %v, want none (credential-free)", echo.SupportedProviders)
	}
	if echo.CredentialCapabilities != nil {
		t.Errorf("echo runtime carries credentialCapabilities = %+v, want nil (no credential surface)", echo.CredentialCapabilities)
	}
	// The echo entry must not carry the §26 reference-runtime provider marker
	// label either; a credential-free runtime declares no provider in any form.
	for k, v := range echo.Labels {
		if k == "lenny.dev/provider" {
			t.Errorf("echo runtime carries a provider label %q = %q, want none (credential-free)", k, v)
		}
	}
}
