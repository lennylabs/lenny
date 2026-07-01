// SPDX-License-Identifier: MIT

package events

// Operational-event datacontenttype values. The §25.5 single-envelope
// model carries the payload directly in the CloudEvents data field: a
// non-audit event carries an event-specific JSON object, an
// audit-bearing event carries the §11.7 OCSF v1.1.0 record. The
// CloudEvents envelope is the transport; the data field is the payload;
// there is no intermediate container. spec: §25.3 line 652; §25.5 line
// 2556.
const (
	// ContentTypeJSON is the datacontenttype a non-audit operational
	// event sets.
	ContentTypeJSON = "application/json"

	// ContentTypeOCSF is the datacontenttype an audit-bearing
	// operational event sets — its data field carries the §11.7 OCSF
	// v1.1.0 record.
	ContentTypeOCSF = "application/ocsf+json"
)

// IsAuditBearing reports whether e carries an OCSF audit record in its
// data field (datacontenttype application/ocsf+json), per the §25.5
// single-envelope model. A consumer that sees true parses data as the
// §11.7 OCSF v1.1.0 record; false means data is the event-specific JSON
// object documented per type in the §16.6 catalogue. spec: §25.5 line
// 2556.
func (e OperationalEvent) IsAuditBearing() bool {
	return e.DataContentType == ContentTypeOCSF
}
