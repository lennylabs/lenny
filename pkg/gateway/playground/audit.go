// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// AuditEvent is one §27.3.1 step-6 playground audit event. The
// playground emits playground.bearer_minted on every successful
// POST /v1/playground/token and playground.bearer_revoked on logout.
// The event shares the taxonomy and redaction rules of the other
// auth events in §11.7.
type AuditEvent struct {
	// Type is the §11.7 event type: playground.bearer_minted or
	// playground.bearer_revoked.
	Type string

	// UserID and TenantID attribute the event to the principal.
	UserID   string
	TenantID string

	// SessionCookieID is the opaque playground session id, empty in
	// apiKey and dev mode (which carry no server-side session).
	SessionCookieID string

	// BearerJTI is the jti of the minted or revoked bearer.
	BearerJTI string

	// BearerTTLSeconds is the TTL of the minted bearer.
	BearerTTLSeconds int64

	// Origin is always "playground".
	Origin string

	// At is the gateway clock instant the event occurred.
	At time.Time
}

// AuditEmitter receives playground audit events. The gateway wires an
// implementation backed by its §11.7 audit sink; a nil AuditEmitter
// disables emission. The contract is fire-and-forget: Emit must not
// block the request path.
type AuditEmitter interface {
	EmitPlaygroundEvent(ctx context.Context, event AuditEvent)
}

// WithAuditEmitter returns a copy of the handler that emits §27.3.1
// audit events through emitter. It is called by the gateway during
// wiring.
func (h *Handler) WithAuditEmitter(emitter AuditEmitter) *Handler {
	h.audit = emitter
	return h
}

// emitBearerMintedAudit emits the §27.3.1 step-6
// playground.bearer_minted event for a successful mint.
func (h *Handler) emitBearerMintedAudit(r *http.Request, tenant, user, jti string, ref *recordRef) {
	if h.audit == nil {
		return
	}
	cookieID := ""
	if ref != nil {
		cookieID = ref.id
	}
	h.audit.EmitPlaygroundEvent(r.Context(), AuditEvent{
		Type:             "playground.bearer_minted",
		UserID:           user,
		TenantID:         tenant,
		SessionCookieID:  cookieID,
		BearerJTI:        jti,
		BearerTTLSeconds: int64(h.cfg.BearerTTL / time.Second),
		Origin:           PlaygroundOrigin,
		At:               h.now(),
	})
}

// emitBearerRevokedAudit emits the §27.3.1 step-6
// playground.bearer_revoked event for a logout or revocation.
func (h *Handler) emitBearerRevokedAudit(ctx context.Context, tenant, user, cookieID string, jtis []string) {
	if h.audit == nil {
		return
	}
	for _, jti := range jtis {
		h.audit.EmitPlaygroundEvent(ctx, AuditEvent{
			Type:            "playground.bearer_revoked",
			UserID:          user,
			TenantID:        tenant,
			SessionCookieID: cookieID,
			BearerJTI:       jti,
			Origin:          PlaygroundOrigin,
			At:              h.now(),
		})
	}
}

// MemoryAuditEmitter is an in-process AuditEmitter that retains every
// emitted event. It backs the package tests and a gateway running
// without a durable audit sink.
type MemoryAuditEmitter struct {
	mu     sync.Mutex
	events []AuditEvent
}

// NewMemoryAuditEmitter returns an empty MemoryAuditEmitter.
func NewMemoryAuditEmitter() *MemoryAuditEmitter {
	return &MemoryAuditEmitter{}
}

var _ AuditEmitter = (*MemoryAuditEmitter)(nil)

// EmitPlaygroundEvent implements AuditEmitter.
func (m *MemoryAuditEmitter) EmitPlaygroundEvent(_ context.Context, event AuditEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

// Events returns a copy of every event emitted so far.
func (m *MemoryAuditEmitter) Events() []AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEvent(nil), m.events...)
}

// jtiCounter backs the monotonic component of newJTI.
var jtiCounter atomic.Uint64

// newJTI returns a unique JWT id for a minted playground bearer. The
// id combines the mint timestamp with a process-monotonic counter so
// two mints in the same nanosecond still receive distinct ids.
func newJTI(now time.Time) string {
	n := jtiCounter.Add(1)
	return "pgjti_" + now.UTC().Format("20060102T150405") + "_" + uint64Hex(n)
}

// uint64Hex renders n as a lowercase hex string with no leading
// zeros.
func uint64Hex(n uint64) string {
	const hex = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = hex[n&0xf]
		n >>= 4
	}
	return string(buf[i:])
}
