// SPDX-License-Identifier: MIT

// Package escalation implements the §25.4 escalation service: the
// structured record an agent creates when a problem exceeds its
// remediation capabilities (requires cluster-admin access, or repeated
// remediation has failed).
//
// §25.4 specifies a tiered create path (Postgres → Redis → in-memory) so
// an escalation can always be recorded, including during the storage
// outages that are most likely to trigger one. The Service composes an
// ordered set of durable Stores (the Postgres pgstore, then the Redis
// redisstore) over the always-present in-memory MemStore: Create attempts
// each tier in order, Get/List/Update read from the highest reachable
// store, and a background reconciliation flush promotes buffered records
// upward when a durable store recovers — preserving the authoring
// timestamp and the exactly-once emission flag.
//
// Creating an escalation emits an escalation_created operational event
// exactly once so webhook and SSE subscribers route it to PagerDuty,
// Slack, or any external system; the platform provides the record, the
// routing is the deployer's responsibility.
package escalation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// §25.4 escalation severities.
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// §25.4 escalation lifecycle statuses.
const (
	StatusOpen         = "open"
	StatusAcknowledged = "acknowledged"
	StatusResolved     = "resolved"
)

// §25.4 escalation persistence tiers. The value stays on the Escalation
// struct as a resource attribute; §25.2 distinguishes it from the
// response-level degradation envelope.
const (
	// PersistenceDurablePostgres is the Tier 1 store.
	PersistenceDurablePostgres = "durable-postgres"
	// PersistenceDurableRedis is the Tier 2 store.
	PersistenceDurableRedis = "durable-redis"
	// PersistenceBufferedMemory is the Tier 3 in-memory buffer.
	PersistenceBufferedMemory = "buffered-memory"
)

// §25.4 canonical escalation error codes. The HTTP layer maps each to
// its documented status code and §25.2 category.
const (
	// ErrCodeNotFound is ESCALATION_NOT_FOUND.
	ErrCodeNotFound = "ESCALATION_NOT_FOUND"
	// ErrCodeNoDurableStore is ESCALATION_NO_DURABLE_STORE: requireDurable
	// is set and both Postgres and Redis are unavailable.
	ErrCodeNoDurableStore = "ESCALATION_NO_DURABLE_STORE"
	// ErrCodeInvalid is the malformed-request rejection.
	ErrCodeInvalid = "ESCALATION_INVALID"
)

// defaultReconciliationWritesPerSecond is the §25.4 line 2414 default
// flush rate (ops.escalation.reconciliationWritesPerSecond).
const defaultReconciliationWritesPerSecond = 20

// Error is a §25.4 escalation failure carrying the canonical error code
// so the HTTP handler maps it to the documented status without
// re-classifying.
type Error struct {
	Code    string
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// CodeOf returns the §25.4 canonical error code carried by err, or the
// empty string when err is not an escalation *Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// FailedAction is one §25.4 failed remediation step recorded on an
// escalation's cause.
type FailedAction struct {
	Action    string `json:"action"`
	Endpoint  string `json:"endpoint,omitempty"`
	Error     string `json:"error,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// Escalation is the §25.4 escalation record. The JSON tags match the
// §25.4 Escalation struct verbatim so the HTTP handler marshals it
// directly.
type Escalation struct {
	ID             string          `json:"id"`
	Severity       string          `json:"severity"`
	Source         string          `json:"source"`
	OperationID    string          `json:"operationId,omitempty"`
	AlertName      string          `json:"alertName,omitempty"`
	RunbookName    string          `json:"runbookName,omitempty"`
	Summary        string          `json:"summary"`
	DiagnosticData json.RawMessage `json:"diagnosticData,omitempty"`
	FailedActions  []FailedAction  `json:"failedActions,omitempty"`
	Status         string          `json:"status"`
	Persistence    string          `json:"persistence"`
	Emitted        bool            `json:"emitted"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt,omitempty"`
	AcknowledgedAt *time.Time      `json:"acknowledgedAt,omitempty"`
	ResolvedAt     *time.Time      `json:"resolvedAt,omitempty"`
}

// CreateRequest is the §25.4 POST /v1/admin/escalations request body.
type CreateRequest struct {
	Severity       string          `json:"severity"`
	AlertName      string          `json:"alertName,omitempty"`
	RunbookName    string          `json:"runbookName,omitempty"`
	OperationID    string          `json:"operationId,omitempty"`
	Summary        string          `json:"summary"`
	DiagnosticData json.RawMessage `json:"diagnosticData,omitempty"`
	FailedActions  []FailedAction  `json:"failedActions,omitempty"`
	// Source is the caller identity; the HTTP layer fills it from the
	// authenticated principal, not from the request body.
	Source string `json:"-"`
}

// UpdateRequest is the §25.4 PUT /v1/admin/escalations/{id} request
// body. §25.4 supports moving an escalation to acknowledged or
// resolved.
type UpdateRequest struct {
	Status string `json:"status"`
}

// Filter is the §25.4 GET /v1/admin/escalations query filter.
type Filter struct {
	Status   string // CSV of lifecycle statuses
	Severity string // CSV of severities
	Since    time.Time
}

// Emitter publishes the §25.4 escalation_created operational event. The
// gateway opsevents.Emitter satisfies it; a nil Emitter leaves an
// escalation un-emitted (Emitted stays false) and a background retry
// would re-attempt the publish.
type Emitter interface {
	// EmitEscalationCreated publishes the escalation_created event for
	// the given escalation and reports whether the publish succeeded.
	EmitEscalationCreated(esc Escalation) bool
}

// Options configures a tiered escalation Service.
type Options struct {
	// Durable lists the durable persistence tiers in priority order
	// (Postgres first, then Redis). May be empty for a memory-only
	// deployment.
	Durable []Store
	// Emitter publishes escalation_created events; nil leaves records
	// un-emitted for the retry loop.
	Emitter Emitter
	// Audit receives the remediation.escalation_persisted event on each
	// reconciliation flush; nil drops it.
	Audit AuditSink
	// RequireDurable rejects a create with ESCALATION_NO_DURABLE_STORE
	// when no durable tier accepts it, instead of buffering in memory
	// (§25.4 line 2396 ops.escalation.requireDurable).
	RequireDurable bool
	// ReconciliationWritesPerSecond paces the flush so a large recovery
	// does not spike Postgres (§25.4 line 2414). Zero applies the default
	// of 20; a negative value disables pacing.
	ReconciliationWritesPerSecond int
}

// Service is the §25.4 tiered escalation service. It is the
// create/list/update surface the HTTP handler calls and owns the
// reconciliation flush the leader replica drives. Service is safe for
// concurrent use.
type Service struct {
	durable        []Store
	mem            *MemStore
	emitter        Emitter
	audit          AuditSink
	requireDurable bool
	flushPerSec    int

	clockMu sync.Mutex
	now     func() time.Time
	sleep   func(time.Duration)
}

// NewService returns a memory-only escalation service (Tier 3 buffer
// only) wired to emitter. It is the single-process / dev constructor;
// use NewWithStores to add the durable Postgres and Redis tiers.
func NewService(emitter Emitter) *Service {
	return NewWithStores(Options{Emitter: emitter})
}

// NewWithStores returns a tiered escalation service over opts.Durable
// plus the always-present in-memory Tier 3 buffer.
func NewWithStores(opts Options) *Service {
	flushPerSec := opts.ReconciliationWritesPerSecond
	if flushPerSec == 0 {
		flushPerSec = defaultReconciliationWritesPerSecond
	}
	return &Service{
		durable:        opts.Durable,
		mem:            NewMemStore(),
		emitter:        opts.Emitter,
		audit:          opts.Audit,
		requireDurable: opts.RequireDurable,
		flushPerSec:    flushPerSec,
		now:            func() time.Time { return time.Now().UTC() },
		sleep:          time.Sleep,
	}
}

// SetClock overrides the service clock; tests use it for deterministic
// timestamps.
func (s *Service) SetClock(now func() time.Time) {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	s.now = now
}

func (s *Service) clock() time.Time {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	return s.now()
}

// allStores returns the durable tiers followed by the in-memory buffer,
// in read priority order.
func (s *Service) allStores() []Store {
	out := make([]Store, 0, len(s.durable)+1)
	out = append(out, s.durable...)
	out = append(out, s.mem)
	return out
}

// validSeverity reports whether sev is a §25.4 escalation severity.
func validSeverity(sev string) bool {
	return sev == SeverityCritical || sev == SeverityWarning || sev == SeverityInfo
}

// Create records a §25.4 escalation. The create path attempts the
// durable tiers in order, falling back to the in-memory buffer; the
// record's Persistence reflects the tier that accepted it. Emission is
// attempted exactly once before persistence so the stored record carries
// the emitted flag; on failure Emitted stays false for the background
// retry. When RequireDurable is set and no durable tier accepts the
// record, Create returns ESCALATION_NO_DURABLE_STORE rather than
// buffering in memory.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Escalation, error) {
	if req.Summary == "" {
		return nil, &Error{Code: ErrCodeInvalid, Message: "summary is required"}
	}
	if !validSeverity(req.Severity) {
		return nil, &Error{Code: ErrCodeInvalid, Message: "severity must be critical, warning, or info"}
	}
	now := s.clock()
	esc := Escalation{
		ID:             newEscalationID(),
		Severity:       req.Severity,
		Source:         req.Source,
		OperationID:    req.OperationID,
		AlertName:      req.AlertName,
		RunbookName:    req.RunbookName,
		Summary:        req.Summary,
		DiagnosticData: req.DiagnosticData,
		FailedActions:  req.FailedActions,
		Status:         StatusOpen,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// requireDurable rejects a create that cannot land durably. Emitting
	// before that rejection would page subscribers for an escalation
	// persisted nowhere, so the durable-required path stores first and
	// emits only once a durable tier accepts the record.
	if s.requireDurable {
		return s.createRequireDurable(ctx, esc)
	}
	// §25.4 emission exactly-once: emit, then persist with the flag set.
	// On failure emitted stays false for the background retry.
	if s.emitter != nil {
		esc.Emitted = s.emitter.EmitEscalationCreated(esc)
	}
	// Tiered create: attempt each durable tier in priority order, falling
	// back to the next on an unavailable store.
	for _, st := range s.durable {
		esc.Persistence = st.Tier()
		err := st.Put(ctx, esc)
		if err == nil {
			return cloneEscalation(&esc), nil
		}
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		return nil, err
	}
	// No durable tier accepted the record; buffer it in memory.
	esc.Persistence = PersistenceBufferedMemory
	if err := s.mem.Put(ctx, esc); err != nil {
		return nil, err
	}
	return cloneEscalation(&esc), nil
}

// createRequireDurable is the §25.4 requireDurable create path: the record
// must land in a durable tier (Postgres, then Redis) or the create is
// rejected with ESCALATION_NO_DURABLE_STORE. The escalation_created event
// is emitted only after a durable store accepts the record, so a create
// rejected with both durable tiers down pages no webhook or SSE
// subscribers. This preserves the §25.4 "exactly one escalation_created
// event per escalation" contract: a rejected create records no escalation
// and therefore emits none.
//
// spec: §25.4 (requireDurable rejects instead of accepting a memory-only
// record; emission exactly-once).
func (s *Service) createRequireDurable(ctx context.Context, esc Escalation) (*Escalation, error) {
	for _, st := range s.durable {
		esc.Persistence = st.Tier()
		err := st.Put(ctx, esc)
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// The record is durably stored. §25.4 emission exactly-once:
		// publish the event, then set the emitted flag on the accepting
		// store. On emission failure the flag stays false for the
		// background retry.
		if s.emitter != nil && s.emitter.EmitEscalationCreated(esc) {
			esc.Emitted = true
			if serr := st.SetEmitted(ctx, esc.ID); serr != nil {
				esc.Emitted = false
			}
		}
		return cloneEscalation(&esc), nil
	}
	// No durable tier accepted the record; reject without emitting.
	return nil, &Error{Code: ErrCodeNoDurableStore, Message: "both Postgres and Redis are unavailable"}
}

// Get returns a single §25.4 escalation by id from the highest reachable
// store that holds it, or ESCALATION_NOT_FOUND.
func (s *Service) Get(ctx context.Context, id string) (*Escalation, error) {
	for _, st := range s.allStores() {
		esc, err := st.Get(ctx, id)
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if esc != nil {
			return esc, nil
		}
	}
	return nil, &Error{Code: ErrCodeNotFound, Message: "no escalation " + id}
}

// List returns one page of the §25.4 escalations matching the filter,
// newest-first, read from the highest reachable store (§25.4 query-path
// tiering). The returned page's CursorKind and NextCursor reflect the store
// that served it: the durable Postgres tier keyset-paginates ("pk"), while
// the Redis scan and in-memory buffer paginate by limit only ("none").
// cursor continues a prior page produced by the same tier.
func (s *Service) List(ctx context.Context, f Filter, cursor string, limit int) (ListPage, error) {
	for _, st := range s.allStores() {
		page, err := st.List(ctx, f, cursor, limit)
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		if err != nil {
			return ListPage{}, err
		}
		return page, nil
	}
	return ListPage{}, nil
}

// Update moves a §25.4 escalation to a new lifecycle status
// (acknowledged or resolved) in the highest reachable store that holds
// it. It returns ESCALATION_NOT_FOUND for an unknown id and
// ESCALATION_INVALID for an unrecognized status.
func (s *Service) Update(ctx context.Context, id string, req UpdateRequest) (*Escalation, error) {
	if req.Status != StatusOpen && req.Status != StatusAcknowledged && req.Status != StatusResolved {
		return nil, &Error{Code: ErrCodeInvalid, Message: "status must be open, acknowledged, or resolved"}
	}
	now := s.clock()
	for _, st := range s.allStores() {
		esc, err := st.SetStatus(ctx, id, req.Status, now)
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if esc != nil {
			return esc, nil
		}
	}
	return nil, &Error{Code: ErrCodeNotFound, Message: "no escalation " + id}
}

// RetryEmission re-attempts the §25.4 escalation_created publish for any
// escalation whose emitted flag is still false, across every reachable
// store. §25.4 has a background goroutine call this every 30s until
// emission succeeds. It returns the number of escalations that became
// emitted on this pass.
func (s *Service) RetryEmission(ctx context.Context) int {
	if s.emitter == nil {
		return 0
	}
	emitted := 0
	for _, st := range s.allStores() {
		pending, err := st.PendingEmission(ctx)
		if err != nil {
			continue
		}
		for _, esc := range pending {
			if s.emitter.EmitEscalationCreated(esc) {
				if err := st.SetEmitted(ctx, esc.ID); err == nil {
					emitted++
				}
			}
		}
	}
	return emitted
}

// Flush is the §25.4 reconciliation pass: when a durable tier is
// reachable, buffered in-memory escalations are promoted upward. The
// original CreatedAt and the emitted flag are preserved across the move
// (§25.4 lines 2411-2412); a destination that already holds the record
// makes the flush a no-op (line 2413); each flush emits a
// remediation.escalation_persisted audit event and is rate-limited to
// the configured writes-per-second (lines 2414-2415). Flush returns the
// number of escalations promoted.
func (s *Service) Flush(ctx context.Context) (int, error) {
	if len(s.durable) == 0 {
		return 0, nil
	}
	buffered := s.mem.Buffered()
	flushed := 0
	for i, esc := range buffered {
		if i > 0 {
			s.pace()
		}
		start := time.Now()
		dest, err := s.putDurable(ctx, &esc)
		if err != nil {
			if errors.Is(err, ErrStoreUnavailable) {
				// No durable tier is reachable; stop and retry next pass.
				break
			}
			return flushed, err
		}
		s.mem.Remove(esc.ID)
		flushed++
		if s.audit != nil {
			s.audit.EscalationPersisted(ctx, esc.ID, PersistenceBufferedMemory, dest,
				time.Since(start).Milliseconds())
		}
	}
	return flushed, nil
}

// putDurable writes esc to the highest reachable durable tier, stamping
// the destination tier onto its Persistence. It returns the destination
// tier label, or ErrStoreUnavailable when no durable tier is reachable.
func (s *Service) putDurable(ctx context.Context, esc *Escalation) (string, error) {
	for _, st := range s.durable {
		esc.Persistence = st.Tier()
		err := st.Put(ctx, *esc)
		if err == nil {
			return st.Tier(), nil
		}
		if errors.Is(err, ErrStoreUnavailable) {
			continue
		}
		return "", err
	}
	return "", ErrStoreUnavailable
}

// pace sleeps to keep the flush within flushPerSec writes per second. A
// non-positive rate disables pacing.
func (s *Service) pace() {
	if s.flushPerSec <= 0 {
		return
	}
	s.sleep(time.Second / time.Duration(s.flushPerSec))
}

// csvSet splits a comma-separated filter value into a set; an empty
// value yields a nil set, which matches everything.
func csvSet(csv string) map[string]bool {
	if csv == "" {
		return nil
	}
	set := make(map[string]bool)
	start := 0
	for i := 0; i <= len(csv); i++ {
		if i == len(csv) || csv[i] == ',' {
			if v := trimSpace(csv[start:i]); v != "" {
				set[v] = true
			}
			start = i + 1
		}
	}
	return set
}

// trimSpace trims ASCII spaces from both ends of s.
func trimSpace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}

// cloneEscalation returns a copy of esc so a caller cannot mutate the
// stored record through the returned pointer.
func cloneEscalation(esc *Escalation) *Escalation {
	cp := *esc
	if esc.AcknowledgedAt != nil {
		t := *esc.AcknowledgedAt
		cp.AcknowledgedAt = &t
	}
	if esc.ResolvedAt != nil {
		t := *esc.ResolvedAt
		cp.ResolvedAt = &t
	}
	return &cp
}

// newEscalationID returns a random "esc-" + hex escalation id.
func newEscalationID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "esc-" + hex.EncodeToString(b[:])
}
