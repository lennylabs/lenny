// SPDX-License-Identifier: MIT

// Package ocsf is the §11.7 wire-format translator. Every audit event
// leaving the Postgres hot tier — to the SIEM forwarder, the webhook
// subscribers, the EventBus `data` payload, and the §25 audit query
// responses — is serialized as an OCSF (Open Cybersecurity Schema
// Framework) v1.1.0 JSON record. OCSF is the single canonical wire
// format; there is no per-deployer format flag.
//
// The translator reads the canonical Postgres tuple field-by-field and
// maps Lenny's internal audit-row columns and payload fields onto OCSF
// class attributes per the §11.7 Lenny → OCSF field-mapping table. The
// mapping is deterministic and reversible: payload fields with no
// explicit OCSF mapping are routed verbatim into `unmapped.lenny.*`,
// and chain-integrity fields into `unmapped.lenny_chain.*`, so external
// tools can verify the hash chain without being OCSF-aware.
//
// Translation runs at the egress boundary, after the canonical row has
// already committed. A translation failure does not roll the row back;
// instead Translate returns a *TranslateError carrying the §11.7
// error_class, and the dead-letter path (DeadLetterReceipt) emits an
// OCSF Application Security Finding describing the failure so a single
// untranslatable event does not halt the per-tenant SIEM stream.
package ocsf

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lennylabs/lenny/pkg/audit"
)

// Version is the §11.7 pinned OCSF wire version. Upgrading it is a
// deployer-observable change: every active SIEM/webhook consumer must
// tolerate it. The version is advertised in every record via
// metadata.version and surfaced on the §25.9 query API envelope as
// ocsfVersion. There is no runtime-selectable dual-version emission.
const Version = "1.1.0"

// TranslatorVersion is the §4.4 line 232 / §11.7 / §25.9 OCSF
// translator-implementation version surfaced on every audit-egress
// response envelope as `translatorVersion`. The pair
// (TranslatorVersion, Version) lets a consumer correlate a given
// record's wire form with the exact mapping code that produced it; an
// off-by-one in the mapping table would otherwise be invisible to a
// SIEM whose records validate against the wire version alone. The
// constant moves only when the mapping code changes its observable
// output (a new payload-key route, a class_uid reassignment, a
// severity bump); a refactor with byte-identical output keeps the
// version pinned.
const TranslatorVersion = "1"

// productName / vendorName identify the emitting product in every
// OCSF record's metadata.product block.
const (
	productName = "Lenny"
	vendorName  = "Lenny"
)

// OCSF class_uid values. §11.7 assigns each Lenny event type to one of
// these classes. The catalog in mapping.go pins the per-event-type
// assignment.
const (
	ClassAuthentication     = 3002 // logon / credential events
	ClassAccountChange      = 3006 // account / user lifecycle
	ClassEntityManagement   = 5001 // resource create/update/delete
	ClassAPIActivity        = 6003 // admin API calls, generic activity
	ClassFileSystemActivity = 6004 // workspace file access
	ClassAppSecurityFinding = 2004 // security findings, translation-failure receipts
)

// category_uid values OCSF groups its classes under.
const (
	categoryIAM         = 3 // Identity & Access Management — classes 30xx
	categoryDiscovery   = 5 // Discovery — classes 50xx
	categoryApplication = 6 // Application Activity — classes 60xx
	categoryFindings    = 2 // Findings — classes 20xx
)

// OCSF activity_id values used by the event-type catalog.
const (
	ActivityUnknown = 0
	ActivityCreate  = 1
	ActivityRead    = 2
	ActivityUpdate  = 3
	ActivityDelete  = 4
	ActivityLogon   = 1 // Authentication: Logon
	// ActivityDisable is the OCSF AccountChange (3006) activity for
	// "Disable Account". spec: §11.4 invalidate-user fan-out emits
	// `admin.user.invalidated` for soft_disable / hard_disable / full_revoke;
	// the OCSF mapping uses activity_id 5 (Disable Account) so SIEM
	// consumers see a distinguished disable event rather than an unknown
	// AccountChange.
	ActivityDisable = 5
)

// OCSF disposition_id values: §11.7 maps payload.policy_result
// allow → Allowed (1), deny → Denied (2).
const (
	dispositionAllowed = 1
	dispositionDenied  = 2
)

// OCSF severity_id values. §16.7 annotates several audit events with a
// syslog-style severity word; these project onto the OCSF v1.1.0
// severity scale as INFO → Informational(1), Notice → Low(2),
// Warning → Medium(3), and Critical → Critical(5). Events §16.7 does
// not annotate default to Informational; a payload carrying
// policy_result=deny raises the floor to Medium so a security-salient
// denial is never reported below Medium. spec: §16.7. F-16.7.9.
const (
	severityInformational = 1
	severityLow           = 2 // §16.7 "Notice"
	severityMedium        = 3 // §16.7 "Warning"; also the policy-deny floor
	severityCritical      = 5 // §16.7 "Critical"
)

// severityCatalog pins the OCSF severity_id for the §16.7 events that
// carry an explicit severity. elicitation.content_tamper_detected is
// payload-dependent and is resolved in severityFor rather than here.
// spec: §16.7. F-16.7.9.
var severityCatalog = map[string]int{
	"compliance.profile_decommissioned":              severityCritical,      // §16.7 line 681
	"gdpr.erasure_blocked_by_hold":                   severityCritical,      // §16.7 line 693
	"gdpr.legal_hold_overridden":                     severityCritical,      // §16.7 line 693
	"gdpr.legal_hold_overridden_tenant":              severityCritical,      // §16.7 line 694
	"delegation.self_recursion_allowed":              severityLow,           // §16.7 line 670 (Notice)
	"delegation.cycle_warning":                       severityMedium,        // §16.7 line 671 (Warning)
	"gateway.cycle_detection_mode_changed":           severityLow,           // §16.7 line 672 (Notice)
	"deployment.feature_flag_downgrade_acknowledged": severityLow,           // §16.7 line 682 (Notice)
	"legal_hold.escrow_region_resolved":              severityInformational, // §16.7 line 694 (INFO)
	"node.drain.forced":                              severityCritical,      // §12 line 291 (critical)
	// §24.12 erasure-job operator recovery actions. Each is an
	// operator-reviewable policy action (a retry, or a manual Article 18
	// restriction clear), Notice severity. F-24.12.4.
	"gdpr.erasure_job_retried":            severityLow,
	"gdpr.processing_restriction_cleared": severityLow,
}

// severityFor returns the OCSF severity_id for an event type. Most
// events fall to Informational; the §16.7 events with an explicit
// severity are pinned in severityCatalog.
// elicitation.content_tamper_detected is Critical under enforcement
// mode `enforce` (the divergent forward was dropped) and Medium
// (Warning) under `detect-only` (the divergent payload was forwarded
// as received), keyed off the `enforcement_mode` payload field. An
// absent or unrecognised mode defaults to the Critical reading.
// spec: §16.7 lines 670-694. F-16.7.9.
func severityFor(eventType string, payload map[string]any) int {
	if eventType == "elicitation.content_tamper_detected" {
		if mode, ok := stringField(payload, "enforcement_mode"); ok && mode == "detect-only" {
			return severityMedium
		}
		return severityCritical
	}
	if sev, ok := severityCatalog[eventType]; ok {
		return sev
	}
	return severityInformational
}

// SeverityName maps an OCSF v1.1.0 severity_id to its canonical name.
// It lets the §25.9 audit query API match a `?severity=` filter against
// a translated record's severity_id by word (case-insensitive) or by
// the numeric id directly. spec: OCSF v1.1.0 severity_id dictionary.
func SeverityName(id int) string {
	switch id {
	case severityInformational:
		return "informational"
	case severityLow:
		return "low"
	case severityMedium:
		return "medium"
	case 4:
		return "high"
	case severityCritical:
		return "critical"
	case 6:
		return "fatal"
	default:
		return "unknown"
	}
}

// actor.user.type_id values: §11.7 maps caller_kind human → User (1),
// service → System (3), agent → Other (99).
const (
	userTypeUser   = 1
	userTypeSystem = 3
	userTypeOther  = 99
)

// ErrorClass is the §11.7 OCSF-translation error taxonomy. It labels
// the lenny_audit_ocsf_translation_failed_total metric and selects the
// dead-letter receipt's finding.title.
type ErrorClass string

const (
	// ErrSchemaViolation: the payload could not be parsed as a JSON
	// object, so no OCSF record can be produced from it.
	ErrSchemaViolation ErrorClass = "schema_violation"

	// ErrClassMappingMissing: the event_type has no entry in the
	// §11.7 event-type → OCSF class/activity catalog.
	ErrClassMappingMissing ErrorClass = "class_mapping_missing"

	// ErrTranslatorPanic: the translator panicked. Recovered and
	// reported so the row dead-letters rather than crashing the
	// egress goroutine.
	ErrTranslatorPanic ErrorClass = "translator_panic"

	// ErrOther: any failure not covered by the classes above.
	ErrOther ErrorClass = "other"
)

// AllErrorClasses returns the §11.7 error_class label values.
func AllErrorClasses() []ErrorClass {
	return []ErrorClass{ErrSchemaViolation, ErrClassMappingMissing, ErrTranslatorPanic, ErrOther}
}

// TranslateError is returned by Translate when a row cannot be
// rendered into OCSF. It carries the §11.7 error_class so the caller
// can label the failure metric and decide retry vs dead-letter.
type TranslateError struct {
	Class     ErrorClass
	EventType string
	Detail    string
}

func (e *TranslateError) Error() string {
	return fmt.Sprintf("ocsf: translate %q failed (%s): %s", e.EventType, e.Class, e.Detail)
}

// Record is an OCSF v1.1.0 JSON record. It is an ordered logical view;
// MarshalJSON emits the canonical OCSF object. Fields left zero are
// omitted from the wire form.
type Record struct {
	// ClassUID is the OCSF class (3002, 6003, …).
	ClassUID int

	// CategoryUID is the OCSF category the class belongs to.
	CategoryUID int

	// ActivityID is the OCSF activity within the class.
	ActivityID int

	// TypeUID is the OCSF composite type: class_uid*100 + activity_id.
	TypeUID int

	// Time is the event time as UNIX epoch milliseconds (OCSF requires
	// numeric epoch ms, not ISO 8601).
	Time int64

	// SeverityID is the OCSF severity. Informational (1) by default;
	// a denied policy result raises it to Medium (3).
	SeverityID int

	// Metadata is the OCSF metadata block (uid, sequence, version, …).
	Metadata Metadata

	// Actor carries the acting user (actor.user.uid / type / type_id).
	Actor *Actor

	// DispositionID is set for policy events: Allowed (1) / Denied (2).
	DispositionID int

	// StatusDetail carries a free-text detail (the denial reason).
	StatusDetail string

	// Resources is the affected-resource list (resources[0].type/uid).
	Resources []Resource

	// SrcEndpoint carries the source IP when the payload provides one.
	SrcEndpoint *Endpoint

	// HTTPRequest carries the user agent when the event came from HTTP.
	HTTPRequest *HTTPRequest

	// Finding carries the OCSF finding block (class 2004 only).
	Finding *Finding

	// Unmapped holds the §11.7 unmapped extension: lenny_chain.* for
	// the hash-chain fields and lenny.* for Lenny-specific payload
	// fields with no explicit OCSF mapping.
	Unmapped Unmapped
}

// Metadata is the OCSF metadata object.
type Metadata struct {
	UID            string            `json:"uid"`
	Sequence       uint64            `json:"sequence"`
	TenantUID      string            `json:"tenant_uid"`
	Version        string            `json:"version"`
	CorrelationUID string            `json:"correlation_uid,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Product        Product           `json:"product"`
}

// Product is the OCSF product descriptor.
type Product struct {
	Name       string `json:"name"`
	VendorName string `json:"vendor_name"`
}

// Actor is the OCSF actor object (the subset Lenny populates).
type Actor struct {
	User User `json:"user"`
}

// User is the OCSF user object.
type User struct {
	UID    string `json:"uid,omitempty"`
	Type   string `json:"type,omitempty"`
	TypeID int    `json:"type_id,omitempty"`
}

// Resource is one OCSF resource_details entry.
type Resource struct {
	Type string `json:"type,omitempty"`
	UID  string `json:"uid,omitempty"`
}

// Endpoint is the OCSF network_endpoint object (IP subset).
type Endpoint struct {
	IP string `json:"ip,omitempty"`
}

// HTTPRequest is the OCSF http_request object (user-agent subset).
type HTTPRequest struct {
	UserAgent string `json:"user_agent,omitempty"`
}

// Finding is the OCSF finding object (class 2004).
type Finding struct {
	Title string `json:"title"`
	UID   string `json:"uid,omitempty"`
}

// Unmapped is the §11.7 unmapped extension block.
type Unmapped struct {
	// Chain carries prev_hash, integrity, and the genesis nonce.
	Chain ChainExt `json:"lenny_chain"`

	// Lenny holds every payload field with no explicit OCSF mapping,
	// preserved verbatim, plus event_schema_version.
	Lenny map[string]any `json:"lenny,omitempty"`
}

// ChainExt is the §11.7 unmapped.lenny_chain extension. It surfaces
// the hash chain so external tools can verify without being OCSF-aware.
type ChainExt struct {
	PrevHash     string `json:"prev_hash"`
	Integrity    string `json:"integrity"`
	GenesisNonce string `json:"genesis_nonce,omitempty"`
}

// MarshalJSON emits the canonical OCSF object for the record.
func (r Record) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"class_uid":    r.ClassUID,
		"category_uid": r.CategoryUID,
		"activity_id":  r.ActivityID,
		"type_uid":     r.TypeUID,
		"time":         r.Time,
		"severity_id":  r.SeverityID,
		"metadata":     r.Metadata,
		"unmapped":     r.Unmapped,
	}
	if r.Actor != nil {
		m["actor"] = r.Actor
	}
	if r.DispositionID != 0 {
		m["disposition_id"] = r.DispositionID
		m["disposition"] = dispositionName(r.DispositionID)
	}
	if r.StatusDetail != "" {
		m["status_detail"] = r.StatusDetail
	}
	if len(r.Resources) > 0 {
		m["resources"] = r.Resources
	}
	if r.SrcEndpoint != nil {
		m["src_endpoint"] = r.SrcEndpoint
	}
	if r.HTTPRequest != nil {
		m["http_request"] = r.HTTPRequest
	}
	if r.Finding != nil {
		m["finding"] = r.Finding
	}
	return json.Marshal(m)
}

func dispositionName(id int) string {
	switch id {
	case dispositionAllowed:
		return "Allowed"
	case dispositionDenied:
		return "Denied"
	}
	return "Unknown"
}

// Input is the canonical Postgres-tuple view the translator reads. It
// is the §11.7 source of truth: every field here comes from the
// audit_log row, never from caller-supplied wire bytes.
type Input struct {
	// ID is the row UUID → metadata.uid.
	ID string

	// Sequence is the per-tenant sequence_number → metadata.sequence.
	Sequence uint64

	// TenantID is the chain selector → metadata.tenant_uid.
	TenantID string

	// EventType drives class_uid + activity_id via the §11.7 catalog.
	EventType string

	// EventSchemaVersion echoes onto unmapped.lenny.event_schema_version
	// so verifiers can locate the schema that was current at hash time.
	EventSchemaVersion string

	// CreatedAtUnixMs is created_at as UNIX epoch ms → time.
	CreatedAtUnixMs int64

	// Payload is the complete Lenny-internal event payload (the
	// audit_log payload jsonb). Mapped field-by-field.
	Payload json.RawMessage

	// PrevHash is the chain link → unmapped.lenny_chain.prev_hash.
	PrevHash string

	// ChainIntegrity is the verifier state → unmapped.lenny_chain.integrity.
	ChainIntegrity audit.ChainIntegrity

	// GenesisNonce is set on the first row of a tenant chain only →
	// unmapped.lenny_chain.genesis_nonce.
	GenesisNonce string
}

// these payload keys have an explicit OCSF mapping in §11.7. Every
// other payload key is routed verbatim into unmapped.lenny.*.
var mappedPayloadKeys = map[string]bool{
	"policy_result": true,
	"denial_reason": true,
	"resource_type": true,
	"resource_id":   true,
	"source_ip":     true,
	"user_agent":    true,
	"caller_kind":   true,
	"user_id":       true,
	"operation_id":  true,
	"session_id":    true,
}

// Translate renders one canonical audit row into its OCSF v1.1.0
// record per the §11.7 field mapping. It returns a *TranslateError
// carrying the §11.7 error_class on failure; the caller drives the
// retry / dead-letter state machine on that class.
func Translate(in Input) (rec Record, err error) {
	defer func() {
		if r := recover(); r != nil {
			rec = Record{}
			err = &TranslateError{
				Class:     ErrTranslatorPanic,
				EventType: in.EventType,
				Detail:    fmt.Sprintf("%v", r),
			}
		}
	}()

	class, ok := LookupClass(in.EventType)
	if !ok {
		return Record{}, &TranslateError{
			Class:     ErrClassMappingMissing,
			EventType: in.EventType,
			Detail:    "event type has no entry in the §11.7 OCSF class/activity catalog",
		}
	}

	payload := map[string]any{}
	if len(in.Payload) > 0 && string(in.Payload) != "null" {
		if e := json.Unmarshal(in.Payload, &payload); e != nil {
			return Record{}, &TranslateError{
				Class:     ErrSchemaViolation,
				EventType: in.EventType,
				Detail:    "payload is not a JSON object: " + e.Error(),
			}
		}
	}

	rec = Record{
		ClassUID:    class.ClassUID,
		CategoryUID: class.CategoryUID,
		ActivityID:  class.ActivityID,
		TypeUID:     class.ClassUID*100 + class.ActivityID,
		Time:        in.CreatedAtUnixMs,
		SeverityID:  severityFor(in.EventType, payload), // §16.7 per-event severity; a policy denial raises the floor below. F-16.7.9.
		Metadata: Metadata{
			UID:       in.ID,
			Sequence:  in.Sequence,
			TenantUID: in.TenantID,
			Version:   Version,
			Product:   Product{Name: productName, VendorName: vendorName},
		},
		Unmapped: Unmapped{
			Chain: ChainExt{
				PrevHash:     in.PrevHash,
				Integrity:    string(in.ChainIntegrity),
				GenesisNonce: in.GenesisNonce,
			},
		},
	}

	// actor.user.uid from payload.user_id.
	if uid, ok := stringField(payload, "user_id"); ok {
		rec.Actor = &Actor{User: User{UID: uid}}
	}
	// caller_kind → actor.user.type + type_id.
	if kind, ok := stringField(payload, "caller_kind"); ok {
		if rec.Actor == nil {
			rec.Actor = &Actor{}
		}
		switch kind {
		case "human":
			rec.Actor.User.Type, rec.Actor.User.TypeID = "User", userTypeUser
		case "service":
			rec.Actor.User.Type, rec.Actor.User.TypeID = "System", userTypeSystem
		case "agent":
			rec.Actor.User.Type, rec.Actor.User.TypeID = "Agent", userTypeOther
		}
	}
	// operation_id → metadata.correlation_uid.
	if opID, ok := stringField(payload, "operation_id"); ok {
		rec.Metadata.CorrelationUID = opID
	}
	// session_id → metadata.labels["lenny.session_id"].
	if sid, ok := stringField(payload, "session_id"); ok {
		rec.Metadata.Labels = map[string]string{"lenny.session_id": sid}
	}
	// policy_result → disposition + disposition_id.
	if pr, ok := stringField(payload, "policy_result"); ok {
		switch pr {
		case "allow":
			rec.DispositionID = dispositionAllowed
		case "deny":
			rec.DispositionID = dispositionDenied
			// A denial is security-salient: never report it below Medium.
			// An event whose §16.7 severity is already higher (e.g.
			// Critical) keeps its higher value. F-16.7.9.
			if rec.SeverityID < severityMedium {
				rec.SeverityID = severityMedium
			}
		}
	}
	// denial_reason → status_detail.
	if dr, ok := stringField(payload, "denial_reason"); ok {
		rec.StatusDetail = dr
	}
	// resource_type / resource_id → resources[0].
	rtype, hasType := stringField(payload, "resource_type")
	rid, hasID := stringField(payload, "resource_id")
	if hasType || hasID {
		rec.Resources = []Resource{{Type: rtype, UID: rid}}
	}
	// source_ip → src_endpoint.ip.
	if ip, ok := stringField(payload, "source_ip"); ok {
		rec.SrcEndpoint = &Endpoint{IP: ip}
	}
	// user_agent → http_request.user_agent.
	if ua, ok := stringField(payload, "user_agent"); ok {
		rec.HTTPRequest = &HTTPRequest{UserAgent: ua}
	}

	// Every remaining payload field goes verbatim into unmapped.lenny.*,
	// plus the event_schema_version echo so verifiers locate the schema.
	lenny := map[string]any{}
	for k, v := range payload {
		if !mappedPayloadKeys[k] {
			lenny[k] = v
		}
	}
	if in.EventSchemaVersion != "" {
		lenny["event_schema_version"] = in.EventSchemaVersion
	}
	if len(lenny) > 0 {
		rec.Unmapped.Lenny = lenny
	}
	return rec, nil
}

// DeadLetterReceipt builds the §11.7 translation-failure receipt — an
// OCSF Application Security Finding (class 2004) emitted in place of an
// untranslatable event so the SIEM delivery pointer can advance past a
// persistently-failing row. The receipt is schema-compliant by
// construction. The original payload is preserved as an opaque base64
// unmapped.lenny.raw_canonical_b64 field so a later translator version
// can re-translate it; the failure error_class is in finding.title.
func DeadLetterReceipt(in Input, te *TranslateError) Record {
	rec := Record{
		ClassUID:    ClassAppSecurityFinding,
		CategoryUID: categoryFindings,
		ActivityID:  ActivityCreate,
		TypeUID:     ClassAppSecurityFinding*100 + ActivityCreate,
		Time:        in.CreatedAtUnixMs,
		SeverityID:  3, // Medium — a dead-letter is operator-visible.
		Metadata: Metadata{
			UID:       in.ID,
			Sequence:  in.Sequence,
			TenantUID: in.TenantID,
			Version:   Version,
			Product:   Product{Name: productName, VendorName: vendorName},
		},
		Finding: &Finding{
			Title: fmt.Sprintf("OCSF translation failed: %s", te.Class),
			UID:   in.ID,
		},
		Unmapped: Unmapped{
			Chain: ChainExt{
				PrevHash:     in.PrevHash,
				Integrity:    string(in.ChainIntegrity),
				GenesisNonce: in.GenesisNonce,
			},
			Lenny: map[string]any{
				"raw_canonical_b64":    base64.StdEncoding.EncodeToString(rawCanonical(in)),
				"failed_event_type":    in.EventType,
				"translation_error":    te.Detail,
				"event_schema_version": in.EventSchemaVersion,
			},
		},
	}
	return rec
}

// rawCanonical returns the bytes of the canonical payload for the
// dead-letter receipt's raw_canonical_b64 field. A nil/empty payload
// becomes the JSON null literal.
func rawCanonical(in Input) []byte {
	if len(in.Payload) == 0 {
		return []byte("null")
	}
	return in.Payload
}

// stringField extracts a string-valued key from a payload map. The
// second return is false when the key is absent or not a string.
func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// MarshalRecord renders a record to its canonical OCSF JSON bytes.
func MarshalRecord(rec Record) ([]byte, error) {
	return json.Marshal(rec)
}

// SortedKeys returns the keys of m sorted, for deterministic
// diagnostics output.
func SortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
