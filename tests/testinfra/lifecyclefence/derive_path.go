// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/google/uuid"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// runDerive drives POST /v1/sessions/{id}/derive of the owner's replay
// source through the production session server, with the World's object
// store as its blob store and the World's manifest store as its checkpoint
// manifest reader and writer. The server's reservation, chunk copy, derived
// manifest write, insert through CreateReserved, undo, and derive-failure
// row are all production code; pathStore observes them. A failing derive
// fails its manifest Finalise after the chunk copy and the manifest write,
// so the handler undoes both and writes its terminal derive-failure row.
//
// spec: §12.8 (Compliance Interfaces: Lifecycle fence and drain), §15.1 (REST API)
func (w *World) runDerive(ctx context.Context, o *CreateOutcome, failing, crash bool) {
	w.mu.Lock()
	src := w.sources[o.Owner]
	w.mu.Unlock()
	p := w.newPathStore(o, crash)
	var writer sessionserver.CheckpointManifestWriter = w.Manifests
	if failing {
		writer = failingFinalise{w.Manifests}
	}
	ids := []string{o.ID, uuid.NewString()}
	h := sessionserver.New(p, sessionserver.Options{
		DevTenantHeader: true,
		IDFunc: func() string {
			id := ids[0]
			ids = append(ids[1:], uuid.NewString())
			return id
		},
		Blobs:                    w.Objects,
		Users:                    w.S.Users,
		Tenants:                  w.S.Tenants,
		CheckpointManifestReader: w.Manifests,
		CheckpointManifestWriter: writer,
		PersistDeriveFailureRows: true,
	}).Handler()
	rr, crashed := serveUntilCrash(h, w.deriveRequest(ctx, src, o.Owner))
	if crashed {
		return
	}
	w.deriveOutcome(ctx, o, p, rr, src)
}

// deriveRequest builds a derive request of the source, authenticated as
// owner in the World's tenant. The derived session inherits the source's
// owner.
func (w *World) deriveRequest(ctx context.Context, sourceID, owner string) *http.Request {
	body, _ := json.Marshal(sessionserver.DeriveRequest{})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sourceID+"/derive", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(authmw.WithPrincipal(ctx, authmw.Principal{Subject: owner, TenantID: w.Tenant}))
}

// serveUntilCrash serves req and reports whether the path stopped at
// pathStore's crash stop. Any other panic propagates.
func serveUntilCrash(h http.Handler, req *http.Request) (rr *httptest.ResponseRecorder, crashed bool) {
	rr = httptest.NewRecorder()
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(crashStop); !ok {
				panic(r)
			}
			crashed = true
		}
	}()
	h.ServeHTTP(rr, req)
	return rr, false
}

// deriveOutcome records the derive's result from the handler's answer and
// the store calls pathStore observed. A derive that ended in a terminal
// derive-failure row is recorded on PathDeriveFailure, judged by that row's
// insert, whichever path was asked for.
func (w *World) deriveOutcome(ctx context.Context, o *CreateOutcome, p *pathStore, rr *httptest.ResponseRecorder, sourceID string) {
	o.ReservationKept = p.reservationKept()
	switch {
	case p.failureRow:
		o.Path, o.Err = PathDeriveFailure, p.failureRowErr
	case rr.Code == http.StatusCreated && p.inserted:
		o.Err = nil
	case rr.Code == http.StatusForbidden:
		o.Err = refusalOf(p.fenceErr, rr)
	case rr.Code == http.StatusNotFound:
		o.Err = fmt.Errorf("%w: derive answered 404", ErrSourceGone)
	case o.Reserved && p.fenceErr == nil && rr.Code >= http.StatusInternalServerError && w.sessionGone(ctx, sourceID):
		// The owner's erasure removed the source's objects under the copy
		// and its row before the derive-failure row's source read, so the
		// handler skipped that row.
		o.Err = fmt.Errorf("%w: derive answered %d", ErrSourceGone, rr.Code)
	default:
		o.Err = fmt.Errorf("lifecyclefence: derive answered %d: %s", rr.Code, rr.Body.String())
	}
}

// refusalOf returns the fence's refusal when the store returned one, and
// otherwise the handler's 403 answer as a refusal: the caller check or the
// source-owner check refused the request before it reached the store.
func refusalOf(fenceErr error, rr *httptest.ResponseRecorder) error {
	var ref *sessionstore.CreationRefusal
	if errors.As(fenceErr, &ref) {
		return fenceErr
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Error.Code == "" {
		return fmt.Errorf("lifecyclefence: 403 with no error code: %s", rr.Body.String())
	}
	return &sessionstore.CreationRefusal{Code: env.Error.Code, Message: env.Error.Message}
}
