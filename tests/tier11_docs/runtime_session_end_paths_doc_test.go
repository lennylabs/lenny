// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for how a session on a long-lived runtime
// process ends without a `session_end` frame.
//
// A session ends on its `session_end`, and also when the connection or loop
// that carries it ends. The specification names two adapter-side closes of
// the connection: the pod's termination, and the coordinator hold timeout with
// no new coordinator. The hold-timeout termination writes no `session_end` in
// either deployment model, so a page that lists the paths writing none, or
// that states when the adapter closes the connection, and leaves the hold
// timeout out tells a runtime author that the connection ends only with the
// pod. On the SDK side, `Run` ends every session the process holds after a
// `shutdown` frame or the end of stdin, so a page that names only
// `session_end` and the end of the connection as OnTerminate triggers leaves a
// process-scoped shutdown with no per-session cleanup.
//
// spec: 28.5.3 (CH-MSGSOCK Inbound: session_end), 28.5.3 (CH-MSGSOCK Session
// frame writes), 4.7.10 (Runtime process lifetime), 15.7 (Runtime author
// SDKs)

package tier11_docs_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// requirePhrases reports every phrase in want that text does not contain,
// naming where the text came from.
func requirePhrases(t *testing.T, where, text string, want []string) {
	t.Helper()
	if text == "" {
		t.Fatalf("%s: section not found", where)
	}
	for _, phrase := range want {
		if !strings.Contains(text, phrase) {
			t.Errorf("%s: missing %q", where, phrase)
		}
	}
}

// spec: 28.5.3 (CH-MSGSOCK Inbound: session_end), 28.5.3 (CH-MSGSOCK Session
// frame writes)
// diagnosis: the `session_end` reference in docs/reference/adapter-contract.md
//
//	no longer lists the coordinator hold-timeout termination among the paths
//	that write no `session_end`, or no longer says that the termination ends
//	the connection in the sidecar model and the session's loop in the embedded
//	model. A runtime author then expects a `session_end` on that teardown and
//	never receives one.
func TestSessionEndReferenceListsTheHoldTimeoutTerminationAsWritingNone(t *testing.T) {
	body := section(adapterContractDoc(t, repoRoot(t)), "`session_end` --- Release a Session")
	requirePhrases(t, "adapter-contract.md session_end", body, []string{
		"the termination that follows a coordinator hold timeout write no `session_end`",
		"The coordinator hold-timeout termination ends the connection in the sidecar model, and the session's runtime loop in the embedded model",
	})
}

// spec: 4.7.10 (Runtime process lifetime)
// diagnosis: the Runtime Process Lifetime section of
//
//	docs/runtime-author-guide/lifecycle.md no longer states that the adapter
//	closes the connection when the coordinator hold times out with no new
//	coordinator, or that a runtime process that stops, or whose connection the
//	hold timeout closed, is not reconnected and the pod is retired. A runtime
//	author then has no account of a connection that ends with every session on
//	it and no `session_end`.
func TestRuntimeProcessLifetimeNamesTheAdapterClosesOfTheConnection(t *testing.T) {
	page := readDocPage(t, filepath.Join(repoRoot(t), "docs", "runtime-author-guide", "lifecycle.md"))
	requirePhrases(t, "lifecycle.md Runtime Process Lifetime", section(page, "Runtime Process Lifetime"), []string{
		"The adapter closes the connection when the pod terminates",
		"when the coordinator hold times out with no new coordinator",
		"A runtime process that stops, or whose connection the hold timeout closed, is not re-created or reconnected inside the pod",
		"the pod is retired",
	})
}

// spec: 4.7.10 (Runtime process lifetime), 4.7.11 (Runtime connection
// handshake)
// diagnosis: the Runtime Process Lifetime section of
//
//	docs/runtime-author-guide/lifecycle.md states the no-reconnect rule for
//	every connection the adapter closes, or no longer says that a connection
//	closed before the first protocol frame is redialed. The specification
//	limits the no-reconnect rule to a stopped runtime process and a
//	hold-timeout close, and requires a runtime to re-read the manifest and dial
//	again after a handshake refusal. A runtime author who reads the page treats
//	a handshake refusal as final and loses the pod's connection.
func TestRuntimeProcessLifetimeScopesNoReconnectAndKeepsHandshakeRedial(t *testing.T) {
	page := readDocPage(t, filepath.Join(repoRoot(t), "docs", "runtime-author-guide", "lifecycle.md"))
	body := section(page, "Runtime Process Lifetime")
	if strings.Contains(body, "A connection the adapter closed is not re-established") {
		t.Errorf("lifecycle.md Runtime Process Lifetime: applies the no-reconnect rule to every connection the adapter closes")
	}
	requirePhrases(t, "lifecycle.md Runtime Process Lifetime", body, []string{
		"A connection the adapter closes before the first protocol frame",
		"read the manifest again and dial again",
		"(../reference/adapter-contract.md#connection-handshake)",
	})
}

// spec: 15.7 (Runtime author SDKs), 28.5.3 (CH-MSGSOCK Inbound: session_end)
// diagnosis: docs/runtime-author-guide/runtime-sdk.md no longer says that a
//
//	`shutdown` frame or the end of stdin ends every session the process holds
//	through OnTerminate after its queued messages finish, or no longer says
//	that only `session_end` discards a session's queued messages. A runtime
//	author then has no per-session cleanup trigger for a process-scoped
//	shutdown.
func TestRuntimeSDKPageNamesShutdownAndEndOfStdinAsOnTerminateTriggers(t *testing.T) {
	page := readDocPage(t, filepath.Join(repoRoot(t), "docs", "runtime-author-guide", "runtime-sdk.md"))
	requirePhrases(t, "runtime-sdk.md Sessions", section(page, "Sessions"), []string{
		"On a `shutdown` frame or the end of stdin, the SDK finishes each live session's queued messages and then calls `OnTerminate` for every session the process holds",
		"`stdin_closed`",
		"Only `session_end` discards the session's queued messages",
	})
}
