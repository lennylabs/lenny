// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// sealRPCTimeout is the §4.7.1 deadline of the SealSession and UnsealSession
// RPCs. spec: §4.7.1 (Role and Gateway RPC Contract), §28.5.1 (CH-ATTACH).
const sealRPCTimeout = 5 * time.Second

// sessionSealer is the runtime adapter's session seal, consumed here so a
// unit test substitutes a fake for the adapter client. *adapterclient.Client
// satisfies it.
// spec: §4.7.1 (Role and Gateway RPC Contract)
type sessionSealer interface {
	SealSession(ctx context.Context, sessionID string, generation int64, idleSeconds int) (sealed bool, idleAge time.Duration, err error)
	UnsealSession(ctx context.Context, sessionID string, generation int64) error
}

// errNoSealer is the error of a seal call for a binding that carries no
// adapter connection.
var errNoSealer = errors.New("sessionserver: the binding carries no adapter connection to seal through")

// defaultSealerFor returns the binding's adapter client, or an untyped nil
// when the binding carries none, so a nil check on the result is exact.
func defaultSealerFor(bind *podsession.BindResult) sessionSealer {
	if bind == nil || bind.Adapter == nil {
		return nil
	}
	return bind.Adapter
}

// sealerOf resolves the sealer for bind through s.sealerFor, which a test
// replaces, or the default.
func (s *Server) sealerOf(bind *podsession.BindResult) sessionSealer {
	if s.sealerFor != nil {
		return s.sealerFor(bind)
	}
	return defaultSealerFor(bind)
}

// sealSession asks the session's runtime adapter to seal the session when it
// had no activity in the last idleSeconds (§4.7.1 Session seal.). The call
// names the binding's session and coordination generation and runs under
// the 5 s RPC deadline. sealed reports whether the session is sealed; when it
// is not, idleAge is the time since the adapter's last activity for it. A
// seal marks the binding sealed, so a message whose stream a release ends
// while the seal holds is routed again as a refused message.
// spec: §4.7.1 (Role and Gateway RPC Contract), §6.2 (maxClientIdleSeconds
// clock behavior across states)
func (s *Server) sealSession(ctx context.Context, bind *podsession.BindResult, idleSeconds int) (bool, time.Duration, error) {
	sealer := s.sealerOf(bind)
	if sealer == nil {
		return false, 0, errNoSealer
	}
	cctx, cancel := context.WithTimeout(ctx, sealRPCTimeout)
	defer cancel()
	sealed, idleAge, err := sealer.SealSession(cctx, bind.SessionID, bind.CoordinationGeneration, idleSeconds)
	if err == nil && sealed {
		bind.MarkSealed()
	}
	return sealed, idleAge, err
}

// unsealSession lifts the session's seal through its runtime adapter, under
// the 5 s RPC deadline, at the binding's coordination generation, and clears
// the binding's seal mark when the adapter lifted it. A failed unseal leaves
// the mark, because the adapter may still refuse the session's messages.
// spec: §4.7.1 (Role and Gateway RPC Contract)
func (s *Server) unsealSession(ctx context.Context, bind *podsession.BindResult) error {
	sealer := s.sealerOf(bind)
	if sealer == nil {
		return errNoSealer
	}
	cctx, cancel := context.WithTimeout(ctx, sealRPCTimeout)
	defer cancel()
	if err := sealer.UnsealSession(cctx, bind.SessionID, bind.CoordinationGeneration); err != nil {
		return err
	}
	bind.ClearSealed()
	return nil
}
