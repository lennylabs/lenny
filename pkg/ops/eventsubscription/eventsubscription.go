// SPDX-License-Identifier: MIT

// Package eventsubscription is the §25.5 webhook-subscription
// service. It owns the Subscription record, the Store interface (a
// production Postgres backend lands in pkg/ops/eventsubscription/
// pgstore alongside the v1 in-memory Store this package ships), and
// the canonical error codes the §25.4 envelope reports.
//
// The lenny-ops opsserver wires its CRUD handlers against the Store;
// the §25.5 webhook delivery worker (pkg/ops/opsservice/
// webhookloop) reads the active subscriptions through a small
// adapter so a Store change is visible to the worker without a
// process restart.
package eventsubscription

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Canonical §25.4 error codes for the subscription surface.
const (
	ErrCodeNotFound       = "RESOURCE_NOT_FOUND"
	ErrCodeInvalid        = "VALIDATION_ERROR"
	ErrCodeNoDurableStore = "SERVICE_UNAVAILABLE"
)

// Errors returned by Service methods carry one of the §25.4 canonical
// codes via CodeOf. Each error wraps a typed Error so the opsserver
// can map it to an HTTP status without string matching.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// CodeOf returns the canonical error code embedded in err, or empty
// when err is not a typed Error.
func CodeOf(err error) string {
	if err == nil {
		return ""
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}

// Subscription is one §25.5 webhook subscription. The Secret field
// carries the HMAC-SHA256 signing secret the worker uses to sign
// every delivery. The ID is allocated by the Store on Create.
type Subscription struct {
	ID          string    `json:"id"`
	CallbackURL string    `json:"callbackUrl"`
	Types       []string  `json:"types,omitempty"`
	Secret      string    `json:"secret,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CreateRequest carries the fields a caller supplies on Create. The
// Store allocates ID, persists Secret only on first read, and
// stamps CreatedAt.
type CreateRequest struct {
	CallbackURL string   `json:"callbackUrl"`
	Types       []string `json:"types,omitempty"`
	Secret      string   `json:"secret,omitempty"`
}

// Store persists §25.5 webhook subscriptions. The production Postgres
// backend lives in pkg/ops/eventsubscription/pgstore; the in-memory
// MemoryStore in this package backs unit tests and the v1
// developer-mode lenny-ops binary.
type Store interface {
	Create(ctx context.Context, req CreateRequest) (Subscription, error)
	Get(ctx context.Context, id string) (Subscription, error)
	List(ctx context.Context) ([]Subscription, error)
	Delete(ctx context.Context, id string) error
}

// Service composes the Store with the §25.5 validation surface so the
// opsserver handler does not call into the Store with malformed
// payloads.
type Service struct {
	Store Store
	// IDFunc generates the subscription id on Create. A nil IDFunc
	// uses a time-prefixed deterministic-feeling id; tests inject a
	// pin so assertions can name the id.
	IDFunc func() string
	// Now returns the creation timestamp. A nil Now uses time.Now.
	Now func() time.Time
}

// NewService builds a Service against the supplied Store with the
// documented defaults.
func NewService(store Store) *Service {
	return &Service{Store: store}
}

// Create validates the request and persists a new subscription. A
// missing CallbackURL or one that fails RFC-3986 parsing returns
// ErrCodeInvalid; a non-http/https scheme is rejected for the same
// reason. The Store allocates the id and timestamps the row.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Subscription, error) {
	if s == nil || s.Store == nil {
		return Subscription{}, &Error{Code: ErrCodeNoDurableStore, Message: "subscription store unavailable"}
	}
	if err := validateCreate(req); err != nil {
		return Subscription{}, err
	}
	if s.IDFunc == nil {
		s.IDFunc = defaultIDFunc
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	// Normalize the Types list so two requests with the same set in
	// different orders persist identically.
	if len(req.Types) > 0 {
		sort.Strings(req.Types)
	}
	return s.Store.Create(ctx, req)
}

// Get reads one subscription by id.
func (s *Service) Get(ctx context.Context, id string) (Subscription, error) {
	if s == nil || s.Store == nil {
		return Subscription{}, &Error{Code: ErrCodeNoDurableStore, Message: "subscription store unavailable"}
	}
	if id == "" {
		return Subscription{}, &Error{Code: ErrCodeInvalid, Message: "id is required"}
	}
	return s.Store.Get(ctx, id)
}

// List returns every active subscription. The §25.5 worker calls
// List on every reconcile, so the implementation is expected to be
// cheap; the in-memory Store returns a defensive copy on every call.
func (s *Service) List(ctx context.Context) ([]Subscription, error) {
	if s == nil || s.Store == nil {
		return nil, &Error{Code: ErrCodeNoDurableStore, Message: "subscription store unavailable"}
	}
	return s.Store.List(ctx)
}

// Delete removes a subscription. A missing id returns NotFound.
func (s *Service) Delete(ctx context.Context, id string) error {
	if s == nil || s.Store == nil {
		return &Error{Code: ErrCodeNoDurableStore, Message: "subscription store unavailable"}
	}
	if id == "" {
		return &Error{Code: ErrCodeInvalid, Message: "id is required"}
	}
	return s.Store.Delete(ctx, id)
}

func validateCreate(req CreateRequest) error {
	if req.CallbackURL == "" {
		return &Error{Code: ErrCodeInvalid, Message: "callbackUrl is required"}
	}
	u, err := url.Parse(req.CallbackURL)
	if err != nil {
		return &Error{Code: ErrCodeInvalid, Message: "callbackUrl is malformed: " + err.Error()}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &Error{Code: ErrCodeInvalid, Message: "callbackUrl scheme must be http or https"}
	}
	if u.Host == "" {
		return &Error{Code: ErrCodeInvalid, Message: "callbackUrl must include a host"}
	}
	for _, t := range req.Types {
		if strings.TrimSpace(t) == "" {
			return &Error{Code: ErrCodeInvalid, Message: "types entries must be non-empty"}
		}
	}
	return nil
}

// MemoryStore is an in-memory Store backing the v1 lenny-ops binary
// when no Postgres connection is configured, and the unit tests.
// Lookups are O(1) by id; List builds a defensive copy on every call.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Subscription
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: map[string]Subscription{}}
}

// Create persists the row. The caller's Service supplies the
// allocated id via the request flow (see Service.Create).
func (m *MemoryStore) Create(_ context.Context, req CreateRequest) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := defaultIDFunc()
	row := Subscription{
		ID:          id,
		CallbackURL: req.CallbackURL,
		Types:       append([]string(nil), req.Types...),
		Secret:      req.Secret,
		CreatedAt:   time.Now().UTC(),
	}
	m.rows[id] = row
	return row, nil
}

// Get returns the subscription or ErrCodeNotFound.
func (m *MemoryStore) Get(_ context.Context, id string) (Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[id]
	if !ok {
		return Subscription{}, &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf("subscription %q not found", id)}
	}
	return row, nil
}

// List returns every persisted subscription, sorted by id for stable
// output.
func (m *MemoryStore) List(_ context.Context) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Subscription, 0, len(m.rows))
	for _, row := range m.rows {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Delete removes the subscription or returns ErrCodeNotFound.
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rows[id]; !ok {
		return &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf("subscription %q not found", id)}
	}
	delete(m.rows, id)
	return nil
}

// defaultIDFunc allocates a subscription id when the Service was
// constructed without an injected IDFunc. The id is unique per
// allocation; the Postgres backend will use a UUID column instead.
func defaultIDFunc() string {
	return fmt.Sprintf("sub_%d", time.Now().UnixNano())
}
