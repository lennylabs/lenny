// SPDX-License-Identifier: MIT

// Package escalation implements the §25.4 escalation service: the
// structured record an agent creates when a problem exceeds its
// remediation capabilities (requires cluster-admin access, or repeated
// remediation has failed).
//
// §25.4 specifies a tiered create path (Postgres → Redis → in-memory)
// so an escalation can always be recorded, including during the
// storage outages that are most likely to trigger one. The v1 store
// implemented here is the in-memory Tier 3 buffer: a capped ring of the
// most recent escalations, supporting create, list with filtering,
// status update, and the §25.4 escalation_created emission flag. The
// Postgres and Redis tiers reuse this service's contract.
//
// Creating an escalation emits an escalation_created operational event
// so webhook and SSE subscribers route it to PagerDuty, Slack, or any
// external system; the platform provides the record, the routing is
// the deployer's responsibility.
package escalation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
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

// Service is the §25.4 escalation service over an in-memory Tier 3
// buffer. It is the create/list/update surface the HTTP handler calls.
// Service is safe for concurrent use.
type Service struct {
	mu       sync.Mutex
	byID     map[string]*Escalation
	order    []string // creation order, for capped eviction
	capacity int
	emitter  Emitter
	now      func() time.Time
}

// bufferCapacity is the §25.4 Tier 3 in-memory buffer cap: the oldest
// escalation is evicted when a new one would exceed it.
const bufferCapacity = 100

// NewService returns an escalation service backed by the in-memory
// Tier 3 buffer. emitter publishes escalation_created events; a nil
// emitter leaves escalations un-emitted.
func NewService(emitter Emitter) *Service {
	return &Service{
		byID:     make(map[string]*Escalation),
		capacity: bufferCapacity,
		emitter:  emitter,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the service clock; tests use it for deterministic
// timestamps.
func (s *Service) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// validSeverity reports whether sev is a §25.4 escalation severity.
func validSeverity(sev string) bool {
	return sev == SeverityCritical || sev == SeverityWarning || sev == SeverityInfo
}

// Create records a §25.4 escalation and emits the escalation_created
// event. The v1 store is the in-memory Tier 3 buffer, so the record's
// persistence is buffered-memory. Emission is attempted once; on
// failure Emitted stays false and a background retry re-attempts it.
func (s *Service) Create(_ context.Context, req CreateRequest) (*Escalation, error) {
	if req.Summary == "" {
		return nil, &Error{Code: ErrCodeInvalid, Message: "summary is required"}
	}
	if !validSeverity(req.Severity) {
		return nil, &Error{Code: ErrCodeInvalid, Message: "severity must be critical, warning, or info"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	esc := &Escalation{
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
		Persistence:    PersistenceBufferedMemory,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	// §25.4 emission exactly-once: emit, then set emitted=true. On
	// failure emitted stays false for the background retry.
	if s.emitter != nil {
		esc.Emitted = s.emitter.EmitEscalationCreated(*esc)
	}
	s.byID[esc.ID] = esc
	s.order = append(s.order, esc.ID)
	s.evictLocked()
	return cloneEscalation(esc), nil
}

// evictLocked drops the oldest escalations until the buffer is within
// capacity. The caller holds s.mu.
func (s *Service) evictLocked() {
	for len(s.order) > s.capacity {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.byID, oldest)
	}
}

// Get returns a single §25.4 escalation by id, or ESCALATION_NOT_FOUND.
func (s *Service) Get(_ context.Context, id string) (*Escalation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	esc, ok := s.byID[id]
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "no escalation " + id}
	}
	return cloneEscalation(esc), nil
}

// List returns the §25.4 escalations matching the filter, newest-first.
// limit caps the page; a non-positive limit applies no cap.
func (s *Service) List(_ context.Context, f Filter, limit int) ([]Escalation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	statuses := csvSet(f.Status)
	severities := csvSet(f.Severity)
	out := make([]Escalation, 0, len(s.byID))
	for _, esc := range s.byID {
		if len(statuses) > 0 && !statuses[esc.Status] {
			continue
		}
		if len(severities) > 0 && !severities[esc.Severity] {
			continue
		}
		if !f.Since.IsZero() && esc.CreatedAt.Before(f.Since) {
			continue
		}
		out = append(out, *cloneEscalation(esc))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Update moves a §25.4 escalation to a new lifecycle status
// (acknowledged or resolved). It stamps acknowledgedAt or resolvedAt
// and returns ESCALATION_NOT_FOUND for an unknown id and
// ESCALATION_INVALID for an unrecognized status.
func (s *Service) Update(_ context.Context, id string, req UpdateRequest) (*Escalation, error) {
	if req.Status != StatusOpen && req.Status != StatusAcknowledged && req.Status != StatusResolved {
		return nil, &Error{Code: ErrCodeInvalid, Message: "status must be open, acknowledged, or resolved"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	esc, ok := s.byID[id]
	if !ok {
		return nil, &Error{Code: ErrCodeNotFound, Message: "no escalation " + id}
	}
	now := s.now()
	esc.Status = req.Status
	esc.UpdatedAt = now
	switch req.Status {
	case StatusAcknowledged:
		if esc.AcknowledgedAt == nil {
			t := now
			esc.AcknowledgedAt = &t
		}
	case StatusResolved:
		if esc.ResolvedAt == nil {
			t := now
			esc.ResolvedAt = &t
		}
	}
	return cloneEscalation(esc), nil
}

// RetryEmission re-attempts the §25.4 escalation_created publish for any
// escalation whose emitted flag is still false. §25.4 has a background
// goroutine call this every 30s until emission succeeds. It returns the
// number of escalations that became emitted on this pass.
func (s *Service) RetryEmission(context.Context) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emitter == nil {
		return 0
	}
	emitted := 0
	for _, esc := range s.byID {
		if esc.Emitted {
			continue
		}
		if s.emitter.EmitEscalationCreated(*esc) {
			esc.Emitted = true
			emitted++
		}
	}
	return emitted
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
