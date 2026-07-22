// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test pinning the §12.5 bootstrap_first_install suite's
// chat-runtime smoke promise: a fresh chart install, on top of reaching
// a Ready gateway (TestBootstrapFirstInstall in lifecycle_test.go), must
// also produce a working smoke session against the `chat` runtime
// within five minutes.
package tier5_e2e_kind_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// bootstrapChatSmokeTenant is the synthetic tenant this test
// bootstraps, per-run-suffixed for the same reason
// promptRoundtripTenant is in prompt_roundtrip_test.go.
const bootstrapChatSmokeTenant = "bootstrap-chat-smoke-tenant"

// spec: §12.5 (TESTING.md: "bootstrap_first_install | Fresh chart
// install with the default values produces a Ready gateway; smoke test
// against `chat` runtime succeeds within five minutes")
//
// diagnosis: once unskipped, a failure here means a fresh Lenny install
// cannot deliver the chat-runtime smoke session the bootstrap_first_install
// critical-path suite promises: either the five-minute budget was
// exceeded, or a live session against `chat` never returned a
// response. TestBootstrapFirstInstall in lifecycle_test.go covers the
// "Ready gateway" half of the same suite's promise; this test covers
// the "smoke test against chat runtime" half.
func TestBootstrapFirstInstallChatSmoke(t *testing.T) {
	// The chat reference runtime (github.com/lennylabs/runtime-chat) is
	// not vendored in this repo and ships no runnable image digest here
	// (tests/spec-map.json marks §26.7 blocked_until_phase 11): the
	// chart's default values.yaml pins `chat` to a placeholder
	// sha256:0000...0000 digest, so no Runtime CRD instance or warm
	// pool is applied for it and the live e2e Kind cluster used by this
	// suite has no `chat` Runtime registered. Unskip once a runnable
	// chat image (or an in-repo adapter implementing the §26.7
	// bootstrap contract) is registered with an applied Runtime CRD
	// and a warm pool, so a session against `chat` can actually be
	// created.
	t.Skip("no runnable chat reference-runtime image or in-repo adapter with a Runtime CRD and warm pool exists yet, so a fresh install has nothing to smoke-test a chat session against")

	d := sessiondriver.New(t)

	// TESTING.md's "within five minutes" is the budget for the smoke
	// session itself (against an install that already reached Ready),
	// not for cluster bring-up or chart install, which InstallLenny
	// already gates on above.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tenant := fmt.Sprintf("%s-%d", bootstrapChatSmokeTenant, time.Now().UnixNano())
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	sess, err := d.CreateAndStart(ctx, tenant, "chat")
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		t.Skipf("precondition not met: chat warm pool not ready, no session to smoke-test a message through: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start chat session: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.Terminate(ctx, tenant, sess.ID)
	})

	const prompt = "hello"
	msgResp, err := d.SendMessage(ctx, tenant, sess.ID, prompt)
	if err != nil {
		t.Fatalf("send message %q: %v", prompt, err)
	}
	if msgResp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("delivery receipt status = %q, want delivered (body: %s)",
			msgResp.DeliveryReceipt.Status, msgResp.Output)
	}
	assertNonEmptyResponsePart(t, "bootstrap chat smoke session response", msgResp.Output)
}
