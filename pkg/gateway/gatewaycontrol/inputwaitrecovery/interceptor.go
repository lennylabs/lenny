// SPDX-License-Identifier: MIT

package inputwaitrecovery

import (
	"context"
	"crypto/x509"
	"errors"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/mtls/spiffe"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// SessionReader is the session read the recovery takes: the call's own row
// and the sessions holding the connection's pod.
type SessionReader interface {
	GetByID(ctx context.Context, id string) (sessionstore.Session, error)
	GetActiveSlotsByPod(ctx context.Context, podID string) ([]string, error)
}

// Recoverer runs the connection recovery for a LNK-GWCONTROL connection.
type Recoverer struct {
	// Sessions reads the call's row and lists the pod's sessions.
	Sessions SessionReader
	// Writer applies the recovery write to each listed session.
	Writer Writer
}

// sessionScoped is a GatewayControl request that names a session.
type sessionScoped interface {
	GetSessionId() *adapterv1.SessionId
}

// UnaryInterceptor runs the recovery before it dispatches a call whose
// request carries a session ID, on a connection whose peer presents a
// verified agent SPIFFE identity and whose record is not recovered. The call
// is dispatched whatever the recovery's outcome; a run that does not complete
// leaves the record not recovered, so the connection's next such call runs it
// again.
// spec: §7.2 (Session state machine), §28.3 (Link register).
func (r *Recoverer) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		rec, ok := FromContext(ctx)
		if !ok || rec.Recovered() {
			return handler(ctx, req)
		}
		scoped, ok := req.(sessionScoped)
		if !ok || scoped.GetSessionId().GetValue() == "" {
			return handler(ctx, req)
		}
		pod, ok := agentPodName(ctx)
		if !ok {
			return handler(ctx, req)
		}
		if err := r.Run(ctx, rec, scoped.GetSessionId().GetValue(), pod); err != nil {
			slog.WarnContext(ctx, "input-wait connection recovery incomplete",
				"session_id", scoped.GetSessionId().GetValue(), "pod", pod, "error", err)
		}
		return handler(ctx, req)
	}
}

// errForeignPod reports a call whose row names a pod other than the peer's.
var errForeignPod = errors.New("inputwaitrecovery: the call's session is not assigned to the peer's pod")

// Run performs one recovery run for the connection whose record is rec and
// whose peer is pod. The request's session ID is the pod's own claim, so the
// run continues only when that row's PodAssignment equals pod, and then
// writes each of the pod's sessions under that row's tenant (§5.2 tenant
// pinning). When every read and write returned no error other than
// sessionstore.ErrSuperseded, it raises the record's recovered generation to
// the replica generation the run started at.
// spec: §7.2 (Session state machine), §28.3 (Link register), §5.2 (Tenant
// pinning).
func (r *Recoverer) Run(ctx context.Context, rec *Record, sessionID, pod string) error {
	gen := rec.gen.Load()
	row, err := r.Sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if row.PodAssignment == "" || row.PodAssignment != pod {
		return errForeignPod
	}
	ids, err := r.Sessions.GetActiveSlotsByPod(ctx, pod)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := r.Writer.Recover(ctx, row.TenantID, id); err != nil && !errors.Is(err, sessionstore.ErrSuperseded) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	rec.markRecovered(gen)
	return nil
}

// agentPodName returns the pod name of the peer's verified agent SPIFFE
// identity. A peer with no TLS, no verified chain, no SPIFFE URI SAN, or a
// non-agent identity has none, and the recovery does not run for it.
func agentPodName(ctx context.Context) (string, bool) {
	pr, ok := peer.FromContext(ctx)
	if !ok || pr == nil {
		return "", false
	}
	info, ok := pr.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 || len(info.State.VerifiedChains[0]) == 0 {
		return "", false
	}
	uri, ok := spiffeURI(info.State.VerifiedChains[0][0])
	if !ok {
		return "", false
	}
	id, err := spiffe.Parse(uri)
	if err != nil || id.Kind != spiffe.KindAgent {
		return "", false
	}
	return id.PodName, true
}

// spiffeURI returns the first spiffe:// URI SAN on the certificate.
func spiffeURI(cert *x509.Certificate) (string, bool) {
	for _, u := range cert.URIs {
		if u != nil && u.Scheme == spiffe.Scheme {
			return u.String(), true
		}
	}
	return "", false
}
