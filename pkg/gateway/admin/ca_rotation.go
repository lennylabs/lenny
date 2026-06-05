// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/carotation"
	"github.com/lennylabs/lenny/pkg/gateway/carotationstore"
	"github.com/lennylabs/lenny/pkg/mtls"
)

// CARotationManager drives the §10.3 CA-rotation state machine on a
// durable store. *carotation.Manager satisfies it; a nil manager leaves
// the routes unregistered (a deployment with mTLS disabled never wires
// one). spec: §10.3 lines 344-350. F-10.3.21.
type CARotationManager interface {
	// Status returns the current rotation snapshot. ok is false before a
	// rotation has been initialized.
	Status(ctx context.Context) (snap mtls.CARotationSnapshot, ok bool, err error)
	// Begin introduces newCAID into the trust bundle (idle ->
	// new_ca_deployed) and opens the overlap window.
	Begin(ctx context.Context, newCAID string) (mtls.CARotationSnapshot, error)
	// Promote swaps the issuer role to the new CA (new_ca_deployed ->
	// promoted).
	Promote(ctx context.Context) (mtls.CARotationSnapshot, error)
	// Retire drops the old CA from the trust bundle once the overlap
	// window closes (promoted -> old_ca_retired).
	Retire(ctx context.Context) (mtls.CARotationSnapshot, error)
}

// WithCARotation wires the §10.3 CA-rotation admin handlers onto the
// Router.
func (r *Router) WithCARotation(m CARotationManager) *Router {
	r.caRotation = m
	return r
}

// CARotationStatus is the §10.3 CA-rotation wire payload returned by GET
// and by each transition. It mirrors mtls.CARotationSnapshot: the
// current issuer, the full trust bundle, and the overlap window bounds
// so the operator runbook reads exactly which CA signs versus which CAs
// are trusted at the current stage.
type CARotationStatus struct {
	Stage            string   `json:"stage"`
	CurrentCaId      string   `json:"currentCaId"`
	TrustedCaIds     []string `json:"trustedCaIds"`
	OverlapStartedAt string   `json:"overlapStartedAt,omitempty"`
	OverlapClosesAt  string   `json:"overlapClosesAt,omitempty"`
}

// BeginCARotationRequest is the POST /begin body: the id of the new CA
// cert-manager has minted and the chart has rolled into the trust
// bundle.
type BeginCARotationRequest struct {
	NewCaId string `json:"newCaId"`
}

func caRotationStatusOf(snap mtls.CARotationSnapshot) CARotationStatus {
	return CARotationStatus{
		Stage:            string(snap.Stage),
		CurrentCaId:      snap.CurrentCAID,
		TrustedCaIds:     snap.TrustedCAIDs,
		OverlapStartedAt: rfc3339Nano(snap.OverlapStartedAt),
		OverlapClosesAt:  rfc3339Nano(snap.OverlapClosesAt),
	}
}

func (r *Router) handleGetCARotation(w http.ResponseWriter, req *http.Request) {
	snap, ok, err := r.caRotation.Status(req.Context())
	if err != nil {
		writeCARotationError(w, err)
		return
	}
	if !ok {
		// mTLS PKI is enabled but no rotation row has been seeded — treat
		// it as idle with no rotation in flight rather than 404 so the
		// operator command always reads a stable shape.
		writeJSON(w, http.StatusOK, CARotationStatus{Stage: string(mtls.CAStageIdle)})
		return
	}
	writeJSON(w, http.StatusOK, caRotationStatusOf(snap))
}

func (r *Router) handleBeginCARotation(w http.ResponseWriter, req *http.Request) {
	var body BeginCARotationRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	snap, err := r.caRotation.Begin(req.Context(), body.NewCaId)
	if err != nil {
		writeCARotationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, caRotationStatusOf(snap))
}

func (r *Router) handlePromoteCARotation(w http.ResponseWriter, req *http.Request) {
	snap, err := r.caRotation.Promote(req.Context())
	if err != nil {
		writeCARotationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, caRotationStatusOf(snap))
}

func (r *Router) handleRetireCARotation(w http.ResponseWriter, req *http.Request) {
	snap, err := r.caRotation.Retire(req.Context())
	if err != nil {
		writeCARotationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, caRotationStatusOf(snap))
}

// writeCARotationError maps a §10.3 rotation error to its HTTP status.
// Wrong-stage and overlap-still-open are 409 (the operator must wait or
// re-read the stage); a bad argument is 400; a concurrent stage write is
// 409. The audit row, when one was warranted, was already committed by
// the Manager before any failure here.
func writeCARotationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, carotation.ErrNotInitialized):
		writeError(w, http.StatusConflict, "CA_ROTATION_NOT_INITIALIZED",
			"CA rotation is not initialized (mTLS PKI disabled or not yet seeded)", nil)
		return
	case errors.Is(err, carotationstore.ErrConflict):
		writeError(w, http.StatusConflict, "CONCURRENT_MODIFICATION",
			"the rotation stage changed concurrently; re-read and retry", nil)
		return
	}
	var re *mtls.RotationError
	if errors.As(err, &re) {
		switch re.Kind {
		case "overlap_open":
			writeError(w, http.StatusConflict, "CA_ROTATION_OVERLAP_OPEN", re.Error(), nil)
		case "invalid_stage":
			writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION", re.Error(), nil)
		default:
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", re.Error(), nil)
		}
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}
