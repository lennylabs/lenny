// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the credential path the adapter manifest
// carries.
//
// The credential file is written per session at
// /run/lenny/slots/{sessionId}/credentials.json, and no pod carries a
// pod-global /run/lenny/credentials.json. A construction-time default cannot
// name a session-scoped file, so the resolved path is delivered on the adapter
// manifest, which the adapter writes before spawning each session's runtime
// binary. A runtime that reads credential material therefore reads
// `credentialsPath` from the manifest, at every integration level including
// Basic.
//
// The retired claim is that a Basic-level runtime never needs the manifest.
// A reader who follows it and assumes a fixed location does not find the file
// at all, because the location depends on the session identifier.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 4.7 (adapter manifest field set, Basic-level reading requirement),
// 6.1 (per-session credential lease), 13.1 (credential-file delivery)

package tier11_docs_test

import (
	"testing"
)

// credentialSlotPath is the only credential path the platform writes, with the
// session-identifier placeholder the documentation spells.
const credentialSlotPath = "/run/lenny/slots/{sessionId}/credentials.json"

// retiredPodGlobalCredentialPath is the pod-global path the per-slot layout
// retires. It exists on no pod, so a page naming it sends a runtime author or
// an operator to a file that is never written.
const retiredPodGlobalCredentialPath = "/run/lenny/credentials.json"

// spec: 4.7, 6.1
// diagnosis: the reader-facing adapter contract disagrees with the §4.7
//
//	manifest field set about the credential path. The manifest carries a
//	required `credentialsPath` member naming this session's credential file,
//	and a runtime that reads credential material reads it from there rather
//	than assuming a fixed location. A page that omits the member, or that tells
//	a Basic-level runtime it never needs the manifest, leaves an author unable
//	to locate the file its own session's credentials were written to.
func TestAdapterManifestDocsCarryTheCredentialsPathMember(t *testing.T) {
	root := repoRoot(t)

	contract := readRepoFile(t, root, "docs", "reference", "adapter-contract.md")
	manifest := section(contract, "Adapter Manifest")
	if manifest == "" {
		t.Fatal("docs/reference/adapter-contract.md: `Adapter Manifest` section not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md Adapter Manifest section", manifest, []string{
		`"credentialsPath": "/run/lenny/slots/sess_abc/credentials.json"`,
		"| `credentialsPath` |",
		credentialSlotPath,
		"A Basic-level runtime that reads a credential file reads `credentialsPath` for its location.",
	})
	requireNoneContain(t, "adapter-contract.md Adapter Manifest section", manifest, []string{
		retiredPodGlobalCredentialPath,
	})

	requireNoneContain(t, "adapter-contract.md", contract, []string{
		"Basic-level runtimes do not need to read the manifest at all",
	})
	requireAllContain(t, "adapter-contract.md", contract, []string{
		"a Basic-level runtime that reads a credential file reads `credentialsPath` from the manifest to find it",
	})

	lifecycle := readRepoFile(t, root, "docs", "runtime-author-guide", "lifecycle.md")
	requireAllContain(t, "runtime-author-guide/lifecycle.md", lifecycle, []string{
		`"credentialsPath": "/run/lenny/slots/sess_abc123/credentials.json"`,
		"a Basic-level runtime that reads a credential file reads `credentialsPath` from the manifest to find it",
	})
	requireNoneContain(t, "runtime-author-guide/lifecycle.md", lifecycle, []string{
		"At the Basic level, you can ignore this file",
		retiredPodGlobalCredentialPath,
	})
}

// credentialLeasePages are the reader-facing statements of the credential
// lease that carried the `maxConcurrentSessions > 1` presence condition the
// per-session rule replaces, each with the phrase a failure reports.
func credentialLeasePages(t *testing.T, root string) map[string]string {
	t.Helper()
	pages := map[string]string{}
	for label, parts := range map[string][]string{
		"reference/glossary.md":                 {"docs", "reference", "glossary.md"},
		"operator-guide/security-principles.md": {"docs", "operator-guide", "security-principles.md"},
	} {
		pages[label] = readRepoFile(t, root, parts...)
	}
	return pages
}

// spec: 6.1, 13.1
// diagnosis: a reader-facing statement of the credential lease still makes the
//
//	per-slot lease conditional on `maxConcurrentSessions > 1`. Every session
//	holds a slot on every pod and its lease is materialized at that session's
//	own /run/lenny/slots/{sessionId}/credentials.json, so the conditional tells
//	an operator that a single-session pod delivers credentials somewhere else,
//	and no such location exists.
func TestCredentialLeaseDocsStateOneLeasePerSessionOnEveryPod(t *testing.T) {
	root := repoRoot(t)

	for label, page := range credentialLeasePages(t, root) {
		requireAllContain(t, label, page, []string{credentialSlotPath})
		requireNoneContain(t, label, page, []string{
			"per slot when `sessionPolicy.maxConcurrentSessions > 1`",
			"per-slot leases when `maxConcurrentSessions > 1`",
			retiredPodGlobalCredentialPath,
		})
	}

	for label, parts := range map[string][]string{
		"operator-guide/security.md":       {"docs", "operator-guide", "security.md"},
		"operator-guide/configuration.md":  {"docs", "operator-guide", "configuration.md"},
		"getting-started/concepts.md":      {"docs", "getting-started", "concepts.md"},
		"runbooks/ephemeral-container.md":  {"docs", "runbooks", "ephemeral-container-cred-guard-unavailable.md"},
		"charts ephemeral-container guard": {"charts", "lenny", "templates", "admission-policies", "ephemeral-container-cred-guard-webhook.yaml"},
	} {
		body := readRepoFile(t, root, parts...)
		requireNoneContain(t, label, body, []string{retiredPodGlobalCredentialPath})
	}

	example := readRepoFile(t, root, "schemas", "examples", "runtime-ops.credentials_rotated.json")
	requireNoneContain(t, "schemas/examples/runtime-ops.credentials_rotated.json", example, []string{
		retiredPodGlobalCredentialPath,
	})
	requireAllContain(t, "schemas/examples/runtime-ops.credentials_rotated.json", example, []string{
		"/run/lenny/slots/",
	})
}
