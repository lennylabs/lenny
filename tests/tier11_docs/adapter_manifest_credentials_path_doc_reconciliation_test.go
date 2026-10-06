// SPDX-License-Identifier: MIT

// Tier-11 documentation check for the carrier of a session's credential path.
//
// The credential file is written per session at
// /run/lenny/slots/{sessionId}/credentials.json, and no pod carries a
// pod-global credential file directly under /run/lenny/. A construction-time
// default cannot name a session-scoped file, so the resolved path is delivered
// in the `credentialsPath` member of the session's own `session_start` frame on
// the message channel. The adapter manifest is one pod-global file that carries
// only pod-scoped fields, so it names no session's credential file: one runtime
// process serves every session on the pod, and a path the manifest carried
// would be whichever session's start last wrote it.
//
// The retired claims are that a Basic-level runtime never needs to locate the
// file, and that the runtime reads the path from the manifest. A reader who
// follows either one does not find its own session's file.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 28.5.3 (CH-MSGSOCK session_start frame), 4.7.6 (adapter manifest
// field set, Basic-level reading requirement), 6.1 (per-session credential
// lease), 13.1 (credential-file delivery)

package tier11_docs_test

import (
	"testing"
)

// credentialSlotPath is the only credential path the platform writes, with the
// session-identifier placeholder the documentation spells.
const credentialSlotPath = "/run/lenny/slots/{sessionId}/credentials.json"

// retiredPodGlobalCredentialPath is the pod-global path the per-slot layout
// retires. It exists on no pod, so a page naming it sends a runtime author or
// an operator to a file that is never written. It is built from its parts so
// that this package, which the widened credential sweep reads, does not report
// its own subject.
const retiredPodGlobalCredentialPath = "/run/lenny/" + "credentials.json"

// retiredManifestCredentialCarrier is the phrase that sent a runtime to the
// adapter manifest for its session's credential path.
const retiredManifestCredentialCarrier = "reads `credentialsPath` from the manifest"

// basicCredentialReadRule is the sentence both runtime-author pages carry for
// a Basic-level runtime that loads credential material.
const basicCredentialReadRule = "a Basic-level runtime that reads a credential file reads `credentialsPath` from the session's `session_start` frame to find it"

// spec: 28.5.3, 4.7.6, 6.1
// diagnosis: the reader-facing adapter contract or the pod lifecycle page
//
//	names the adapter manifest, rather than the session's `session_start`
//	frame, as the carrier of the session's credential path. The manifest is
//	pod-global and carries only pod-scoped fields, so a runtime that reads the
//	path from it finds no member, or on a pod serving several sessions reads
//	no path for its own session. A failure names the page and the carrier that
//	is behind.
func TestSessionStartFrameDocsCarryTheCredentialsPathMember(t *testing.T) {
	root := repoRoot(t)

	contract := readRepoFile(t, root, "docs", "reference", "adapter-contract.md")
	frame := section(contract, "`session_start` ---")
	if frame == "" {
		t.Fatal("docs/reference/adapter-contract.md: `session_start` reference section not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md session_start section", frame, []string{
		`"credentialsPath": "/run/lenny/slots/sess_abc/credentials.json"`,
		"| `credentialsPath` |",
		credentialSlotPath,
		"Present whenever the adapter provisioned a credential file for the session and absent otherwise",
	})
	requireNoneContain(t, "adapter-contract.md session_start section", frame, []string{
		retiredPodGlobalCredentialPath,
	})

	manifest := section(contract, "Adapter Manifest")
	if manifest == "" {
		t.Fatal("docs/reference/adapter-contract.md: `Adapter Manifest` section not found (renamed or removed?)")
	}
	requireAllContain(t, "adapter-contract.md Adapter Manifest section", manifest, []string{
		"reads its location from the `credentialsPath` member of the session's `session_start` frame",
	})
	requireNoneContain(t, "adapter-contract.md Adapter Manifest section", manifest, []string{
		`"credentialsPath":`,
		"| `credentialsPath` |",
		retiredPodGlobalCredentialPath,
	})

	requireNoneContain(t, "adapter-contract.md", contract, []string{
		"Basic-level runtimes do not need to read the manifest at all",
		retiredManifestCredentialCarrier,
	})
	requireAllContain(t, "adapter-contract.md", contract, []string{basicCredentialReadRule})

	lifecycle := readRepoFile(t, root, "docs", "runtime-author-guide", "lifecycle.md")
	requireAllContain(t, "runtime-author-guide/lifecycle.md", lifecycle, []string{
		`"credentialsPath": "/run/lenny/slots/sess_abc123/credentials.json"`,
		basicCredentialReadRule,
	})
	requireNoneContain(t, "runtime-author-guide/lifecycle.md", lifecycle, []string{
		"At the Basic level, you can ignore this file",
		retiredManifestCredentialCarrier,
		retiredPodGlobalCredentialPath,
	})
	manifestExample := section(lifecycle, "Adapter Manifest")
	if manifestExample == "" {
		t.Fatal("docs/runtime-author-guide/lifecycle.md: `Adapter Manifest` section not found (renamed or removed?)")
	}
	requireNoneContain(t, "runtime-author-guide/lifecycle.md Adapter Manifest section", manifestExample, []string{
		`"credentialsPath":`,
		`"taskId":`,
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
