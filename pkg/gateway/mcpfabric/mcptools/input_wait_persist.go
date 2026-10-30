// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/gatewaycontrol/inputwaitrecovery"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// inputWait is one registered lenny/request_input wait. A wait whose call
// arrived over LNK-GWCONTROL is persisted: the session row carries
// input_required naming the wait's identity until the wait leaves the
// registry. A wait served on the gateway-edge /mcp endpoint carries no
// connection record, is registered unpersisted, and writes nothing to the
// row.
type inputWait struct {
	ch        <-chan string
	deps      Deps
	row       sessionstore.Session
	requestID string
	waitID    string
	record    *inputwaitrecovery.Record
	writer    inputwaitrecovery.Writer
}

// beginInputWait registers the call's wait and, for a call carrying a
// connection record, takes the entry write. When the entry write fails with
// an error other than sessionstore.ErrSuperseded, it cancels the wait, takes
// the exit write, and returns the entry error. A superseded entry write
// leaves the row as it is and the wait proceeds.
// spec: §7.2 (Session state machine) — running → input_required.
func beginInputWait(ctx context.Context, deps Deps, row sessionstore.Session, requestID string, parts []json.RawMessage, idFn func() string, clock func() time.Time) (*inputWait, error) {
	w := &inputWait{deps: deps, row: row, requestID: requestID}
	rec, persisted := inputwaitrecovery.FromContext(ctx)
	if !persisted {
		ch, err := deps.InputWaits.Register(row.ID, requestID, parts)
		if err != nil {
			return nil, err
		}
		w.ch = ch
		return w, nil
	}
	// The wait identity is drawn from the gateway's ID source rather than
	// taken from the caller's requestId, which a runtime can reuse on its
	// next connection.
	w.waitID = "iw_" + idFn()
	ch, err := deps.InputWaits.RegisterPersisted(row.ID, requestID, w.waitID, parts)
	if err != nil {
		return nil, err
	}
	w.ch = ch
	w.record = rec
	w.writer = inputwaitrecovery.Writer{Store: deps.Store, Waits: deps.InputWaits, Clock: clock}
	if deps.Events != nil {
		w.writer.Events = deps.Events
	}
	if err := w.writer.Enter(ctx, row.TenantID, row.ID, w.waitID); err != nil && !errors.Is(err, sessionstore.ErrSuperseded) {
		w.cancel()
		w.exit(ctx)
		return nil, fmt.Errorf("request_input: persist input_required: %w", err)
	}
	return w, nil
}

// cancel removes the wait from the registry without an answer.
func (w *inputWait) cancel() {
	w.deps.InputWaits.Cancel(w.row.ID, w.requestID)
}

// exit takes the exit write once the wait has left the registry. A failure
// other than sessionstore.ErrSuperseded re-arms every connection record on
// the replica, so the next call carrying a session ID on each connection runs
// the recovery that converges the row. exit runs at most once.
// spec: §7.2 (Session state machine) — input_required → running; §28.3 (Link
// register).
func (w *inputWait) exit(ctx context.Context) {
	if w.record == nil {
		return
	}
	rec := w.record
	w.record = nil
	if err := w.writer.Exit(ctx, w.row.TenantID, w.row.ID, w.waitID); err != nil && !errors.Is(err, sessionstore.ErrSuperseded) {
		rec.Generation().Rearm()
	}
}
