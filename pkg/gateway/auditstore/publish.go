// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
)

// PublishingAppender wraps a Store with the §4.4 line 232 / §12.3.7
// audit-bearing EventBus first-publish path. On a successful Append it
// translates the row to OCSF, wraps the record in a CloudEvents v1.0.2
// envelope, publishes it on the §12.4 tenant-prefixed
// `t:{tenant_id}:evt:session_lifecycle` channel, and transitions the
// row's `eventbus_publish_state` to `published`. A publish failure
// leaves the row in `retry_pending` so the existing
// `pkg/gateway/auditstore/retranscribe.go` worker drains it on the
// next sweep — the spec's failure-retry path the §12.3.7 publish-state
// machine and the retranscribe worker close together.
//
// The audit row is durable as soon as Append returns; the publish path
// is a best-effort first-attempt that the worker covers. The pair
// matches the §4.4 line 232 contract: "the CloudEvents-wrapped audit
// events published on the EventBus" are emitted via OCSF in the data
// field of the envelope (datacontenttype = application/ocsf+json),
// with the same translator the §11.7 SIEM forwarder and §25.9 admin
// query API use.
//
// spec: §4.4 line 232 — "CloudEvents-wrapped audit events published on
// the EventBus".
type PublishingAppender struct {
	// Store is the §11.7 Postgres-backed audit chain. Required.
	Store *Store
	// Publisher is the §12.6 EventBus the wrapper publishes onto. It is
	// the interface, not the concrete RedisEventBus, so a Tier-4 backend
	// swap needs no change here. A nil Publisher disables the EventBus
	// path entirely (the wrapper then is functionally identical to
	// Store.Append).
	Publisher eventbus.EventBus
	// PublisherID is the §12.3.7 gateway-replica id (gw-<5-hex>) the
	// CloudEvents envelope stamps onto its `source` URI and the id
	// segment. Required when Publisher is wired; the constructor
	// defaults it to "gw-init" when empty so a dev-mode gateway has a
	// deterministic value.
	PublisherID string
	// Now overrides time.Now for the envelope `time` and the id
	// segment. Nil selects time.Now.
	Now func() time.Time
}

// NewPublishingAppender returns a PublishingAppender over store. The
// publisher and publisherID may be empty; in that case Append still
// commits the row and skips the publish.
func NewPublishingAppender(store *Store, publisher eventbus.EventBus, publisherID string) *PublishingAppender {
	if publisherID == "" {
		publisherID = "gw-init"
	}
	return &PublishingAppender{
		Store:       store,
		Publisher:   publisher,
		PublisherID: publisherID,
		Now:         time.Now,
	}
}

// Append commits the audit event to the chain and then publishes the
// audit-bearing CloudEvents envelope. The publish is best-effort: on
// failure the row's `eventbus_publish_state` is moved to
// `retry_pending` and the retranscribe worker drives the row to
// terminal `published` or `failed`. The returned audit.Row is the
// sealed row from the underlying Store, byte-identical to what
// `Store.Append` returns.
func (a *PublishingAppender) Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	row, err := a.Store.Append(ctx, tenantID, eventType, payload, at)
	if err != nil {
		return audit.Row{}, err
	}
	if a.Publisher == nil {
		// No publisher wired — the row is durable, but the publish
		// path is disabled. The retranscribe worker will not pick the
		// row up because it is still in the default `pending` state;
		// operators that enable the publisher later can drive the
		// initial transition via a manual republish API (§25.9.5).
		return row, nil
	}
	if err := a.publishAndMark(ctx, row); err != nil {
		// Publish failed — the spec contract says the row state moves
		// to `retry_pending` and the worker takes over. We log the
		// failure (the publisher's metric covers the counter side) but
		// do NOT return the error from Append: the row is durable and
		// the worker covers the publish.
		log.Printf("auditstore: publish (%s tenant=%s seq=%d): %v",
			eventType, tenantID, row.Seq, err)
	}
	return row, nil
}

// publishAndMark builds the OCSF record + CloudEvents envelope and
// publishes it; on success it marks `eventbus_publish_state =
// published`; on failure it marks `retry_pending` so the retranscribe
// worker can drain the row on its next sweep.
func (a *PublishingAppender) publishAndMark(ctx context.Context, row audit.Row) error {
	env, ocsfErr := a.buildEnvelope(row)
	if ocsfErr != nil {
		// Translation failure — the row carries no envelope, so the
		// best we can do is mark it for the retranscribe worker. The
		// worker will go through the OCSF dead-letter receipt path
		// (pkg/gateway/auditstore/retranscribe.go).
		_ = a.Store.SetPublishState(ctx, row.TenantID, row.Seq,
			eventbus.PublishRetryPending, 0)
		return fmt.Errorf("ocsf translate: %w", ocsfErr)
	}
	if pubErr := a.Publisher.Publish(ctx, eventbus.TenantID(row.TenantID),
		eventbus.TopicSessionLifecycle, env); pubErr != nil {
		_ = a.Store.SetPublishState(ctx, row.TenantID, row.Seq,
			eventbus.PublishRetryPending, 0)
		return pubErr
	}
	return a.Store.SetPublishState(ctx, row.TenantID, row.Seq,
		eventbus.PublishPublished, 0)
}

// buildEnvelope translates the row to OCSF and wraps it in a
// CloudEvents v1.0.2 envelope on the §12.3.7
// TopicSessionLifecycle channel. The envelope's `data` is the OCSF
// record (datacontenttype = application/ocsf+json).
func (a *PublishingAppender) buildEnvelope(row audit.Row) (eventbus.Event, error) {
	in := ocsf.Input{
		ID:                 row.TenantID + ":" + fmt.Sprint(row.Seq),
		Sequence:           row.Seq,
		TenantID:           row.TenantID,
		EventType:          row.EventType,
		EventSchemaVersion: row.EventSchemaVersion,
		CreatedAtUnixMs:    row.Timestamp.UTC().UnixMilli(),
		Payload:            row.Payload,
		PrevHash:           row.PrevHash,
		ChainIntegrity:     audit.ChainUnchecked,
	}
	rec, terr := ocsf.Translate(in)
	if terr != nil {
		return eventbus.Event{}, terr
	}
	body, err := ocsf.MarshalRecord(rec)
	if err != nil {
		return eventbus.Event{}, err
	}
	now := a.Now
	if now == nil {
		now = time.Now
	}
	return eventbus.NewEvent(eventbus.NewEventInput{
		TenantID:    row.TenantID,
		PublisherID: a.PublisherID,
		Component:   "gateway",
		ShortName:   row.EventType,
		Subject:     "session/" + sessionSubject(row),
		Data:        body,
		OCSF:        true,
		Now:         now,
	})
}

// sessionSubject pulls the §11.7 payload.session_id when present so
// the CloudEvents envelope's subject identifies the session the row
// belongs to. Falls back to the chain seq when no session id is set
// (admin events scoped to the tenant, not a session).
func sessionSubject(row audit.Row) string {
	if len(row.Payload) == 0 {
		return fmt.Sprintf("seq-%d", row.Seq)
	}
	var p map[string]any
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		return fmt.Sprintf("seq-%d", row.Seq)
	}
	if sid, ok := p["session_id"].(string); ok && sid != "" {
		return sid
	}
	return fmt.Sprintf("seq-%d", row.Seq)
}

// hexHashHelper returns the hex digest of a raw byte slice — a small
// helper so callers do not import "encoding/hex" just to format the
// hash for logging. Unused in v1 but kept for future log-shape
// extensions.
func hexHashHelper(b []byte) string { return hex.EncodeToString(b) }
