// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// §4.9 direct-mode lease-expiry timer.
//
// In direct delivery mode the runtime holds the real upstream credential
// in its credential file, so the gateway cannot enforce lease expiry on
// the upstream path the way it does for proxy mode. The adapter MUST set
// a local timer for each direct-mode lease's expiresAt; when the timer
// fires without a replacement lease having been delivered via
// RotateCredentials, the adapter deletes that provider's entry from the
// credential file and reports AUTH_EXPIRED on CH-ADAPTEREVENTS,
// triggering the standard fallback flow. This caps a long-lived key
// (e.g. anthropic_direct, whose key does not itself expire) at the lease
// boundary. Proxy-mode leases get no timer: the gateway rejects expired
// proxy requests server-side.
//
// spec: §4.9.

// expiryTimerHandle is the subset of *time.Timer the expiry tracker
// needs; the test seam swaps in a fake.
type expiryTimerHandle interface {
	Stop() bool
}

// expiryTimer is one armed direct-mode lease-expiry timer. leaseID
// records the lease the timer was armed for so a fired timer can tell
// whether the lease it guarded is still current (not yet replaced by a
// rotation).
type expiryTimer struct {
	leaseID string
	handle  expiryTimerHandle
}

// expiryAfter schedules f to run after d, using the injected test seam
// when set and time.AfterFunc otherwise. A d at or below zero fires
// immediately, which deletes an already-expired direct-mode lease on the
// next acquisition of s.mu.
func (s *Server) expiryAfter(d time.Duration, f func()) expiryTimerHandle {
	if s.ExpiryAfterFunc != nil {
		return s.ExpiryAfterFunc(d, f)
	}
	return time.AfterFunc(d, f)
}

// expiryNow returns the current time through the injected test seam when
// set and time.Now otherwise.
func (s *Server) expiryClockNow() time.Time {
	if s.ExpiryNow != nil {
		return s.ExpiryNow()
	}
	return time.Now()
}

// directDeliveryMode is the §4.9 deliveryMode value that arms the
// adapter expiry timer. It matches credential.DeliveryDirect; the
// adapter reads it from the lease payload rather than importing the
// gateway credential package.
const directDeliveryMode = "direct"

// leaseDeliveryMode reads the §4.9 deliveryMode out of a credential
// lease's opaque payload. The gateway encodes the payload as
// {"deliveryMode": "...", "materializedConfig": {...}} (see
// credassign.ProtoLease); the adapter inspects only the discriminator to
// decide whether to arm an expiry timer. A missing or malformed payload
// yields the empty string, which leaves the lease without a timer.
func leaseDeliveryMode(lease *adapterv1.CredentialLease) string {
	payload := lease.GetPayload()
	if len(payload) == 0 {
		return ""
	}
	var doc struct {
		DeliveryMode string `json:"deliveryMode"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return ""
	}
	return doc.DeliveryMode
}
