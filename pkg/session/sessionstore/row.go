// SPDX-License-Identifier: MIT

package sessionstore

import "github.com/lennylabs/lenny/pkg/session/sessionrow"

// The session row and the types it holds are declared in package
// sessionrow, which imports no tenancy or credential package, so a package
// that carries the row does not link the session store. Each alias below
// names the same type as its sessionrow counterpart, so the code that names
// the row through this package is unchanged.
type (
	// Session is the session row the store persists.
	Session = sessionrow.Session
	// ConversationResponse is one Open Responses turn recorded on the row.
	ConversationResponse = sessionrow.ConversationResponse
	// WebhookEventRecord is one session-terminal callback event that
	// exhausted its delivery retries.
	WebhookEventRecord = sessionrow.WebhookEventRecord
	// SessionTimeouts is the per-session timeout overrides a client
	// requested.
	SessionTimeouts = sessionrow.SessionTimeouts
	// CredentialPolicyOverride is the per-session credential policy
	// override a client requested.
	CredentialPolicyOverride = sessionrow.CredentialPolicyOverride
	// DelegationLeaseRequest is the delegation lease bounds a client
	// requested.
	DelegationLeaseRequest = sessionrow.DelegationLeaseRequest
	// SetupCommandOutput is one setup-command record retained on the row.
	SetupCommandOutput = sessionrow.SetupCommandOutput
	// DelegationLease is the lease slice persisted on the row.
	DelegationLease = sessionrow.DelegationLease
	// NodeAttributes is the per-node tracking projection of a row.
	NodeAttributes = sessionrow.NodeAttributes
	// FailureHistory is the per-node failure projection of a row.
	FailureHistory = sessionrow.FailureHistory
	// ExperimentContext is the experiment enrollment recorded on the row.
	ExperimentContext = sessionrow.ExperimentContext
	// WorkspaceSnapshot is the stored workspace artifact attached to the
	// row.
	WorkspaceSnapshot = sessionrow.WorkspaceSnapshot
	// WorkspaceSnapshotSource records how a workspace snapshot was
	// produced. Its values are declared in this package.
	WorkspaceSnapshotSource = sessionrow.WorkspaceSnapshotSource
)
