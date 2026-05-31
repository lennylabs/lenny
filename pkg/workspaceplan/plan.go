// SPDX-License-Identifier: MIT

// Package workspaceplan parses and validates the §14 WorkspacePlan JSON
// document submitted via `CreateSessionRequest.workspacePlan`. The
// parser is pure Go: no filesystem I/O, no archive parsing, no
// network. It returns a typed plan plus a slice of structured
// warnings (for unknown source types and other consumer-side advisory
// signals).
//
// The wire schema is canonically defined in
// schemas/workspaceplan-v1.json. This package mirrors that schema and
// adds the per-§14 invariants that JSON Schema 2020-12 cannot
// express:
//
//   - Mode-string setuid / setgid rejection across all variants
//     (§14 "mode field notes").
//   - Mode-string sticky-bit rejection on file variants (inlineFile,
//     uploadFile); sticky permitted on `mkdir`.
//   - Path traversal rejection (no `..` segment; no absolute paths;
//     <= 32 components; <= 4096 UTF-8 bytes).
//   - `gitClone.resolvedCommitSha` is gateway-written; clients that
//     supply it are rejected with reason `gateway_written_field`.
//   - `schemaVersion` strict equality with the known constant.
//
// Per-variant errors carry the §14 `details.field` /
// `details.reason` payload the gateway echoes back in the
// `400 WORKSPACE_PLAN_INVALID` envelope.
package workspaceplan

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// SchemaVersion is the §14.1 wire-compat identifier for v1.
const SchemaVersion = 1

// Plan is the parsed root document.
type Plan struct {
	SchemaVersion int            `json:"schemaVersion"`
	Sources       []Source       `json:"sources"`
	SetupCommands []SetupCommand `json:"setupCommands,omitempty"`
}

// Source is the §14 union over the five variant types
// (inlineFile, uploadFile, uploadArchive, mkdir, gitClone). Unknown
// variants are preserved with an empty TypedVariant and surface as a
// `workspace_plan_unknown_source_type` warning per §14.
type Source struct {
	Type    string         `json:"type"`
	Variant SourceVariant  `json:"-"`
	Raw     map[string]any `json:"-"`
}

// SourceVariant is implemented by each typed §14 variant.
type SourceVariant interface {
	sourceVariantTag()
	// Path returns the workspace-relative destination this variant
	// writes to. The parser fills in §14 spec defaults for optional
	// path fields (e.g., gitClone.path defaults to "." per §14 line
	// 91) so the returned value is always non-empty for a validated
	// source.
	Path() string
}

// InlineFile is the §14 inlineFile variant.
type InlineFile struct {
	PathField string `json:"path"`
	Content   string `json:"content"`
	Mode      string `json:"mode,omitempty"`
}

func (InlineFile) sourceVariantTag() {}
func (f InlineFile) Path() string    { return f.PathField }

// UploadFile is the §14 uploadFile variant.
type UploadFile struct {
	PathField string `json:"path"`
	UploadRef string `json:"uploadRef"`
	Mode      string `json:"mode,omitempty"`
}

func (UploadFile) sourceVariantTag() {}
func (f UploadFile) Path() string    { return f.PathField }

// UploadArchive is the §14 uploadArchive variant.
type UploadArchive struct {
	PathPrefix      string `json:"pathPrefix"`
	UploadRef       string `json:"uploadRef"`
	Format          string `json:"format"`
	StripComponents int    `json:"stripComponents,omitempty"`
}

func (UploadArchive) sourceVariantTag() {}
func (a UploadArchive) Path() string    { return a.PathPrefix }

// Mkdir is the §14 mkdir variant.
type Mkdir struct {
	PathField string `json:"path"`
	Mode      string `json:"mode,omitempty"`
}

func (Mkdir) sourceVariantTag() {}
func (m Mkdir) Path() string    { return m.PathField }

// GitClone is the §14 gitClone variant.
type GitClone struct {
	URL               string        `json:"url"`
	Ref               string        `json:"ref"`
	PathField         string        `json:"path,omitempty"`
	Depth             int           `json:"depth,omitempty"`
	Submodules        bool          `json:"submodules,omitempty"`
	Auth              *GitCloneAuth `json:"auth,omitempty"`
	ResolvedCommitSha string        `json:"resolvedCommitSha,omitempty"` // gateway-written
}

func (GitClone) sourceVariantTag() {}
func (g GitClone) Path() string    { return g.PathField }

// GitCloneAuth is the §14 gitClone.auth object.
type GitCloneAuth struct {
	Mode       string `json:"mode"`
	LeaseScope string `json:"leaseScope"`
}

// SetupCommand is the §14 setupCommand object.
type SetupCommand struct {
	Cmd            string `json:"cmd"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// VariantType constants — the §14 source.type discriminator values.
const (
	TypeInlineFile    = "inlineFile"
	TypeUploadFile    = "uploadFile"
	TypeUploadArchive = "uploadArchive"
	TypeMkdir         = "mkdir"
	TypeGitClone      = "gitClone"
)

// WarningCode is the closed §14 consumer-warning enum that this
// package emits. The values match the spec wording for downstream
// observability emitters.
type WarningCode string

const (
	WarnUnknownSourceType         WarningCode = "workspace_plan_unknown_source_type"
	WarnStripComponentsSkip       WarningCode = "workspace_plan_strip_components_skip"
	WarnPathCollision             WarningCode = "workspace_plan_path_collision"
	WarnDurableSchemaVersionAhead WarningCode = "workspace_plan_durable_schema_version_ahead"
)

// Warning is a non-fatal advisory the parser raised against a plan.
//
// The per-warning structured-field set follows §14:
//   - `workspace_plan_unknown_source_type` (spec: §14 line 334): the
//     warning carries `schemaVersion` (the plan's declared schema
//     version, copied so a downstream consumer that filters on warnings
//     can correlate them to plans without reparsing) and `unknownType`
//     (the open-string discriminator the consumer skipped).
//   - `workspace_plan_path_collision` (spec: §14 line 338): `path`,
//     `winningSourceIndex`, `losingSourceIndex`. `sourceIndex` is
//     preserved equal to `winningSourceIndex` for backwards-compatible
//     pre-§14-line-338 single-index consumers.
//   - `workspace_plan_durable_schema_version_ahead` (spec: §15.5 lines
//     2466, 2471-2474): emitted by ParseStored when a persisted plan
//     carries a `schemaVersion` higher than this gateway understands.
//     The warning carries `knownVersion` (this gateway's schema) and
//     `encounteredVersion` (the persisted plan's schema) so durable
//     consumer alerts can route on the §15.5 catalog's
//     `durable_schema_version_ahead` body shape. F-15.5.5, F-15.5.8.
//
// The strip-components-skip warning is emitted by the adapter
// materializer (see pkg/adapter/workspace) and carries its own per-
// entry fields (`entryPath`, `segmentCount`, `stripComponents`) there.
// F-14.1.18.
type Warning struct {
	Code        WarningCode `json:"code"`
	SourceIndex int         `json:"sourceIndex"`
	Field       string      `json:"field,omitempty"`
	Message     string      `json:"message"`

	// Structured fields per §14 line 334
	// (`workspace_plan_unknown_source_type`). Optional; populated only
	// on unknown-source-type warnings.
	SchemaVersion *int   `json:"schemaVersion,omitempty"`
	UnknownType   string `json:"unknownType,omitempty"`

	// Structured fields per §14 line 338 (`workspace_plan_path_collision`).
	// Optional; populated only on collision warnings so the JSON output
	// is unchanged for the other warning codes.
	Path               string `json:"path,omitempty"`
	WinningSourceIndex *int   `json:"winningSourceIndex,omitempty"`
	LosingSourceIndex  *int   `json:"losingSourceIndex,omitempty"`

	// Structured fields per §15.5 line 2466
	// (`workspace_plan_durable_schema_version_ahead`). Optional; populated
	// only on the durable-forward-read warning so the JSON output is
	// unchanged for the other warning codes. KnownVersion is this
	// gateway's `SchemaVersion`; EncounteredVersion is the schemaVersion
	// the stored plan carried. F-15.5.5, F-15.5.8.
	KnownVersion       *int `json:"knownVersion,omitempty"`
	EncounteredVersion *int `json:"encounteredVersion,omitempty"`
}

// ValidationError is the §14 `400 WORKSPACE_PLAN_INVALID` envelope
// payload. The gateway maps this to the REST error response verbatim.
//
// The schemaVersion gate is the one exception to the 400 mapping: when
// Reason is ReasonUnsupportedSchemaVersion the gateway emits
// `422 WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` instead, carrying
// KnownVersion / EncounteredVersion as `details.knownVersion` /
// `details.encounteredVersion` so a client (and the §15.5 forward-read
// durable-consumer contract) can distinguish "you submitted a bad plan"
// from "this gateway is too old to read this plan." spec: §14.1 line
// 326. F-14.1.1.
type ValidationError struct {
	Reason  string   `json:"reason"`
	Field   string   `json:"field,omitempty"`
	Message string   `json:"message"`
	SubErrs []SubErr `json:"subErrors,omitempty"`

	// KnownVersion / EncounteredVersion accompany a
	// ReasonUnsupportedSchemaVersion error: the gateway's known schema
	// revision and the higher revision the plan declared. Both are nil
	// for every other reason. spec: §14.1 line 326. F-14.1.1.
	KnownVersion       *int `json:"knownVersion,omitempty"`
	EncounteredVersion *int `json:"encounteredVersion,omitempty"`
}

// SubErr captures a per-source validation error in the aggregate form
// the gateway returns when multiple sources fail validation in the
// same plan.
type SubErr struct {
	SourceIndex int    `json:"sourceIndex"`
	Field       string `json:"field"`
	Reason      string `json:"reason"`
	Message     string `json:"message"`
}

// Error implements error.
func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Field != "" {
		return fmt.Sprintf("workspaceplan: %s: %s (%s)", e.Field, e.Message, e.Reason)
	}
	return fmt.Sprintf("workspaceplan: %s (%s)", e.Message, e.Reason)
}

// Validation reasons (§14 normative).
const (
	ReasonInvalidModeFormat        = "invalid_mode_format"
	ReasonSetuidSetgidProhibited   = "setuid_setgid_prohibited"
	ReasonStickyOnFileProhibited   = "sticky_on_file_prohibited"
	ReasonGatewayWrittenField      = "gateway_written_field"
	ReasonPathTraversal            = "path_traversal"
	ReasonAbsolutePath             = "absolute_path"
	ReasonPathTooDeep              = "path_too_deep"
	ReasonPathTooLong              = "path_too_long"
	ReasonGitNonHTTPS              = "git_non_https"
	ReasonGitInvalidURL            = "git_invalid_url"
	ReasonMissingRequired          = "missing_required"
	ReasonUnknownField             = "unknown_field"
	ReasonInvalidEnumValue         = "invalid_enum_value"
	ReasonInvalidSchemaVersion     = "invalid_schema_version"
	ReasonZeroSchemaVersion        = "zero_schema_version"
	ReasonUnsupportedSchemaVersion = "unsupported_schema_version"
	ReasonNegativeDepth            = "negative_depth"
	ReasonInvalidLeaseScope        = "invalid_lease_scope"
	ReasonInvalidAuthMode          = "invalid_auth_mode"
)

// §14 limits.
const (
	// MaxPathDepth aligns with §13.4 archive depth (32 components).
	MaxPathDepth = 32
	// MaxPathLength aligns with §13.4 archive path (4096 UTF-8 bytes).
	MaxPathLength = 4096
)

var (
	modeRE       = regexp.MustCompile(`^0[0-7]{3,4}$`)
	leaseScopeRE = regexp.MustCompile(`^vcs\.[a-z0-9_-]+\.(read|write)$`)
	sha1RE       = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Parse decodes a §14 WorkspacePlan from raw JSON. The result carries
// a typed Sources slice; unknown source types are preserved in their
// raw form so the consumer can pass them through with a warning per
// §14 open-string discriminator semantics.
//
// Parse rejects JSON-level errors (`INVALID_REQUEST`-shaped) and the
// hard §14 invariants (schemaVersion strict, required-fields,
// gateway-written-fields, modes, paths, gitClone URL). It does NOT
// validate `uploadRef` against any blob store; callers run that check
// later, when the upload pipeline resolves refs.
func Parse(raw []byte) (Plan, []Warning, error) { return parse(raw, false) }

// ParseStored decodes a §14 WorkspacePlan the gateway previously
// stored. It is identical to Parse except that it accepts the
// gateway-written sources[<n>].resolvedCommitSha field that Parse
// rejects on a client request body. The start and resume handlers use
// ParseStored to read back a plan whose gitClone refs were pinned at
// session creation.
func ParseStored(raw []byte) (Plan, []Warning, error) { return parse(raw, true) }

// parse is the shared §14 decoder. When stored is true the
// gateway-written resolvedCommitSha field is accepted on gitClone
// sources, and the §15.5 durable-consumer forward-read rule applies:
// a schemaVersion higher than known returns a Plan plus a
// `workspace_plan_durable_schema_version_ahead` warning rather than a
// hard reject, with unknown fields preserved verbatim in
// `Source.Raw`. F-15.5.8.
func parse(raw []byte, stored bool) (Plan, []Warning, error) {
	// Step 1 — root structural decode.
	var root struct {
		SchemaVersion *int              `json:"schemaVersion"`
		Sources       []json.RawMessage `json:"sources"`
		SetupCommands []SetupCommand    `json:"setupCommands"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return Plan{}, nil, &ValidationError{
			Reason:  "invalid_request",
			Message: fmt.Sprintf("plan is not valid JSON: %v", err),
		}
	}

	if root.SchemaVersion == nil {
		return Plan{}, nil, &ValidationError{
			Reason:  ReasonMissingRequired,
			Field:   "schemaVersion",
			Message: "schemaVersion is required",
		}
	}
	// forward is true when this parse is operating in §15.5
	// durable-consumer forward-read mode: stored input whose
	// schemaVersion exceeds this gateway's known revision. The fork
	// preserves unknown variant fields verbatim (via Source.Raw) and
	// emits a `workspace_plan_durable_schema_version_ahead` warning.
	// F-15.5.5, F-15.5.8.
	var forward bool
	var schemaWarning *Warning
	switch *root.SchemaVersion {
	case 0:
		return Plan{}, nil, &ValidationError{
			Reason:  ReasonZeroSchemaVersion,
			Field:   "schemaVersion",
			Message: "schemaVersion must not be 0 (proto3 default); set it to 1",
		}
	case SchemaVersion:
		// ok
	default:
		if stored && *root.SchemaVersion > SchemaVersion {
			// spec: §15.5 lines 2471-2474 — durable consumers MUST
			// forward-read records with unrecognized `schemaVersion`.
			// Rejection at read time creates compliance gaps; the
			// gateway emits a `durable_schema_version_ahead`
			// annotation-shaped warning instead. F-15.5.8.
			forward = true
			known := SchemaVersion
			encountered := *root.SchemaVersion
			schemaWarning = &Warning{
				Code:    WarnDurableSchemaVersionAhead,
				Field:   "schemaVersion",
				Message: fmt.Sprintf("stored plan schemaVersion %d exceeds gateway-known %d; forward-reading per §15.5 durable-consumer rule", encountered, known),
				// Mirror the §15.5 line 2466 catalog body verbatim so a
				// durable consumer can route on the same field shape it
				// sees in MessageEnvelope.annotations.
				KnownVersion:       &known,
				EncounteredVersion: &encountered,
			}
		} else {
			// Future versions: return the §14.1 spec-aligned error.
			// schemaVersion > known → WORKSPACE_PLAN_SCHEMA_UNSUPPORTED.
			// schemaVersion < known (e.g., negative) → INVALID.
			reason := ReasonInvalidSchemaVersion
			ve := &ValidationError{
				Field:   "schemaVersion",
				Message: fmt.Sprintf("schemaVersion %d is not supported by this gateway (known: %d)", *root.SchemaVersion, SchemaVersion),
			}
			if *root.SchemaVersion > SchemaVersion {
				reason = ReasonUnsupportedSchemaVersion
				// spec: §14.1 line 326 — the gateway maps this reason to
				// `422 WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` and echoes the
				// version pair so a rollback can be diagnosed. F-14.1.1.
				known := SchemaVersion
				encountered := *root.SchemaVersion
				ve.KnownVersion = &known
				ve.EncounteredVersion = &encountered
			}
			ve.Reason = reason
			return Plan{}, nil, ve
		}
	}

	plan := Plan{
		SchemaVersion: *root.SchemaVersion,
		SetupCommands: root.SetupCommands,
	}

	var warnings []Warning
	if schemaWarning != nil {
		warnings = append(warnings, *schemaWarning)
	}
	var subErrs []SubErr

	// spec: §14 line 99 — per-command timeoutSeconds is an unsigned
	// duration; a negative value is meaningless and silently degrades to
	// the "no per-command bound" path (F-7.5.6 fix) when downstream code
	// gates on `> 0`. Reject it explicitly so a typo or sign bug surfaces
	// at the §14 ingress instead of through observable warmpool behaviour.
	// F-7.5.14.
	for i, sc := range root.SetupCommands {
		if sc.TimeoutSeconds < 0 {
			subErrs = append(subErrs, SubErr{
				SourceIndex: i,
				Field:       fmt.Sprintf("setupCommands[%d].timeoutSeconds", i),
				Reason:      ReasonNegativeDepth,
				Message:     "timeoutSeconds must be >= 0",
			})
		}
	}

	for i, rawSrc := range root.Sources {
		src, warn, err := parseSource(rawSrc, i, stored, forward)
		if err != nil {
			subErrs = append(subErrs, *err)
			continue
		}
		if warn != nil {
			// spec: §14 line 334 — the unknown-source-type warning
			// carries the plan's `schemaVersion` so a downstream
			// consumer can correlate the warning to the plan version
			// without re-reading the request body. F-14.1.18.
			if warn.Code == WarnUnknownSourceType && warn.SchemaVersion == nil {
				sv := *root.SchemaVersion
				warn.SchemaVersion = &sv
			}
			warnings = append(warnings, *warn)
		}
		plan.Sources = append(plan.Sources, src)
	}
	if len(subErrs) > 0 {
		return Plan{}, warnings, &ValidationError{
			Reason:  "validation_failed",
			Message: fmt.Sprintf("%d source(s) failed validation", len(subErrs)),
			SubErrs: subErrs,
		}
	}

	// Collision detection: emit a warning when two known sources write
	// to the same workspace-relative path. Last-writer-wins per §14
	// line 338, which mandates the structured fields `path`,
	// `winningSourceIndex` (the later, winning entry — `j` here), and
	// `losingSourceIndex` (the earlier, overwritten entry — `i`).
	// `SourceIndex` is preserved equal to `winningSourceIndex` for
	// backwards-compatibility with existing consumers that read the
	// pre-§14-line-338 single-index field.
	for i := 0; i < len(plan.Sources); i++ {
		for j := i + 1; j < len(plan.Sources); j++ {
			pi := plan.Sources[i].Variant.Path()
			pj := plan.Sources[j].Variant.Path()
			if pi == "" || pj == "" {
				continue
			}
			if pi == pj {
				winIdx := j
				loseIdx := i
				warnings = append(warnings, Warning{
					Code:               WarnPathCollision,
					SourceIndex:        winIdx,
					Field:              "path",
					Message:            fmt.Sprintf("path %q at sources[%d] is overwritten by sources[%d]; last-writer-wins per §14", pj, loseIdx, winIdx),
					Path:               pj,
					WinningSourceIndex: &winIdx,
					LosingSourceIndex:  &loseIdx,
				})
			}
		}
	}

	return plan, warnings, nil
}

// parseSource decodes a single source entry. Returns one of:
//   - (src, nil, nil) on a recognised variant validated cleanly
//   - (src, &Warning{Unknown}, nil) on a known structure with an
//     unknown discriminator value (open-string passthrough per §14)
//   - ({}, nil, *SubErr) on a hard validation error
//
// `forward` is the §15.5 durable-consumer forward-read flag: when true,
// unknown fields inside a known variant are preserved verbatim in
// `Source.Raw` instead of triggering a hard `unknown_field` reject.
// F-15.5.8.
func parseSource(raw json.RawMessage, i int, stored, forward bool) (Source, *Warning, *SubErr) {
	// Peek at type discriminator.
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return Source{}, nil, &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d]", i),
			Reason:      "invalid_request",
			Message:     fmt.Sprintf("source is not a JSON object: %v", err),
		}
	}
	if head.Type == "" {
		return Source{}, nil, &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d].type", i),
			Reason:      ReasonMissingRequired,
			Message:     "type is required",
		}
	}

	var rawMap map[string]any
	_ = json.Unmarshal(raw, &rawMap)

	switch head.Type {
	case TypeInlineFile:
		var v InlineFile
		if subErr := decodeVariant(raw, i, head.Type, &v, forward); subErr != nil {
			return Source{}, nil, subErr
		}
		if subErr := validateInlineFile(&v, i); subErr != nil {
			return Source{}, nil, subErr
		}
		return Source{Type: head.Type, Variant: v, Raw: rawMap}, nil, nil

	case TypeUploadFile:
		var v UploadFile
		if subErr := decodeVariant(raw, i, head.Type, &v, forward); subErr != nil {
			return Source{}, nil, subErr
		}
		if subErr := validateUploadFile(&v, i); subErr != nil {
			return Source{}, nil, subErr
		}
		return Source{Type: head.Type, Variant: v, Raw: rawMap}, nil, nil

	case TypeUploadArchive:
		var v UploadArchive
		if subErr := decodeVariant(raw, i, head.Type, &v, forward); subErr != nil {
			return Source{}, nil, subErr
		}
		if subErr := validateUploadArchive(&v, i); subErr != nil {
			return Source{}, nil, subErr
		}
		return Source{Type: head.Type, Variant: v, Raw: rawMap}, nil, nil

	case TypeMkdir:
		var v Mkdir
		if subErr := decodeVariant(raw, i, head.Type, &v, forward); subErr != nil {
			return Source{}, nil, subErr
		}
		if subErr := validateMkdir(&v, i); subErr != nil {
			return Source{}, nil, subErr
		}
		return Source{Type: head.Type, Variant: v, Raw: rawMap}, nil, nil

	case TypeGitClone:
		var v GitClone
		if subErr := decodeVariant(raw, i, head.Type, &v, forward); subErr != nil {
			return Source{}, nil, subErr
		}
		if subErr := validateGitClone(&v, i, stored); subErr != nil {
			return Source{}, nil, subErr
		}
		// spec: §14 line 91 — `path` default is `.` (the repo root) when
		// omitted. Apply the default explicitly here so the parsed Plan
		// and the round-tripped JSON both carry the spec-named default
		// rather than the empty-string ambiguity the parser would
		// otherwise leak downstream.
		if v.PathField == "" {
			v.PathField = "."
			if rawMap != nil {
				if _, present := rawMap["path"]; !present {
					rawMap["path"] = "."
				}
			}
		}
		return Source{Type: head.Type, Variant: v, Raw: rawMap}, nil, nil

	default:
		// §14 open-string discriminator: unknown types pass through
		// with a warning so the consumer can decide whether to skip
		// them. spec: §14 line 334 — fields are `schemaVersion` (stamped
		// by the caller in parse) and `unknownType`. F-14.1.18.
		return Source{Type: head.Type, Raw: rawMap},
			&Warning{
				Code:        WarnUnknownSourceType,
				SourceIndex: i,
				Field:       "type",
				UnknownType: head.Type,
				Message:     fmt.Sprintf("unknown source type %q; consumer will skip", head.Type),
			},
			nil
	}
}

// decodeVariant runs a strict decode (json.Decoder.DisallowUnknownFields)
// so per-variant `additionalProperties: false` enforces the §14
// "Per-variant field strictness" rule. The `type` discriminator is
// stripped before the strict decode so the per-variant struct does
// not need to carry a no-op Type field.
//
// `forward` is the §15.5 durable-consumer forward-read flag: when true
// the decoder allows unknown fields so a future schemaVersion's added
// fields don't break re-reads of a stored plan. The `Source.Raw` map
// preserves the original bytes verbatim, so the unknown fields are
// still available to any downstream pass-through consumer. F-15.5.8.
func decodeVariant(raw json.RawMessage, i int, typ string, out any, forward bool) *SubErr {
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d]", i),
			Reason:      "invalid_request",
			Message:     fmt.Sprintf("invalid %s entry: %v", typ, err),
		}
	}
	delete(asMap, "type")
	stripped, err := json.Marshal(asMap)
	if err != nil {
		return &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d]", i),
			Reason:      "invalid_request",
			Message:     fmt.Sprintf("could not re-marshal %s: %v", typ, err),
		}
	}
	dec := json.NewDecoder(strings.NewReader(string(stripped)))
	if !forward {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(out); err != nil {
		return &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d]", i),
			Reason:      ReasonUnknownField,
			Message:     fmt.Sprintf("invalid %s entry: %v", typ, err),
		}
	}
	return nil
}

// validateInlineFile applies §14 inlineFile rules.
func validateInlineFile(v *InlineFile, i int) *SubErr {
	if v.PathField == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].path", i),
			Reason: ReasonMissingRequired, Message: "path is required",
		}
	}
	if subErr := validatePath(v.PathField, i, "path"); subErr != nil {
		return subErr
	}
	if v.Mode == "" {
		v.Mode = "0644"
	}
	return validateMode(v.Mode, i, "mode", false /*allowSticky*/)
}

// validateUploadFile applies §14 uploadFile rules.
func validateUploadFile(v *UploadFile, i int) *SubErr {
	if v.PathField == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].path", i),
			Reason: ReasonMissingRequired, Message: "path is required",
		}
	}
	if v.UploadRef == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].uploadRef", i),
			Reason: ReasonMissingRequired, Message: "uploadRef is required",
		}
	}
	if subErr := validatePath(v.PathField, i, "path"); subErr != nil {
		return subErr
	}
	if v.Mode == "" {
		v.Mode = "0644"
	}
	return validateMode(v.Mode, i, "mode", false)
}

// validateUploadArchive applies §14 uploadArchive rules.
func validateUploadArchive(v *UploadArchive, i int) *SubErr {
	if v.PathPrefix == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].pathPrefix", i),
			Reason: ReasonMissingRequired, Message: "pathPrefix is required",
		}
	}
	if v.UploadRef == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].uploadRef", i),
			Reason: ReasonMissingRequired, Message: "uploadRef is required",
		}
	}
	switch v.Format {
	case "tar", "tar.gz", "zip":
		// ok
	default:
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].format", i),
			Reason:  ReasonInvalidEnumValue,
			Message: fmt.Sprintf("format %q must be one of tar, tar.gz, zip", v.Format),
		}
	}
	if v.StripComponents < 0 {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].stripComponents", i),
			Reason: ReasonNegativeDepth, Message: "stripComponents must be >= 0",
		}
	}
	return validatePath(v.PathPrefix, i, "pathPrefix")
}

// validateMkdir applies §14 mkdir rules. Sticky bit is permitted on
// directories per §14 mode-field notes.
func validateMkdir(v *Mkdir, i int) *SubErr {
	if v.PathField == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].path", i),
			Reason: ReasonMissingRequired, Message: "path is required",
		}
	}
	if subErr := validatePath(v.PathField, i, "path"); subErr != nil {
		return subErr
	}
	if v.Mode == "" {
		v.Mode = "0755"
	}
	return validateMode(v.Mode, i, "mode", true /*allowSticky*/)
}

// validateGitClone applies §14 gitClone rules. When stored is false the
// gateway-written resolvedCommitSha field is rejected as client input;
// ParseStored sets stored to true so a pinned plan read back from
// storage is accepted.
func validateGitClone(v *GitClone, i int, stored bool) *SubErr {
	if !stored && v.ResolvedCommitSha != "" {
		return &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d].resolvedCommitSha", i),
			Reason:      ReasonGatewayWrittenField,
			Message:     "resolvedCommitSha is gateway-written; clients must omit this field",
		}
	}
	if v.URL == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].url", i),
			Reason: ReasonMissingRequired, Message: "url is required",
		}
	}
	u, err := url.Parse(v.URL)
	if err != nil || u.Host == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].url", i),
			Reason:  ReasonGitInvalidURL,
			Message: fmt.Sprintf("url %q is not a valid URI", v.URL),
		}
	}
	if strings.ToLower(u.Scheme) != "https" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].url", i),
			Reason:  ReasonGitNonHTTPS,
			Message: "gitClone v1 accepts HTTPS URLs only; SSH and git:// are deferred post-V1",
		}
	}
	if u.User != nil {
		// §14 / §13.5: in-URL credentials bypass the credential-lease
		// path that gates HTTPS clone auth. Reject so the only path
		// to authenticated clones is via gitClone.auth.leaseScope.
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].url", i),
			Reason:  ReasonGitInvalidURL,
			Message: "gitClone url must not embed userinfo; use gitClone.auth.leaseScope for authenticated clones",
		}
	}
	if v.Ref == "" {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].ref", i),
			Reason: ReasonMissingRequired, Message: "ref is required",
		}
	}
	if v.Depth < 0 {
		return &SubErr{
			SourceIndex: i, Field: fmt.Sprintf("sources[%d].depth", i),
			Reason: ReasonNegativeDepth, Message: "depth must be >= 1 when set",
		}
	}
	if v.Auth != nil {
		if v.Auth.Mode != "credential-lease" {
			return &SubErr{
				SourceIndex: i,
				Field:       fmt.Sprintf("sources[%d].auth.mode", i),
				Reason:      ReasonInvalidAuthMode,
				Message:     fmt.Sprintf("auth.mode %q must be credential-lease", v.Auth.Mode),
			}
		}
		if !leaseScopeRE.MatchString(v.Auth.LeaseScope) {
			return &SubErr{
				SourceIndex: i,
				Field:       fmt.Sprintf("sources[%d].auth.leaseScope", i),
				Reason:      ReasonInvalidLeaseScope,
				Message:     "auth.leaseScope must match ^vcs\\.[a-z0-9_-]+\\.(read|write)$",
			}
		}
	}
	if v.PathField != "" {
		if subErr := validatePath(v.PathField, i, "path"); subErr != nil {
			return subErr
		}
	}
	// If ref is already a SHA-1 commit, no further work is required;
	// otherwise the gateway will ls-remote at session creation time.
	_ = sha1RE
	return nil
}

// validateMode applies the §14 mode-field rules: 3-4 octal digit
// pattern with a leading zero, no setuid or setgid bits, optional
// sticky bit only on directories.
func validateMode(mode string, i int, field string, allowSticky bool) *SubErr {
	if !modeRE.MatchString(mode) {
		return &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d].%s", i, field),
			Reason:      ReasonInvalidModeFormat,
			Message:     fmt.Sprintf("mode %q must match ^0[0-7]{3,4}$", mode),
		}
	}
	// Only the four-digit form can encode setuid / setgid / sticky.
	if len(mode) == 5 { // leading 0 + 4 octal digits
		high := mode[1] // first significant octal digit
		switch high {
		case '4', '5', '6', '7':
			return &SubErr{
				SourceIndex: i,
				Field:       fmt.Sprintf("sources[%d].%s", i, field),
				Reason:      ReasonSetuidSetgidProhibited,
				Message:     fmt.Sprintf("mode %q sets setuid (04xxx) which is prohibited", mode),
			}
		case '2', '3':
			return &SubErr{
				SourceIndex: i,
				Field:       fmt.Sprintf("sources[%d].%s", i, field),
				Reason:      ReasonSetuidSetgidProhibited,
				Message:     fmt.Sprintf("mode %q sets setgid (02xxx) which is prohibited", mode),
			}
		case '1':
			if !allowSticky {
				return &SubErr{
					SourceIndex: i,
					Field:       fmt.Sprintf("sources[%d].%s", i, field),
					Reason:      ReasonStickyOnFileProhibited,
					Message:     fmt.Sprintf("mode %q sets the sticky bit (01xxx); sticky on a regular file has no defined semantics", mode),
				}
			}
		}
	}
	return nil
}

// validatePath enforces §13.4-derived path invariants for any
// workspace-relative path supplied in a plan: no absolute paths, no
// `..` segments, no backslash / NUL / control bytes, bounded depth
// and length.
func validatePath(p string, i int, field string) *SubErr {
	if len(p) > MaxPathLength {
		return &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d].%s", i, field),
			Reason:      ReasonPathTooLong,
			Message:     fmt.Sprintf("path length %d exceeds %d-byte limit", len(p), MaxPathLength),
		}
	}
	if strings.HasPrefix(p, "/") {
		return &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d].%s", i, field),
			Reason:      ReasonAbsolutePath,
			Message:     "absolute paths are prohibited; supply a workspace-relative path",
		}
	}
	// Reject control / NUL / backslash bytes. NUL terminates strings
	// in C path APIs and would truncate the materialised path;
	// backslash is the Windows separator and a runtime that splits on
	// `\` could escape the workspace via `foo\..\..\etc`.
	for j := 0; j < len(p); j++ {
		c := p[j]
		if c == 0 || c < 0x20 || c == 0x7f {
			return &SubErr{
				SourceIndex: i,
				Field:       fmt.Sprintf("sources[%d].%s", i, field),
				Reason:      ReasonPathTraversal,
				Message:     fmt.Sprintf("path contains a forbidden control byte (0x%02x)", c),
			}
		}
		if c == '\\' {
			return &SubErr{
				SourceIndex: i,
				Field:       fmt.Sprintf("sources[%d].%s", i, field),
				Reason:      ReasonPathTraversal,
				Message:     "path contains a backslash; use forward slashes only",
			}
		}
	}
	segs := strings.Split(p, "/")
	depth := 0
	for _, s := range segs {
		if s == "" {
			continue
		}
		if s == ".." {
			return &SubErr{
				SourceIndex: i,
				Field:       fmt.Sprintf("sources[%d].%s", i, field),
				Reason:      ReasonPathTraversal,
				Message:     `path contains ".." segment; traversal prohibited`,
			}
		}
		depth++
	}
	if depth > MaxPathDepth {
		return &SubErr{
			SourceIndex: i,
			Field:       fmt.Sprintf("sources[%d].%s", i, field),
			Reason:      ReasonPathTooDeep,
			Message:     fmt.Sprintf("path depth %d exceeds %d-segment limit", depth, MaxPathDepth),
		}
	}
	return nil
}
