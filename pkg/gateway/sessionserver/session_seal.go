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
// is not, idleAge is the time since the adapter's last activity for it.
//
// The binding is marked sealed before the call, so a message written from
// then on is covered by the mark whichever of the message and the seal
// reaches the adapter first: when the adapter seals, it refuses the message,
// and when the message reaches the runtime first, the adapter counts it as
// activity and does not seal. When the adapter does not seal, or the call
// fails, the mark is cleared, unless the binding was already marked before
// the call, because a seal the adapter placed earlier stays in place when it
// answers not sealed. Every call opens its own seal epoch, also over an
// earlier mark, and a sealed answer records the grant for that epoch only, so
// a grant never covers a message written while an earlier grant was
// withdrawn. The executor reads the end of a turn's stream as a refusal
// only when the turn's message was written under a mark the adapter granted
// and that held until the end. A message written before the mark, or under
// a mark the adapter had not yet granted when the stream ended, may have
// reached the runtime, so it is never routed again.
// spec: §4.7.1 (Role and Gateway RPC Contract), §6.2 (maxClientIdleSeconds
// clock behavior across states)
func (s *Server) sealSession(ctx context.Context, bind *podsession.BindResult, idleSeconds int) (bool, time.Duration, error) {
	sealer := s.sealerOf(bind)
	if sealer == nil {
		return false, 0, errNoSealer
	}
	markedBefore := bind.Sealed()
	epoch := bind.MarkSealed()
	cctx, cancel := context.WithTimeout(ctx, sealRPCTimeout)
	defer cancel()
	sealed, idleAge, err := sealer.SealSession(cctx, bind.SessionID, bind.CoordinationGeneration, idleSeconds)
	switch {
	case err == nil && sealed:
		bind.GrantSealed(epoch)
	case !markedBefore:
		bind.ClearSealed()
	}
	return sealed, idleAge, err
}

// unsealSession lifts the session's seal through its runtime adapter, under
// the 5 s RPC deadline, at the binding's coordination generation, and clears
// the binding's seal mark when the adapter lifted it. A failed unseal leaves
// the mark, because the adapter may still refuse the session's messages, and
// withdraws the grant, because the adapter may instead have lifted the seal
// before the failure, so a message written afterwards can reach the runtime
// and its stream's end must not read as a refusal. The adapter's own
// session_sealed refusal still reports a refused message.
// spec: §4.7.1 (Role and Gateway RPC Contract)
func (s *Server) unsealSession(ctx context.Context, bind *podsession.BindResult) error {
	sealer := s.sealerOf(bind)
	if sealer == nil {
		return errNoSealer
	}
	cctx, cancel := context.WithTimeout(ctx, sealRPCTimeout)
	defer cancel()
	if err := sealer.UnsealSession(cctx, bind.SessionID, bind.CoordinationGeneration); err != nil {
		bind.WithdrawSealGrant()
		return err
	}
	bind.ClearSealed()
	return nil
}
