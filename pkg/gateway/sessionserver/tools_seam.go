// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/environment/environmentmw"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/messagerouting"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// The methods below are the session core's side of the seam operations the
// platform tools seam calls by value on ToolsSeam: the session lookup and
// the ownership rule, the tool call forwarding, the child session
// operations, the message delivery operations, the pod file export, and the
// tree budget expiry, with the in-process client API, which runs through
// the API front's request pipeline that SetServiceHandler hands it. An
// operation that runs the core's logic on a stored row reads the row from
// the session record's Stored field, which the platform tools seam fills
// with the row it read or wrote, and returns the row the core's logic
// produced in the same field.

// storedSession returns the session record of a stored row, carrying the
// row in its Stored field.
func storedSession(row sessionstore.Session) seam.Session {
	out := seamSessionOf(row)
	out.Stored = row
	return out
}

// rowOf returns the stored row a session record carries, or the row its
// fields describe when it carries none.
func rowOf(s seam.Session) sessionstore.Session {
	if row, ok := storedRow(s); ok {
		return row
	}
	return recordRow(s)
}

// storedRow returns the stored row a session record carries when the row is
// the session the record names: the same identifier and tenant. A record
// whose row names another session or tenant is treated as carrying none, so
// the core acts on the session the record's fields name (decision 52 of
// change 0117).
func storedRow(rec seam.Session) (sessionstore.Session, bool) {
	row, ok := rec.Stored.(sessionstore.Session)
	if !ok || row.ID != rec.ID || row.TenantID != rec.TenantID {
		return sessionstore.Session{}, false
	}
	return row, true
}

// sentinelError is a core error that also matches a seam sentinel under
// errors.Is. Its message is the core's, so a caller that reports the error
// as it came reports the text it reported before the seam carried it.
type sentinelError struct {
	sentinel error
	msg      string
	err      error
}

func (e *sentinelError) Error() string { return e.msg }

// Unwrap returns the seam sentinel and the core's error.
func (e *sentinelError) Unwrap() []error { return []error{e.sentinel, e.err} }

// withSentinel wraps err so it matches sentinel, with the message msg.
func withSentinel(sentinel error, msg string, err error) error {
	return &sentinelError{sentinel: sentinel, msg: msg, err: err}
}

// LookupSession is GWSEAM-LookupSession, served by the Server method the seams share.
//
// spec: GWSEAM-LookupSession
func (t ToolsSeam) LookupSession(ctx context.Context, tenantID, sessionID string) (seam.Session, error) {
	return t.s.lookupSeamSession(ctx, tenantID, sessionID)
}

// AuthorizeSession is GWSEAM-AuthorizeSession, served by the Server method
// the seams share.
//
// spec: GWSEAM-AuthorizeSession
func (t ToolsSeam) AuthorizeSession(ctx context.Context, tenantID, sessionID string, action seam.SessionAction) (seam.Session, error) {
	return t.s.authorizeSeamSession(ctx, tenantID, sessionID, action)
}

// SessionAccessible applies the §10.2 ownership rule to a session record
// the platform tools seam read: a read for SessionRead, an action for
// SessionManage. An unknown action is refused.
//
// spec: GWSEAM-SessionAccessible; §10.2 (Session ownership) rules 3, 4, 5,
// and 7.
func (t ToolsSeam) SessionAccessible(ctx context.Context, s seam.Session, action seam.SessionAction) bool {
	switch action {
	case seam.SessionRead:
		return t.s.SessionAccessible(ctx, rowOf(s), false)
	case seam.SessionManage:
		return t.s.SessionAccessible(ctx, rowOf(s), true)
	default:
		return false
	}
}

// toolForwardKinds maps the seam's forward kinds of tool calls to the
// core's forwarding rows.
var toolForwardKinds = map[seam.ForwardKind]ForwardKind{
	seam.ForwardTerminate:        ForwardKindTerminate,
	seam.ForwardMessage:          ForwardKindMessage,
	seam.ForwardMessageNoReplyTo: ForwardKindMessageNoReplyTo,
}

// ForwardToolCall forwards a tool call on the session record's stored row
// under the forwarding row of kind, through Server.ForwardToolCall. A kind
// the core has no row for is a wiring defect: it is refused with
// INTERNAL_ERROR and not forwarded.
//
// spec: GWSEAM-ForwardToolCall
func (t ToolsSeam) ForwardToolCall(ctx context.Context, s seam.Session, kind seam.ForwardKind, tool string, arguments json.RawMessage) (seam.ToolResult, seam.ForwardOutcome, error) {
	k, ok := toolForwardKinds[kind]
	if !ok {
		return seam.ToolResult{}, seam.ForwardNotDelivered, &seam.Refusal{
			Code: "INTERNAL_ERROR", Message: "unknown forward kind " + strconv.Quote(string(kind)),
		}
	}
	res, outcome, err := t.s.ForwardToolCall(ctx, rowOf(s), k, tool, arguments)
	return res, seamForwardOutcome(outcome), err
}

// Forwarded reports whether the request the context carries arrived on the
// forward listener.
//
// spec: GWSEAM-Forwarded
func (t ToolsSeam) Forwarded(ctx context.Context) bool {
	return Forwarded(ctx)
}

// HoldsUndelivered reports whether a message of kind whose forward was not
// delivered is held, for the session record's stored row. A kind the core
// has no row for holds nothing.
//
// spec: GWSEAM-HoldsUndelivered
func (t ToolsSeam) HoldsUndelivered(s seam.Session, kind seam.ForwardKind) bool {
	k, ok := toolForwardKinds[kind]
	return ok && HoldsUndelivered(rowOf(s), k)
}

// CreateSession decodes a client API create body and runs the shared
// creation service on it. A body that does not decode, or that names no
// runtime, is refused with VALIDATION_ERROR; a body that names no
// environment takes the explicit environment the request's context names;
// and a refusal of the creation service comes back with its code, message,
// and details.
//
// spec: GWSEAM-CreateSession; §15.2.1 rules 1 and 4.
func (t ToolsSeam) CreateSession(ctx context.Context, tenantID string, body json.RawMessage) (seam.CreatedSession, error) {
	var in CreateSessionRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return seam.CreatedSession{}, &seam.Refusal{Code: "VALIDATION_ERROR", Message: fmt.Sprintf("invalid arguments: %v", err)}
	}
	if in.RuntimeRef == "" {
		return seam.CreatedSession{}, &seam.Refusal{
			Code: "VALIDATION_ERROR", Message: "runtimeRef is required",
			Details: map[string]any{"field": "runtimeRef"},
		}
	}
	if in.Environment == "" {
		in.Environment = environmentmw.ExplicitEnvironmentFromContext(ctx)
	}
	resp, svcErr := t.s.CreateSessionService(ctx, tenantID, in)
	if svcErr != nil {
		return seam.CreatedSession{}, &seam.Refusal{Code: svcErr.Code, Message: svcErr.Message, Details: svcErr.Details}
	}
	return seam.CreatedSession{ID: resp.ID, State: state.State(resp.State), UploadToken: resp.UploadToken}, nil
}

// MaterializeChild materializes a delegated child through
// MaterializeDelegatedChild. A credential assignment that failed after the
// pod claim matches seam.ErrChildCredentialAssignment, and a pool still
// warming matches seam.ErrPoolWarming; each keeps the core's message and
// its error chain.
//
// spec: GWSEAM-MaterializeChild
func (t ToolsSeam) MaterializeChild(ctx context.Context, tenantID, childID string) (state.State, error) {
	st, err := t.s.MaterializeDelegatedChild(ctx, tenantID, childID)
	if err == nil {
		return state.State(st), nil
	}
	var credAssign *podsession.CredentialAssignmentError
	var warming *podsession.PoolWarmingError
	switch {
	case errors.As(err, &credAssign):
		return state.State(st), withSentinel(seam.ErrChildCredentialAssignment, err.Error(), err)
	case errors.As(err, &warming):
		return state.State(st), withSentinel(seam.ErrPoolWarming, warming.Error(), err)
	default:
		return state.State(st), err
	}
}

// CheckChildCredentials runs the delegation-time credential check. An
// exhausted pool matches seam.ErrChildCredentialUnavailable and a missing
// user credential seam.ErrChildUserCredentialNotFound.
//
// spec: GWSEAM-CheckChildCredentials
func (t ToolsSeam) CheckChildCredentials(ctx context.Context, q seam.ChildCredentialQuery) error {
	err := t.s.CheckDelegationCredentialAvailability(ctx, DelegationCredentialQuery{
		TenantID:                  q.TenantID,
		UserID:                    q.UserID,
		ChildRuntimeRef:           q.ChildRuntimeRef,
		CredentialOriginSessionID: q.CredentialOriginSessionID,
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrDelegationCredentialUnavailable):
		return withSentinel(seam.ErrChildCredentialUnavailable, err.Error(), err)
	case errors.Is(err, ErrDelegationUserCredentialNotFound):
		return withSentinel(seam.ErrChildUserCredentialNotFound, err.Error(), err)
	default:
		return err
	}
}

// ApplyExperimentRouting routes the child's stored row through the
// experiment router and returns the routed row in the record.
//
// spec: GWSEAM-ApplyExperimentRouting
func (t ToolsSeam) ApplyExperimentRouting(ctx context.Context, child *seam.Session) error {
	row := rowOf(*child)
	err := t.s.ApplyExperimentRouting(ctx, &row)
	*child = storedSession(row)
	return err
}

// EffectiveMaxRetries resolves the retry budget of the session record's
// stored row against the deployer caps.
//
// spec: GWSEAM-EffectiveMaxRetries
func (t ToolsSeam) EffectiveMaxRetries(s seam.Session) int {
	return t.s.EffectiveMaxRetries(rowOf(s))
}

// InvokeClientAPI runs one client API request in process through
// ServiceCall. A refusal comes back as a result whose status is outside
// 2xx, with the error code, message, and details of the refusal.
//
// spec: GWSEAM-InvokeClientAPI
func (t ToolsSeam) InvokeClientAPI(ctx context.Context, call seam.ClientAPICall) (seam.ClientAPIResult, error) {
	res, svcErr := t.s.ServiceCall(ctx, call.TenantID, call.Method, call.Target, call.Body, call.ContentType, call.Headers)
	if svcErr != nil {
		return seam.ClientAPIResult{
			Status:       svcErr.HTTPStatus,
			ErrorCode:    svcErr.Code,
			ErrorMessage: svcErr.Message,
			ErrorDetails: svcErr.Details,
		}, nil
	}
	return seam.ClientAPIResult{Status: res.Status, Body: res.Body, ContentType: res.ContentType}, nil
}

// SendMessages delivers messages through the session server's executor. A
// session sealed against delivery returns a *seam.SealedError with the
// count it accepted and its response in the error chain, and a session with no binding on
// this replica matches seam.ErrNotBound with the executor's message.
//
// spec: GWSEAM-SendMessages
func (t ToolsSeam) SendMessages(ctx context.Context, sessionID string, messages []seam.Message) (seam.Response, error) {
	if t.s.executor == nil {
		return seam.Response{}, errors.New("no executor configured")
	}
	in := make([]executor.Message, 0, len(messages))
	for _, m := range messages {
		in = append(in, executor.Message{
			ID: m.ID, Role: m.Role, Content: m.Content,
			From: executor.MessageFrom{Kind: m.FromKind, ID: m.FromID},
		})
	}
	resp, err := t.s.executor.Send(ctx, sessionID, in)
	if err != nil {
		return seam.Response{}, seamDeliveryError(err)
	}
	return seamResponse(resp), nil
}

// seamDeliveryError maps a delivery error of the executor or of the held
// delivery to the seam's: a partial sealed delivery to an error that holds
// a *seam.SealedError, and a sealed session, a session with no binding, and
// a held delivery another replica took over to errors that match the seam's
// sentinels. Each keeps the core's message.
func seamDeliveryError(err error) error {
	var sealed *executor.SealedError
	switch {
	case errors.As(err, &sealed):
		return withSentinel(&seam.SealedError{Delivered: sealed.Delivered, Response: seamResponse(sealed.Response)}, err.Error(), err)
	case errors.Is(err, executor.ErrSessionSealed):
		return withSentinel(seam.ErrSessionSealed, err.Error(), err)
	case errors.Is(err, executor.ErrNotBound):
		return withSentinel(seam.ErrNotBound, err.Error(), err)
	case errors.Is(err, ErrHeldDeliveryPreempted):
		return withSentinel(seam.ErrHeldDeliveryPreempted, err.Error(), err)
	default:
		return err
	}
}

// seamResponse returns the seam's form of an executor response.
func seamResponse(r executor.Response) seam.Response {
	out := seam.Response{Annotations: r.Annotations}
	if r.Parts != nil {
		out.Parts = make([]seam.MessagePart, 0, len(r.Parts))
	}
	for _, p := range r.Parts {
		out.Parts = append(out.Parts, seam.MessagePart{
			Type: p.Type, Text: p.Text, Ref: p.Ref, SchemaVersion: p.SchemaVersion, Annotations: p.Annotations,
		})
	}
	return out
}

// inboxMessage returns the core's form of a held message.
func inboxMessage(m seam.InboxMessage) sessioninbox.Message {
	return sessioninbox.Message{
		MessageID:       m.MessageID,
		SenderSessionID: m.SenderSessionID,
		Payload:         m.Payload,
		EnqueuedAt:      m.EnqueuedAt,
	}
}

// EnqueueInbox places a message in the session's inbox through the
// messaging coordinator, and returns the message it evicted for room.
//
// spec: GWSEAM-EnqueueInbox
func (t ToolsSeam) EnqueueInbox(ctx context.Context, tenantID, sessionID string, msg seam.InboxMessage) (*seam.InboxMessage, error) {
	if t.s.messaging == nil {
		return nil, sessioninbox.ErrMessagingUnavailable
	}
	evicted, err := t.s.messaging.EnqueueInbox(ctx, tenantID, sessionID, inboxMessage(msg))
	if err != nil || evicted == nil {
		return nil, err
	}
	return &seam.InboxMessage{
		MessageID:       evicted.MessageID,
		SenderSessionID: evicted.SenderSessionID,
		Payload:         evicted.Payload,
		EnqueuedAt:      evicted.EnqueuedAt,
	}, nil
}

// LockDelivery takes this replica's delivery lock for the session.
//
// spec: GWSEAM-LockDelivery
func (t ToolsSeam) LockDelivery(ctx context.Context, sessionID string) (func(), error) {
	return t.s.LockDelivery(ctx, sessionID)
}

// seamHoldOutcome maps the core's hold outcome to the seam's.
func seamHoldOutcome(o HoldOutcome) seam.HoldOutcome {
	switch o {
	case HoldTerminal:
		return seam.HoldTerminal
	case HoldDropped:
		return seam.HoldDropped
	default:
		return seam.HoldHeld
	}
}

// HoldMessage holds a message in the session's DLQ through HoldMessage.
//
// spec: GWSEAM-HoldMessage
func (t ToolsSeam) HoldMessage(ctx context.Context, tenantID, sessionID string, msg seam.InboxMessage) (seam.HoldOutcome, state.State, error) {
	o, st, err := t.s.HoldMessage(ctx, tenantID, sessionID, inboxMessage(msg))
	return seamHoldOutcome(o), state.State(st), err
}

// HoldUndeliveredForward holds a message whose forward was not delivered,
// for the session record's stored row.
//
// spec: GWSEAM-HoldUndeliveredForward
func (t ToolsSeam) HoldUndeliveredForward(ctx context.Context, s seam.Session, msg seam.InboxMessage) (seam.HoldOutcome, state.State, error) {
	o, st, err := t.s.HoldUndeliveredForward(ctx, rowOf(s), inboxMessage(msg))
	return seamHoldOutcome(o), state.State(st), err
}

// DeliverHeld delivers the held messages of the session record's stored
// row, and maps its errors as SendMessages does.
//
// spec: GWSEAM-DeliverHeld
func (t ToolsSeam) DeliverHeld(ctx context.Context, s seam.Session) error {
	if err := t.s.DeliverHeld(ctx, rowOf(s)); err != nil {
		return seamDeliveryError(err)
	}
	return nil
}

// ResumeSuspended resumes the session record's stored row for a delivery.
//
// spec: GWSEAM-ResumeSuspended
func (t ToolsSeam) ResumeSuspended(ctx context.Context, s seam.Session) error {
	return t.s.ResumeSuspended(ctx, rowOf(s))
}

// AdmitResume checks a resume of the session record's stored row against
// the tenant's limits. A cap refusal comes back as a *seam.CapRefusal with
// its message and details.
//
// spec: GWSEAM-AdmitResume
func (t ToolsSeam) AdmitResume(ctx context.Context, s seam.Session) error {
	err := t.s.AdmitResume(ctx, rowOf(s))
	var refusal *CapRefusal
	if errors.As(err, &refusal) {
		return &seam.CapRefusal{Message: refusal.Message(), Details: refusal.Details()}
	}
	return err
}

// RouteMessage classifies a message from another session under the §7.2
// delivery paths.
//
// spec: GWSEAM-RouteMessage; §7.2 (Message delivery routing — seven paths)
func (t ToolsSeam) RouteMessage(st state.State, inputRequired bool) seam.MessageRoute {
	decision := messagerouting.Classify(session.State(st), inputRequired, false, messagerouting.SourceInterSession)
	switch decision.Action {
	case messagerouting.ActionRejectTerminal:
		return seam.RouteTerminal
	case messagerouting.ActionBufferInbox:
		return seam.RouteInbox
	case messagerouting.ActionBufferDLQ:
		return seam.RouteHold
	case messagerouting.ActionResumeAndDeliver:
		return seam.RouteResume
	default:
		return seam.RouteDeliver
	}
}

// ExportPaths runs the export RPC of each specification against the
// adapter client of the session's binding on this replica. A session with no binding here returns seam.ErrNoLiveBinding.
//
// spec: GWSEAM-ExportPaths
func (t ToolsSeam) ExportPaths(ctx context.Context, sessionID string, specs []seam.ExportSpec) ([]seam.ExportedFile, error) {
	if t.s.podRegistry == nil {
		return nil, seam.ErrNoLiveBinding
	}
	bind, ok := t.s.podRegistry.Get(sessionID)
	if !ok || bind == nil || bind.Adapter == nil {
		return nil, seam.ErrNoLiveBinding
	}
	in := make([]adapterclient.ExportSpec, 0, len(specs))
	for _, sp := range specs {
		in = append(in, adapterclient.ExportSpec{Source: sp.Source, DestPrefix: sp.DestPrefix})
	}
	files, err := bind.Adapter.ExportPaths(ctx, sessionID, in)
	if err != nil {
		return nil, err
	}
	out := make([]seam.ExportedFile, 0, len(files))
	for _, f := range files {
		out = append(out, seam.ExportedFile{Path: f.Path, Content: f.Content, SHA256: f.SHA256, Size: f.Size})
	}
	return out, nil
}

// HandleBudgetKeysExpired runs the tree cleanup of a delegation tree whose
// budget keys expired, through Server.HandleBudgetKeysExpired.
//
// spec: GWSEAM-HandleBudgetKeysExpired
func (t ToolsSeam) HandleBudgetKeysExpired(ctx context.Context, tenantID, rootSessionID string) {
	t.s.HandleBudgetKeysExpired(ctx, tenantID, rootSessionID)
}

// The interfaces ToolsSeam serves to the platform tools seam's calls by
// value; tools_seam_core.go asserts the terminal pipeline and the tree node
// operations.
var (
	_ seam.Sessions         = ToolsSeam{}
	_ seam.SessionAccess    = ToolsSeam{}
	_ seam.Forwarding       = ToolsSeam{}
	_ seam.ChildSessions    = ToolsSeam{}
	_ seam.ClientAPI        = ToolsSeam{}
	_ seam.MessageDelivery  = ToolsSeam{}
	_ seam.PodFiles         = ToolsSeam{}
	_ seam.TreeBudgetExpiry = ToolsSeam{}
)
