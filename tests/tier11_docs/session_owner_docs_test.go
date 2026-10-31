// SPDX-License-Identifier: MIT

// Tier-11 documentation checks for the reader-facing pages that describe who
// owns a session. The owner is the authenticated subject that created the
// session, no request field sets it, and a user reads another user's session
// only through a custom role a tenant-admin assigns. Each case pins the
// sentence or table row the specification fixes and names the retired
// wording it replaced. No external infrastructure.

package tier11_docs_test

import "testing"

// TestSecurityGuideUserRoleNamesCustomRoleGrant_spec_10_2 asserts the RBAC
// platform roles table of the security guide states that a user reads other
// users' sessions only through a custom role a tenant-admin assigns, and no
// longer refers to an undefined explicit grant.
//
// diagnosis: A failure means the user row still carries the retired
// "without explicit grant" wording, which names no mechanism, or names
// tenant-viewer as the grant, which replaces the user role and removes
// session creation.
//
// spec: 10.2 (Session ownership, user role bullet, permission matrix row
// "Read other users' sessions (same tenant)")
func TestSecurityGuideUserRoleNamesCustomRoleGrant_spec_10_2(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "operator-guide/security.md",
			row:        "| `user` | Per-tenant |",
			contains:   []string{"Create and manage own sessions", "read other users' sessions only through a custom role a tenant-admin assigns"},
			notContain: []string{"explicit grant", "tenant-viewer"},
		},
	})
}

// TestOpenAICompletionsUserParameterIsIgnored_spec_10_2 asserts the OpenAI
// completions reference describes the request user parameter as accepted and
// ignored, with the session owner taken from the authenticated caller.
//
// diagnosis: A failure means the parameter table still presents user as an
// end-user identifier, which tells a client it can set the session owner
// from the request body.
//
// spec: 10.2 (Session ownership)
func TestOpenAICompletionsUserParameterIsIgnored_spec_10_2(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "api/openai-completions.md",
			row:        "| `user` | string |",
			contains:   []string{"Accepted and ignored.", "The session owner is the authenticated caller."},
			notContain: []string{"End-user identifier for tracking"},
		},
	})
}

// TestClientSDKExamplesSendNoSessionOwner_spec_10_2 asserts the client SDK
// examples create a session without an owner field, because the client SDK
// request types carry none and the gateway binds the owner from the
// authenticated principal.
//
// diagnosis: A failure means an example still passes userId, UserID, or
// user_id to session creation, which no longer compiles against the SDK
// request types and suggests a client chooses the owner.
//
// spec: 10.2 (Session ownership), 15.1 (POST /v1/sessions)
func TestClientSDKExamplesSendNoSessionOwner_spec_10_2(t *testing.T) {
	runDocPageChecks(t, []docPageCheck{
		{
			page:       "client-guide/sdk-examples/typescript.md",
			notContain: []string{"userId:"},
		},
		{
			page:       "client-guide/sdk-examples/go.md",
			notContain: []string{"UserID:"},
		},
		{
			page:       "client-guide/sdk-examples/python.md",
			row:        "CreateSessionRequest(",
			contains:   []string{`runtime_ref="chat"`},
			notContain: []string{"user_id"},
		},
	})
}
