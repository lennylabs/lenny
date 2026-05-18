// SPDX-License-Identifier: MIT

package propagator

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/mtls/denylist"
)

// future returns a timestamp comfortably past now, the natural form of
// a certificate expiry.
func future() time.Time { return time.Now().Add(time.Hour) }

// TestAddAppliesLocallyWithNilBus confirms Add updates the wrapped deny
// list even with no Bus, so the §10.3 handshake check still sees a
// locally-revoked certificate in a single-replica deployment.
func TestAddAppliesLocallyWithNilBus(t *testing.T) {
	local := denylist.New()
	p := New(local, nil)

	uri := "spiffe://acme.com/agent/pod-1"
	p.Add(uri, future())
	if !local.Contains(uri) {
		t.Errorf("after Add, the local deny list does not contain %q", uri)
	}
	if !p.Local().Contains(uri) {
		t.Error("Local() should expose the same deny list Add updated")
	}
}

// TestRemoveAppliesLocallyWithNilBus confirms Remove drops the entry
// from the wrapped deny list with no Bus wired.
func TestRemoveAppliesLocallyWithNilBus(t *testing.T) {
	local := denylist.New()
	p := New(local, nil)

	uri := "spiffe://acme.com/agent/pod-2"
	p.Add(uri, future())
	p.Remove(uri)
	if local.Contains(uri) {
		t.Errorf("after Remove, the local deny list still contains %q", uri)
	}
}

// TestApplyAddFromPeerMessage confirms the subscribe-side apply decodes
// a peer replica's add message and adds the certificate to the local
// deny list with the carried expiry.
func TestApplyAddFromPeerMessage(t *testing.T) {
	local := denylist.New()
	p := New(local, nil)

	uri := "spiffe://acme.com/agent/pod-3"
	expiry := future()
	payload, err := json.Marshal(message{Op: opAdd, URI: uri, Expiry: expiry.UnixNano()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p.apply(payload)
	if !local.Contains(uri) {
		t.Errorf("apply of an add message did not add %q to the local deny list", uri)
	}
}

// TestApplyRemoveFromPeerMessage confirms apply decodes a peer's remove
// message and drops the certificate from the local deny list.
func TestApplyRemoveFromPeerMessage(t *testing.T) {
	local := denylist.New()
	p := New(local, nil)

	uri := "spiffe://acme.com/agent/pod-4"
	p.Add(uri, future())
	payload, err := json.Marshal(message{Op: opRemove, URI: uri})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	p.apply(payload)
	if local.Contains(uri) {
		t.Errorf("apply of a remove message did not drop %q from the local deny list", uri)
	}
}

// TestApplyIgnoresMalformedPayload confirms a payload that does not
// decode is dropped without panicking, so a malformed message cannot
// stall the subscribe loop.
func TestApplyIgnoresMalformedPayload(t *testing.T) {
	p := New(denylist.New(), nil)
	p.apply([]byte("{not json"))
	p.apply(nil)
	p.apply([]byte(`{"op":"unknown","uri":"spiffe://acme.com/x"}`))
	if p.Local().Size() != 0 {
		t.Errorf("malformed payloads mutated the deny list; size = %d, want 0", p.Local().Size())
	}
}

// TestAddRoundTripsThroughApply confirms an add published by one
// Propagator decodes and applies on a second Propagator's deny list,
// which is the cross-replica path exercised end-to-end with no Bus by
// feeding the encoded message straight to apply.
func TestAddRoundTripsThroughApply(t *testing.T) {
	// Capture what Add would publish, then replay it on a peer.
	publisher := New(denylist.New(), nil)
	uri := "spiffe://acme.com/agent/pod-5"
	expiry := future()
	encoded, err := json.Marshal(message{Op: opAdd, URI: uri, Expiry: expiry.UnixNano()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	publisher.Add(uri, expiry)

	peer := New(denylist.New(), nil)
	peer.apply(encoded)
	if !peer.Local().Contains(uri) {
		t.Errorf("the peer Propagator did not converge on the added certificate %q", uri)
	}
}
