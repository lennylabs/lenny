// SPDX-License-Identifier: MIT

//go:build contract

// Cross-checks Lenny's CloudEvents envelope against the published
// CloudEvents v1.0.2 JSON Schema, vendored at
// tests/testdata/cloudevents/v1.0.2-cloudevents.schema.json (see the
// README next to it for provenance), rather than against Lenny's own
// eventbus.Event.Validate(). The rest of this package's tests confirm the
// envelope satisfies Lenny's reading of §12.3.7; they do not catch an
// envelope that is well-formed per Lenny's own validator but violates the
// externally published CloudEvents contract (a malformed uri-reference
// source, a non-RFC-3339 time the schema's date-time format rejects, or
// an extension attribute name the CloudEvents attribute-naming convention
// forbids). This file closes that gap.
package cloudevents_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storage/eventbus"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// extensionAttributeNamePattern is the CloudEvents "Attribute Naming
// Convention" rule the vendored JSON Schema does not encode (the schema
// permits any additional top-level property name): "CloudEvents attribute
// names MUST consist of lower-case letters ('a' to 'z') or digits ('0' to
// '9') from the ASCII character set" (CloudEvents spec v1.0.2, "Attribute
// Naming Convention").
var extensionAttributeNamePattern = regexp.MustCompile(`^[a-z0-9]+$`)

// rfc2046MediaTypePattern approximates the RFC 2045/2046 `type "/" subtype`
// token grammar for the `datacontenttype` attribute. The vendored JSON
// Schema only requires a non-empty string (or null); the CloudEvents spec
// itself requires an RFC 2046 media type: "Type: String per RFC 2046 ...
// Constraints: ... If present, MUST adhere to the format specified in
// RFC 2046" (CloudEvents spec v1.0.2, "datacontenttype").
var rfc2046MediaTypePattern = regexp.MustCompile(`^[!#$&\-\^_.+0-9A-Za-z]+/[!#$&\-\^_.+0-9A-Za-z]+$`)

// spec: 12.3.7
// diagnosis: a failure here means a marshalled EventBus envelope no
// longer validates against the externally published CloudEvents v1.0.2
// JSON Schema, or violates an attribute-naming or media-type constraint
// from the CloudEvents spec prose the schema does not encode. Either
// failure is a wire-interop defect: a CloudEvents-conformant consumer
// (an off-the-shelf CloudEvents SDK or broker) would reject or
// misinterpret the envelope even though Lenny's own eventbus.Event.Validate
// reports it as conformant.
func TestCloudEventsEnvelopeMatchesPublishedSchema(t *testing.T) {
	sch := schematest.Compile(t, "tests/testdata/cloudevents/v1.0.2-cloudevents.schema.json")

	for _, tc := range []struct {
		name string
		in   eventbus.NewEventInput
	}{
		{
			name: "plain control event",
			in: eventbus.NewEventInput{
				TenantID: "acme", PublisherID: "gw-1", ShortName: "session_state_changed",
				Subject: "session/s",
			},
		},
		{
			name: "delegation-tree event with root-session and operation extensions",
			in: eventbus.NewEventInput{
				TenantID: "acme", PublisherID: "gw-1", ShortName: "delegation_tree_completed",
				Subject: "tree/root-9", RootSessionID: "root-9", OperationID: "op-7",
			},
		},
		{
			name: "audit-bearing OCSF event",
			in: eventbus.NewEventInput{
				TenantID: "acme", PublisherID: "gw-1", ShortName: "session_state_changed",
				Subject: "session/s",
				Data:    json.RawMessage(`{"class_uid":3002,"metadata":{"version":"1.1.0"}}`),
				OCSF:    true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := eventbus.NewEvent(tc.in)
			if err != nil {
				t.Fatalf("NewEvent: %v", err)
			}

			b, err := json.Marshal(ev)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var instance any
			if err := json.Unmarshal(b, &instance); err != nil {
				t.Fatalf("decode wire JSON: %v", err)
			}
			if err := sch.Validate(instance); err != nil {
				t.Errorf("envelope does not validate against the published CloudEvents v1.0.2 schema: %v", err)
			}

			// The JSON Schema permits any additional top-level property
			// name; the CloudEvents attribute-naming convention does not.
			for name := range ev.Extensions {
				if !extensionAttributeNamePattern.MatchString(name) {
					t.Errorf("extension attribute %q is not lower-case ASCII letters or digits, "+
						"violating the CloudEvents attribute-naming convention", name)
				}
			}

			// The JSON Schema only requires datacontenttype to be a
			// non-empty string; the CloudEvents spec requires an RFC 2046
			// media type.
			if !rfc2046MediaTypePattern.MatchString(ev.DataContentType) {
				t.Errorf("datacontenttype %q is not a valid RFC 2046 media type", ev.DataContentType)
			}
		})
	}
}
