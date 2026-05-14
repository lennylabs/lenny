// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract scaffolds for §12.3.7 CloudEvents envelope. The
// EventBus publishes CloudEvents v1.0.2 messages. The suite asserts
// every documented event type maps to the correct envelope shape,
// with the documented Lenny extensions (lennytenantid,
// lennyrootsessionid, lennyoperationid).

package cloudevents_test

import "testing"

// TestCloudEventsEnvelopeShape — every published event has the
// required CloudEvents v1.0.2 fields and the documented Lenny
// extensions.
func TestCloudEventsEnvelopeShape(t *testing.T) {
	t.Skip("not implemented: §12.3.7 CloudEvents envelope — requires the EventBus implementation and an event-publication harness that captures the wire envelope for assertion")
}

// TestCloudEventsLennyExtensions — lennytenantid, lennyrootsessionid,
// lennyoperationid are present on every event and carry the expected
// values.
func TestCloudEventsLennyExtensions(t *testing.T) {
	t.Skip("not implemented: §12.3.7 CloudEvents Lenny extensions — requires the EventBus publisher to populate the lenny-prefixed extension attributes")
}

// TestCloudEventsTenantPrefixedChannels — events are published on
// tenant-prefixed channels and the test confirms a subscriber on
// tenant A never receives tenant B events.
func TestCloudEventsTenantPrefixedChannels(t *testing.T) {
	t.Skip("not implemented: §12.3.7 tenant-prefixed channels — requires the Redis-backed EventBus and a per-tenant subscriber harness")
}
