// SPDX-License-Identifier: MIT

package inputwaitrecovery

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// Updater is the locked-row session write the input-wait writes take.
type Updater interface {
	Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error)
}

// PendingLister lists a session's waits pending in this replica's registry.
// *inputwait.Registry satisfies it.
type PendingLister interface {
	PendingDetailsForSession(sessionID string) []inputwait.PendingRequest
}

// EventPublisher publishes a session event under a tenant. *sessionevents.Bus
// satisfies it.
type EventPublisher interface {
	PublishForTenant(tenantID, sessionID, eventType, data string, now time.Time) sessionevents.Event
}

// Writer applies the row writes that keep a session's persisted
// input_required state on a live wait: the entry write, the exit write, and
// the recovery write. Each write that changes the row's state publishes one
// status_change under the locked row's tenant.
type Writer struct {
	// Store is the session store the writes update.
	Store Updater
	// Waits is this replica's input-wait registry.
	Waits PendingLister
	// Events receives status_change. Nil publishes nothing.
	Events EventPublisher
	// Clock stamps LastAgentActivityAt and the event time. Nil uses
	// time.Now.
	Clock func() time.Time
}

func (w Writer) now() time.Time {
	if w.Clock == nil {
		return time.Now()
	}
	return w.Clock()
}

// Enter is the entry write. On a locked running row it sets input_required
// naming waitID; on a locked input_required row it re-names the row to
// waitID and leaves the state; on any other state it returns
// sessionstore.ErrSuperseded and changes nothing.
// spec: §7.2 (Session state machine) — running → input_required.
func (w Writer) Enter(ctx context.Context, tenantID, sessionID, waitID string) error {
	return w.apply(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		switch row.State {
		case session.StateRunning:
			row.State = session.StateInputRequired
		case session.StateInputRequired:
		default:
			return sessionstore.ErrSuperseded
		}
		row.InputWaitID = waitID
		return nil
	})
}

// Exit is the exit write, taken once after the wait named waitID has left the
// registry. It runs on a context detached from the request's cancellation and
// bounded by sessionstore.DefaultPrimaryReadTimeout, with no retry. It
// applies only when the locked row is input_required and names waitID; it
// then re-points the row at another persisted wait of the session that is
// still pending here, or restores running.
// spec: §7.2 (Session state machine) — input_required → running; §6.2 (idle
// clock restarts on every entry to running).
func (w Writer) Exit(ctx context.Context, tenantID, sessionID, waitID string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionstore.DefaultPrimaryReadTimeout)
	defer cancel()
	return w.apply(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if row.State != session.StateInputRequired || row.InputWaitID != waitID {
			return sessionstore.ErrSuperseded
		}
		w.repointOrRestore(row)
		return nil
	})
}

// Recover is the recovery write for one session of the connection's pod. It
// applies only when the locked row is input_required and no wait pending
// here carries the row's InputWaitID, and then re-points or restores as Exit
// does. The registry is read under the row lock, so the locked-row mutator
// makes the only state decision.
// spec: §7.2 (Session state machine) — input_required → running; §28.3
// (Link register).
func (w Writer) Recover(ctx context.Context, tenantID, sessionID string) error {
	return w.apply(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if row.State != session.StateInputRequired {
			return sessionstore.ErrSuperseded
		}
		for _, p := range w.Waits.PendingDetailsForSession(row.ID) {
			if p.WaitID != "" && p.WaitID == row.InputWaitID {
				return sessionstore.ErrSuperseded
			}
		}
		w.repointOrRestore(row)
		return nil
	})
}

// repointOrRestore names the first persisted wait of the session pending
// here, leaving input_required, or restores running with the idle clock
// restarted when none is pending.
func (w Writer) repointOrRestore(row *sessionstore.Session) {
	for _, p := range w.Waits.PendingDetailsForSession(row.ID) {
		if p.Persisted {
			row.InputWaitID = p.WaitID
			return
		}
	}
	row.State = session.StateRunning
	row.LastAgentActivityAt = w.now()
}

// apply runs mutate under the row lock and publishes status_change when the
// applied write changed the state.
func (w Writer) apply(ctx context.Context, tenantID, sessionID string, mutate func(*sessionstore.Session) error) error {
	changed := false
	row, err := w.Store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		prev := row.State
		if err := mutate(row); err != nil {
			changed = false
			return err
		}
		changed = row.State != prev
		return nil
	})
	if err != nil {
		return fmt.Errorf("input-wait write on session %s: %w", sessionID, err)
	}
	if changed {
		w.publishStatusChange(row)
	}
	return nil
}

// publishStatusChange sends the status_change payload the session server's
// lifecycle transitions send.
// spec: §7.2 (Session state machine) — status_change(state).
func (w Writer) publishStatusChange(row sessionstore.Session) {
	if w.Events == nil {
		return
	}
	data, err := json.Marshal(map[string]any{"state": string(row.State)})
	if err != nil {
		return
	}
	w.Events.PublishForTenant(row.TenantID, row.ID, "status_change", string(data), w.now())
}
