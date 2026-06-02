// SPDX-License-Identifier: MIT

// Package opsidem implements the §25.4 lenny-ops idempotency middleware.
// It is distinct from the gateway's §11.5 idempotency (which is keyed by
// (tenant, key) and passes through when no key is present): the §25.4
// control-plane contract keys records by (key, caller_id) where caller_id
// is the OIDC sub claim, classifies a small set of non-convergent Tier
// 2/3 endpoints as requiring the header (returning 400 when omitted), and
// fails closed (503) on a store outage for those required endpoints
// rather than silently proceeding.
//
// spec: §25.4 lines 2011-2130 ("Idempotency").
package opsidem

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// HeaderName is the §25.4 idempotency-key header.
const HeaderName = "Idempotency-Key"

// Record status values stored in ops_idempotency_keys.status.
const (
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// Default TTLs. §25.4: standard 24h (ops.idempotency.keyTTLSeconds),
// long-running 7d (ops.idempotency.longRunningKeyTTLSeconds = 604800).
const (
	DefaultStandardTTL    = 24 * time.Hour
	DefaultLongRunningTTL = 7 * 24 * time.Hour
)

// TTLClass is the §25.4 idempotency-key TTL classification. The endpoint
// picks it statically; agents do not request a TTL.
type TTLClass int

const (
	// ClassStandard is the 24h single-request-mutation class.
	ClassStandard TTLClass = iota
	// ClassLongRunning is the 7d multi-phase-operation class (upgrades,
	// restore) where the agent may pause between steps.
	ClassLongRunning
)

// Record is one stored idempotency entry keyed by (Key, CallerID).
type Record struct {
	Key        string
	CallerID   string
	Endpoint   string
	Status     string
	StatusCode int
	Response   []byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// ClaimResult enumerates the outcome of Store.Claim.
type ClaimResult int

const (
	// ClaimInserted means no live row existed for (key, caller_id); the
	// caller must execute the operation and then Complete or Fail it.
	ClaimInserted ClaimResult = iota
	// ClaimReplay means a completed/failed row exists; the stored
	// response is replayed without re-executing.
	ClaimReplay
	// ClaimInProgress means an in-progress row exists for this
	// (key, caller_id); the operation is still running.
	ClaimInProgress
	// ClaimOwnedByOther means a live row exists for this key under a
	// different caller_id — accidental cross-caller key reuse.
	ClaimOwnedByOther
)

// ErrStoreUnavailable signals that the durable store (Postgres) is
// unreachable. The middleware fails required-key endpoints closed (503)
// and lets optional endpoints proceed with a degradation warning.
var ErrStoreUnavailable = errors.New("opsidem: idempotency store unavailable")

// Store persists §25.4 idempotency records keyed by (key, caller_id).
type Store interface {
	// Claim atomically looks up (key, callerID) and, when absent,
	// inserts an in-progress row with the given endpoint and TTL.
	// It returns the existing or freshly inserted record and the
	// outcome. A Postgres outage returns ErrStoreUnavailable.
	Claim(ctx context.Context, key, callerID, endpoint string, ttl time.Duration, now time.Time) (Record, ClaimResult, error)
	// Complete records the final response for a previously claimed row,
	// flipping its status to completed.
	Complete(ctx context.Context, key, callerID string, statusCode int, response []byte, now time.Time) error
	// Fail marks a claimed row failed without caching a response, so a
	// retry re-executes.
	Fail(ctx context.Context, key, callerID string, now time.Time) error
	// PruneExpired removes rows past expires_at and returns the count.
	PruneExpired(ctx context.Context, now time.Time) (int, error)
}

// Config configures the Middleware.
type Config struct {
	// CallerID extracts the §25.4 caller_id (OIDC sub) from the request.
	// Required. The lenny-ops wiring passes the verified-principal
	// subject, falling back to the dev caller header.
	CallerID func(*http.Request) string
	// Production reports whether this deployment is Tier 2/3. At Tier 2/3
	// the required-key endpoints reject a missing key (400) and fail
	// closed on a store outage (503); at Tier 1 (dev) the key is optional
	// on those endpoints to simplify interactive testing.
	Production bool
	// StandardTTL overrides DefaultStandardTTL (ops.idempotency.keyTTLSeconds).
	StandardTTL time.Duration
	// LongRunningTTL overrides DefaultLongRunningTTL
	// (ops.idempotency.longRunningKeyTTLSeconds).
	LongRunningTTL time.Duration
	// MaxBodyBytes caps the request body the middleware buffers for
	// endpoint classification and re-delivery. Zero uses 1 MiB.
	MaxBodyBytes int64
	// Now overrides the clock; tests pin it.
	Now func() time.Time
	// Logger receives a WARN line when a Complete/Fail write fails after
	// the inner handler ran. Nil uses log.Default().
	Logger *log.Logger
}

// Middleware applies §25.4 idempotency before invoking the inner handler.
type Middleware struct {
	store Store
	cfg   Config
}

// New returns a Middleware over store. CallerID is required.
func New(store Store, cfg Config) *Middleware {
	if cfg.CallerID == nil {
		cfg.CallerID = func(*http.Request) string { return "" }
	}
	if cfg.StandardTTL <= 0 {
		cfg.StandardTTL = DefaultStandardTTL
	}
	if cfg.LongRunningTTL <= 0 {
		cfg.LongRunningTTL = DefaultLongRunningTTL
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 1 << 20
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Middleware{store: store, cfg: cfg}
}

// Wrap returns inner wrapped in the §25.4 idempotency middleware.
func (m *Middleware) Wrap(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.serve(w, r, inner)
	})
}

func (m *Middleware) logger() *log.Logger {
	if m.cfg.Logger != nil {
		return m.cfg.Logger
	}
	return log.Default()
}

func (m *Middleware) serve(w http.ResponseWriter, r *http.Request, inner http.Handler) {
	// §25.4: only POST and PUT mutate; other methods pass through.
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		inner.ServeHTTP(w, r)
		return
	}

	// Buffer the body so the classifier can inspect it (backups type:full)
	// and the inner handler still reads it.
	body := m.bufferBody(r)
	required, class := classify(r.Method, r.URL.Path, body)

	key := strings.TrimSpace(r.Header.Get(HeaderName))
	if key == "" {
		// §25.4: required-key endpoints reject a missing key at Tier 2/3.
		if required && m.cfg.Production {
			conventions.WriteError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED",
				conventions.CategoryPermanent,
				"this endpoint requires an Idempotency-Key header at the current tier")
			return
		}
		// Optional (or dev): proceed without idempotency tracking.
		inner.ServeHTTP(w, r)
		return
	}

	callerID := m.cfg.CallerID(r)
	ttl := m.cfg.StandardTTL
	if class == ClassLongRunning {
		ttl = m.cfg.LongRunningTTL
	}
	now := m.cfg.Now()
	endpoint := r.Method + " " + r.URL.Path

	rec, result, err := m.store.Claim(r.Context(), key, callerID, endpoint, ttl, now)
	if errors.Is(err, ErrStoreUnavailable) {
		// §25.4: required-key endpoints fail closed; the agent cannot rely
		// on retry-safety, so silently proceeding would violate the
		// contract. Optional endpoints proceed but mark degradation.
		if required && m.cfg.Production {
			conventions.WriteError(w, http.StatusServiceUnavailable, "IDEMPOTENCY_STORE_UNAVAILABLE",
				conventions.CategoryTransient,
				"the idempotency store is unreachable and this endpoint cannot proceed without it")
			return
		}
		dw := &degradingWriter{ResponseWriter: w}
		inner.ServeHTTP(dw, r)
		dw.flush()
		return
	}
	if err != nil {
		conventions.WriteError(w, http.StatusInternalServerError, "INTERNAL",
			conventions.CategoryTransient, "idempotency store error")
		return
	}

	switch result {
	case ClaimOwnedByOther:
		conventions.WriteError(w, http.StatusForbidden, "IDEMPOTENCY_KEY_OWNED_BY_OTHER_CALLER",
			conventions.CategoryAuth,
			"the (key, caller_id) lookup is not yours; this key is in use by another caller")
		return
	case ClaimInProgress:
		elapsed := now.Sub(rec.CreatedAt)
		if elapsed < 0 {
			elapsed = 0
		}
		body := conventions.NewError("OPERATION_IN_PROGRESS", conventions.CategoryPolicy,
			"a matching idempotency key is already in progress")
		body.Error.Details = map[string]any{"elapsed": elapsed.Round(time.Second).String()}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = encodeJSON(w, body)
		return
	case ClaimReplay:
		// §25.4: replay the stored response without re-executing.
		status := rec.StatusCode
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Lenny-Idempotent-Replay", "true")
		w.WriteHeader(status)
		_, _ = w.Write(rec.Response)
		return
	case ClaimInserted:
		cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		inner.ServeHTTP(cw, r)
		// §25.4 / §11.5: do not cache a 5xx — a transient failure must be
		// retryable. Mark the row failed so the retry re-executes.
		if cw.status >= 500 {
			if ferr := m.store.Fail(r.Context(), key, callerID, m.cfg.Now()); ferr != nil {
				m.logger().Printf("opsidem: mark failed (%s %s, caller=%s): %v", r.Method, r.URL.Path, callerID, ferr)
			}
			return
		}
		if cerr := m.store.Complete(r.Context(), key, callerID, cw.status, cw.body.Bytes(), m.cfg.Now()); cerr != nil {
			m.logger().Printf("opsidem: cache response (%s %s, caller=%s): %v", r.Method, r.URL.Path, callerID, cerr)
		}
		return
	}
}

// bufferBody reads the request body up to MaxBodyBytes and replaces
// r.Body with a fresh reader over the buffered bytes so the inner
// handler reads the same content.
func (m *Middleware) bufferBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, m.cfg.MaxBodyBytes))
	_ = r.Body.Close()
	if err != nil {
		buf = nil
	}
	r.Body = io.NopCloser(bytes.NewReader(buf))
	return buf
}
