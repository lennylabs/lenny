// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component setup-and-import smoke test for the §26.9 mastra
// reference runtime's TypeScript bootstrap path: `npm ci` (or `pnpm
// install`) against the workspace's package.json, followed by the
// adapter importing the user's agent module via ts-node/tsx and
// answering a single message. This complements
// tests/tier5_e2e_kind/framework_runtime_mastra_test.go, which drives
// a full live session (including Mastra tool-call mapping) once a
// runnable adapter exists; this file isolates the narrower
// setup/import step so a regression in the `npm ci` -> `tsx` import
// chain surfaces even before a full session-level harness is
// available.
package runtimes_test

import "testing"

// spec: §26.9 ("The adapter is a Node process; it imports the user
// module via ts-node/tsx (bundled in the image) and wraps the Mastra
// agent's message handling.")
//
// diagnosis: once unskipped, a failure here means the mastra adapter
// either did not complete `npm ci`/`pnpm install` against the
// fixture's package.json, did not import the fixture's src/agent.ts
// via ts-node/tsx, or did not answer a single message sent through
// the imported agent's message handling.
func TestMastraAdapterImportsTypeScriptAgentViaTsx(t *testing.T) {
	// The mastra reference runtime (github.com/lennylabs/runtime-mastra)
	// is not vendored in this repo, and tests/spec-map.json marks §26.9
	// blocked_until_phase 11: no in-repo package
	// (cmd/runtimes/mastra does not exist) bundles ts-node/tsx or
	// performs the `npm ci` setup-command / module-import bootstrap
	// this test targets, so there is no adapter binary to invoke and
	// no npm-installable image to run `npm ci` inside. See the sibling
	// skip on TestMastraRuntimeSessionMapsToolCallsAndStreamsResponse
	// in tests/tier5_e2e_kind/framework_runtime_mastra_test.go for the
	// same missing-adapter reasoning applied to the full session flow.
	//
	// Unskip once a runnable mastra image or an equivalent in-repo
	// adapter under cmd/runtimes/mastra exists (bundling ts-node/tsx
	// and running `npm ci`/`pnpm install` as its setup command) and a
	// fixture TypeScript agent module (package.json plus
	// src/agent.ts) is available under tests/testdata.
	t.Skip("no runnable mastra reference-runtime image or in-repo adapter implementing the §26.9 npm ci / ts-node/tsx import bootstrap exists yet")
}
