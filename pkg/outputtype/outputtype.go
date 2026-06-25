// SPDX-License-Identifier: MIT

// Package outputtype encodes the §15.4.1 canonical MessagePart type
// registry (v1) and the ingress classification rule the gateway applies
// to a runtime's emitted `type` string.
//
// The MessagePart `type` is an open string, not a closed enum. Names in
// the registry below are platform-defined; the `x-<vendor>/` namespace
// is reserved for third-party extension types. Any other value is a
// custom type: it collapses to `text` with the original type preserved
// in `annotations.originalType`. An unknown name that is neither
// registered nor vendor-namespaced also earns an `unregistered_platform_type`
// warning annotation at ingress so a type added in a later minor release
// is forward-compatible across gateways that pre-date it.
//
// spec: §15.4.1 lines 1503, 1522 — Canonical Type Registry (v1) and the
// namespace convention for third-party types.
package outputtype

import "strings"

// MaxKnownSchemaVersion is the highest §15.4.1 MessagePart schemaVersion
// the v1 gateway understands. A part stamped above this triggers the
// `schema_version_ahead` live-consumer degradation signal: the gateway
// forward-reads the fields it knows and annotates the enclosing envelope.
//
// spec: §15.4.1 lines 1499-1501.
const MaxKnownSchemaVersion = 1

// Canonical lists the §15.4.1 v1 registry types in registry order. A
// producer may emit any of these and the gateway translates them per the
// per-adapter fidelity matrix without a fallback.
//
// spec: §15.4.1 — Canonical Type Registry (v1).
var Canonical = []string{
	"text",
	"code",
	"reasoning_trace",
	"citation",
	"screenshot",
	"image",
	"diff",
	"file",
	"execution_result",
	"error",
}

// vendorPrefix is the reverse-DNS namespace marker every third-party
// type carries (`x-<vendor>/<typeName>`). A type bearing it is a
// deliberate extension, so ingress does not warn on it.
//
// spec: §15.4.1 line 1522 — "all vendor- or community-defined custom
// types MUST use a reverse-DNS namespace prefix in the form `x-<vendor>/`".
const vendorPrefix = "x-"

var canonicalSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(Canonical))
	for _, t := range Canonical {
		m[t] = struct{}{}
	}
	return m
}()

// IsCanonical reports whether t is a v1 registry type.
func IsCanonical(t string) bool {
	_, ok := canonicalSet[t]
	return ok
}

// IsVendorNamespaced reports whether t uses the reserved `x-<vendor>/`
// third-party namespace. A bare `x-foo` with no slash is not a valid
// namespaced type and is treated as an unregistered unprefixed name.
//
// spec: §15.4.1 line 1522.
func IsVendorNamespaced(t string) bool {
	if !strings.HasPrefix(t, vendorPrefix) {
		return false
	}
	rest := t[len(vendorPrefix):]
	slash := strings.IndexByte(rest, '/')
	return slash > 0 && slash < len(rest)-1
}

// Unregistered reports whether t triggers the §15.4.1 line 1522
// `unregistered_platform_type` warning at ingress: an unprefixed name
// that is not in the v1 registry. Registered types and properly
// vendor-namespaced types do not warn. An empty type is not classified
// as unregistered — the producer omitted the field and the part defaults
// to `text` elsewhere.
//
// spec: §15.4.1 lines 1503, 1522.
func Unregistered(t string) bool {
	if t == "" || IsCanonical(t) || IsVendorNamespaced(t) {
		return false
	}
	return true
}
