// SPDX-License-Identifier: MIT

// Package redisstore is the Redis-backed §25.4 escalation Store (Tier 2).
// It writes each escalation to the platform-scoped Redis hash
// ops:escalations:{id} with a 24h TTL, so an escalation is recorded
// durably even when Postgres is unreachable. A Redis outage surfaces as
// escalation.ErrStoreUnavailable so the tiered Service falls back to the
// in-memory tier.
//
// The keyspace is platform-scoped (escalations are platform operational
// records, not tenant data), so the keys carry no tenant prefix —
// matching the other ops: Redis namespaces.
//
// spec: §25.4 lines 2376-2429.
package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/ops/escalation"
)

// keyPrefix is the §25.4 line 2383 escalation key namespace.
const keyPrefix = "ops:escalations:"

// recordTTL is the §25.4 line 2383 Tier 2 escalation TTL.
const recordTTL = 24 * time.Hour

// dataField is the hash field holding the JSON-encoded escalation.
const dataField = "data"

func key(id string) string { return keyPrefix + id }

// Store is the Redis-backed §25.4 escalation Tier 2 store. Construct with New.
type Store struct {
	client redis.UniversalClient
}

// New returns a Store backed by client.
func New(client redis.UniversalClient) *Store { return &Store{client: client} }

// Tier reports the durable-redis persistence label.
func (s *Store) Tier() string { return escalation.PersistenceDurableRedis }

// unavailable maps a non-nil Redis error (other than the key-miss
// sentinel) to escalation.ErrStoreUnavailable so the Service treats a
// Redis outage as a tier fallback.
func unavailable(err error) error {
	if err == nil || errors.Is(err, redis.Nil) {
		return nil
	}
	return escalation.ErrStoreUnavailable
}

// Put writes esc to ops:escalations:{id} as a JSON blob with a fresh 24h
// TTL. A re-put of the same id overwrites the record, so the
// reconciliation flush is idempotent (§25.4 line 2413).
func (s *Store) Put(ctx context.Context, esc escalation.Escalation) error {
	blob, err := json.Marshal(esc)
	if err != nil {
		return err
	}
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, key(esc.ID), dataField, blob)
	pipe.Expire(ctx, key(esc.ID), recordTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return unavailable(err)
	}
	return nil
}

// Get returns the escalation by id, or (nil, nil) when absent or expired.
func (s *Store) Get(ctx context.Context, id string) (*escalation.Escalation, error) {
	esc, err := s.read(ctx, key(id))
	if err != nil {
		return nil, unavailable(err)
	}
	return esc, nil
}

// List scans the escalation keyspace, filters, and returns the matching
// records newest-first as one page capped by limit. The Redis scan is the
// CursorKindNone query path: it paginates by limit only and reports
// HasMore when more matching records exist beyond the page, but issues no
// continuation cursor (§25.4 line 2428, cursorKind "none").
func (s *Store) List(ctx context.Context, f escalation.Filter, _ string, limit int) (escalation.ListPage, error) {
	all, err := s.scanAll(ctx)
	if err != nil {
		return escalation.ListPage{}, unavailable(err)
	}
	statuses := csvSet(f.Status)
	severities := csvSet(f.Severity)
	out := make([]escalation.Escalation, 0, len(all))
	for _, esc := range all {
		if len(statuses) > 0 && !statuses[esc.Status] {
			continue
		}
		if len(severities) > 0 && !severities[esc.Severity] {
			continue
		}
		if !f.Since.IsZero() && esc.CreatedAt.Before(f.Since) {
			continue
		}
		out = append(out, esc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	hasMore := limit > 0 && len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return escalation.ListPage{Items: out, HasMore: hasMore, CursorKind: escalation.CursorKindNone}, nil
}

// SetStatus moves the escalation to status and re-persists it with a
// refreshed TTL. Returns (nil, nil) when absent.
func (s *Store) SetStatus(ctx context.Context, id, status string, now time.Time) (*escalation.Escalation, error) {
	esc, err := s.read(ctx, key(id))
	if err != nil {
		return nil, unavailable(err)
	}
	if esc == nil {
		return nil, nil
	}
	escalation.ApplyStatus(esc, status, now)
	if err := s.Put(ctx, *esc); err != nil {
		return nil, err
	}
	return esc, nil
}

// SetEmitted flips the escalation's emitted flag true and re-persists it.
func (s *Store) SetEmitted(ctx context.Context, id string) error {
	esc, err := s.read(ctx, key(id))
	if err != nil {
		return unavailable(err)
	}
	if esc == nil {
		return nil
	}
	esc.Emitted = true
	return s.Put(ctx, *esc)
}

// PendingEmission returns escalations whose emitted flag is false.
func (s *Store) PendingEmission(ctx context.Context) ([]escalation.Escalation, error) {
	all, err := s.scanAll(ctx)
	if err != nil {
		return nil, unavailable(err)
	}
	var out []escalation.Escalation
	for _, esc := range all {
		if !esc.Emitted {
			out = append(out, esc)
		}
	}
	return out, nil
}

// read fetches and decodes the escalation at k, or (nil, nil) on a miss.
func (s *Store) read(ctx context.Context, k string) (*escalation.Escalation, error) {
	blob, err := s.client.HGet(ctx, k, dataField).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var esc escalation.Escalation
	if err := json.Unmarshal(blob, &esc); err != nil {
		return nil, nil
	}
	return &esc, nil
}

// scanAll iterates the escalation keyspace and decodes every live record.
// Keys that expire between the scan and the read are skipped.
func (s *Store) scanAll(ctx context.Context) ([]escalation.Escalation, error) {
	iter := s.client.Scan(ctx, 0, keyPrefix+"*", 256).Iterator()
	var out []escalation.Escalation
	for iter.Next(ctx) {
		esc, err := s.read(ctx, iter.Val())
		if err != nil {
			return nil, err
		}
		if esc != nil {
			out = append(out, *esc)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// csvSet splits a comma-separated filter value into a set; an empty value
// yields a nil set, which matches everything.
func csvSet(csv string) map[string]bool {
	if csv == "" {
		return nil
	}
	set := make(map[string]bool)
	start := 0
	for i := 0; i <= len(csv); i++ {
		if i == len(csv) || csv[i] == ',' {
			v := csv[start:i]
			for len(v) > 0 && v[0] == ' ' {
				v = v[1:]
			}
			for len(v) > 0 && v[len(v)-1] == ' ' {
				v = v[:len(v)-1]
			}
			if v != "" {
				set[v] = true
			}
			start = i + 1
		}
	}
	return set
}

// Compile-time guard that *Store satisfies the escalation.Store contract.
var _ escalation.Store = (*Store)(nil)
