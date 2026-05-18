// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract scaffolds for §12.3.7 CloudEvents envelope.
//
// TestCloudEventsEnvelopeShape, TestCloudEventsLennyExtensions, and
// TestCloudEventsTenantPrefixedChannels are implemented in
// cloudevents_test.go, which exercises the §12.3.7 CloudEvents v1.0.2
// envelope produced by pkg/gateway/eventbus: the required context
// attributes, the lenny-prefixed extension attributes, the
// audit-bearing datacontenttype discriminator, and the §12.4
// tenant-prefixed channel-name convention.

package cloudevents_test
