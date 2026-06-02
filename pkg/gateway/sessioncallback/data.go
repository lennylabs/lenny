// SPDX-License-Identifier: MIT

package sessioncallback

import "encoding/json"

// §16.6 short names that the §14 callback delivers. The dev.lenny.<name>
// CloudEvents type is derived from these.
const (
	EventSessionCompleted      = "session_completed"
	EventSessionFailed         = "session_failed"
	EventSessionTerminated     = "session_terminated"
	EventSessionCancelled      = "session_cancelled"
	EventSessionExpired        = "session_expired"
	EventSessionAwaitingAction = "session_awaiting_action"
	EventDelegationCompleted   = "delegation_completed"
)

// Usage is the §14 per-event token-usage block.
type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
}

// SessionInfo carries the fields the §14 line 142-148 per-event data
// schemas draw from. The caller fills only the fields the target event
// type names; the builders read the relevant subset.
type SessionInfo struct {
	SessionID       string
	ParentSessionID string
	ChildSessionID  string
	Usage           Usage
	Artifacts       []string
	ErrorCode       string
	ErrorMessage    string
	Reason          string
	TerminatedBy    string
	ExpiryReason    string
	ActionRequired  string
	ResumeURL       string
	ChildStatus     string
}

// CompletedData builds the §14 line 142 dev.lenny.session_completed data.
func CompletedData(info SessionInfo) json.RawMessage {
	arts := info.Artifacts
	if arts == nil {
		arts = []string{}
	}
	return mustJSON(map[string]any{
		"session_id": info.SessionID,
		"status":     "completed",
		"usage":      info.Usage,
		"artifacts":  arts,
	})
}

// FailedData builds the §14 line 143 dev.lenny.session_failed data.
func FailedData(info SessionInfo) json.RawMessage {
	return mustJSON(map[string]any{
		"session_id": info.SessionID,
		"status":     "failed",
		"error": map[string]any{
			"code":    info.ErrorCode,
			"message": info.ErrorMessage,
		},
		"usage": info.Usage,
	})
}

// TerminatedData builds the §14 line 144 dev.lenny.session_terminated data.
func TerminatedData(info SessionInfo) json.RawMessage {
	return mustJSON(map[string]any{
		"session_id":   info.SessionID,
		"reason":       info.Reason,
		"terminatedBy": info.TerminatedBy,
	})
}

// CancelledData builds the §14 line 145 dev.lenny.session_cancelled data.
func CancelledData(info SessionInfo) json.RawMessage {
	return mustJSON(map[string]any{
		"session_id": info.SessionID,
		"reason":     info.Reason,
	})
}

// ExpiredData builds the §14 line 146 dev.lenny.session_expired data.
func ExpiredData(info SessionInfo) json.RawMessage {
	return mustJSON(map[string]any{
		"session_id":   info.SessionID,
		"expiryReason": info.ExpiryReason,
	})
}

// AwaitingActionData builds the §14 line 147 dev.lenny.session_awaiting_action data.
func AwaitingActionData(info SessionInfo) json.RawMessage {
	return mustJSON(map[string]any{
		"session_id":     info.SessionID,
		"actionRequired": info.ActionRequired,
		"resumeUrl":      info.ResumeURL,
	})
}

// DelegationCompletedData builds the §14 line 148 dev.lenny.delegation_completed data.
func DelegationCompletedData(info SessionInfo) json.RawMessage {
	return mustJSON(map[string]any{
		"parent_session_id": info.ParentSessionID,
		"childSessionId":    info.ChildSessionID,
		"status":            info.ChildStatus,
		"usage":             info.Usage,
	})
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
