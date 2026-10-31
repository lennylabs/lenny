// SPDX-License-Identifier: MIT

package lifecycleharness

import (
	"context"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// WireOpenResponses installs the production Open Responses translator
// handler as POST /v1/responses on every replica, on its public path and on
// its CH-HOLDERFORWARD listener, through SetResponsesHandler. Each replica's
// handler is wired as the gateway wires it: the binder runs the replica's
// create-and-start service, the holder forwarder adapts the replica's session
// server as the gateway's forwarder adapter does, and the replica's session
// server is the session terminal. The replicas share one transcript store, as
// the replicas of one deployment share one.
//
// hold is the binder's hold decision for every session it binds. The
// gateway's binder adapter derives it from a runtime registry the harness
// does not seed, so the scenario fixes it.
//
// spec: §15 (Open Responses conversation sessions), §29.3 (Interactive
// message send) Forwarding
func (h *Harness) WireOpenResponses(hold bool) {
	transcripts := transcriptstore.NewMemory()
	for _, r := range h.Replicas {
		handler := translator.NewOpenResponsesHandler(h.Store, r.Executor, translator.OpenResponsesOptions{
			SingleShotBinder: responsesBinder{h: h, srv: r.Server, hold: hold},
			DefaultRuntime:   RuntimeRef,
			Transcripts:      transcripts,
			HolderForwarder:  responsesForwarder{srv: r.Server},
			Terminal:         r.Server,
			DevTenantHeader:  true,
		})
		r.SetResponsesHandler(handler.Handler())
	}
}

// responsesBinder runs the replica's create-and-start service and reports
// hold, as the gateway's binder adapter does for a runtime whose hold
// decision is hold. It tracks each session it binds for the harness's
// cleanup.
type responsesBinder struct {
	h    *Harness
	srv  *sessionserver.Server
	hold bool
}

// BindSingleShot creates and starts the session and maps a session-server
// refusal into the translator's binder error.
func (b responsesBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (translator.SingleShotBinding, error) {
	resp, serr := b.srv.CreateAndStartService(ctx, spec.TenantID, sessionserver.CreateSessionRequest{
		RuntimeRef: spec.RuntimeRef, UserID: spec.UserID, Environment: spec.Environment,
	})
	if serr != nil {
		return translator.SingleShotBinding{}, &translator.SingleShotError{
			HTTPStatus: serr.HTTPStatus, Code: serr.Code, Message: serr.Message,
			RetryAfterSeconds: serr.RetryAfterSeconds, Retryable: serr.Retryable,
		}
	}
	b.h.Track(resp.ID)
	return translator.SingleShotBinding{SessionID: resp.ID, RecoveryGeneration: resp.RecoveryGeneration, Hold: b.hold}, nil
}

// responsesForwarder adapts the replica's session server to the translator's
// holder forwarder as the gateway's adapter does: a warm turn forwards to the
// session's coordinating replica over CH-HOLDERFORWARD and is local exactly
// when the session server reports ForwardLocal.
//
// spec: §29.3 (Interactive message send) Forwarding
type responsesForwarder struct{ srv *sessionserver.Server }

// ForwardResponsesTurn forwards the turn and reports whether this replica
// serves it.
func (f responsesForwarder) ForwardResponsesTurn(w http.ResponseWriter, r *http.Request, row sessionstore.Session, body []byte,
	refuse func(http.ResponseWriter, string),
) bool {
	return f.srv.ForwardResponsesTurn(w, r, row, body, refuse) == sessionserver.ForwardLocal
}

// Forwarded reports whether the request arrived forwarded from another
// replica.
func (responsesForwarder) Forwarded(ctx context.Context) bool { return sessionserver.Forwarded(ctx) }
