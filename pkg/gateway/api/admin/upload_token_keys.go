// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/signingkeys"
)

// UploadTokenKeyRotator forces a rotation of the shared upload-token
// signing keys through the write a scheduled rotation uses.
// *signingkeys.Ring satisfies it.
type UploadTokenKeyRotator interface {
	RotateNow(ctx context.Context) (signingkeys.Rotation, error)
}

// eventUploadTokenKeyRotated is the audit event a forced rotation emits.
const eventUploadTokenKeyRotated = "platform.upload_token_key_rotated"

// WithUploadTokenKeyRotator wires the upload-token key ring the forced
// rotation route writes through. Without one the route is not registered.
func (r *Router) WithUploadTokenKeyRotator(rot UploadTokenKeyRotator) *Router {
	r.uploadTokenKeys = rot
	return r
}

// registerUploadTokenKeyRoutes registers POST
// /v1/admin/upload-token-keys/rotate behind the platform-admin gate when a
// rotator is wired.
func (r *Router) registerUploadTokenKeyRoutes(mux *http.ServeMux) {
	if r.uploadTokenKeys == nil {
		return
	}
	mux.Handle("POST /v1/admin/upload-token-keys/rotate",
		r.requireAdmin(http.HandlerFunc(r.handleRotateUploadTokenKeys)))
}

// uploadTokenKeyRotation is the response body of a forced rotation: the
// key it wrote. It carries no secret.
type uploadTokenKeyRotation struct {
	KeyID        string    `json:"keyId"`
	RotationSlot int64     `json:"rotationSlot"`
	ActiveFrom   time.Time `json:"activeFrom"`
}

// handleRotateUploadTokenKeys writes the next upload-token signing key at
// once, one publish lead ahead of its activation, and emits
// platform.upload_token_key_rotated. The rotation itself is
// signingkeys.Ring.RotateNow, which every protocol front can call; this
// handler maps its outcome to HTTP. A rotation that cannot read or write
// the key store, wrap the new secret, or win a slot answers 503
// SERVICE_UNAVAILABLE with a retry hint and emits nothing.
//
// spec: §7.1 (uploadToken format and security properties), §15.1 (REST
// API)
func (r *Router) handleRotateUploadTokenKeys(w http.ResponseWriter, req *http.Request) {
	rot, err := r.uploadTokenKeys.RotateNow(req.Context())
	if err != nil {
		msg := "upload-token key rotation failed: " + err.Error()
		if errors.Is(err, signingkeys.ErrRotationContended) {
			msg = "other writers took every rotation slot the rotation tried"
		}
		writeErrorRetryAfter(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", msg, nil, time.Second)
		return
	}
	p, _ := authmw.FromContext(req.Context())
	r.emit(req.Context(), p, eventUploadTokenKeyRotated, "upload-token-keys", map[string]any{
		"key_id":        rot.KeyID,
		"rotation_slot": rot.RotationSlot,
		"active_from":   rot.ActiveFrom.UTC().Format(time.RFC3339Nano),
	})
	writeJSON(w, http.StatusOK, uploadTokenKeyRotation{
		KeyID:        rot.KeyID,
		RotationSlot: rot.RotationSlot,
		ActiveFrom:   rot.ActiveFrom.UTC(),
	})
}
