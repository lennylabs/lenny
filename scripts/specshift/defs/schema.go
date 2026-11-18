// SPDX-License-Identifier: MIT

package defs

// FieldType is the value type a schema field accepts.
type FieldType string

// The field types. Each one names what the YAML value of the field must be.
const (
	// TypeString is a non-empty scalar read as text.
	TypeString FieldType = "string"
	// TypeScalar is any non-null scalar: a string, a number, or a boolean.
	TypeScalar FieldType = "scalar"
	// TypeInteger is an integer scalar.
	TypeInteger FieldType = "integer"
	// TypeBoolean is true or false.
	TypeBoolean FieldType = "boolean"
	// TypeEnum is a scalar from the field's value list.
	TypeEnum FieldType = "enum"
	// TypeList is a sequence of non-null scalars, which may be empty.
	TypeList FieldType = "list"
	// TypeScalarOrList is a non-null scalar or a sequence of them.
	TypeScalarOrList FieldType = "scalar or list"
	// TypeEnumList is a non-empty sequence of scalars from the field's
	// value list.
	TypeEnumList FieldType = "enum list"
	// TypeCode is one page code that a page of the tree registers.
	TypeCode FieldType = "page code"
	// TypeCodes is one page code or a non-empty sequence of them, each
	// registered by a page of the tree.
	TypeCodes FieldType = "page codes"
	// TypeMapping is a mapping from scalar keys to scalar values.
	TypeMapping FieldType = "mapping"
	// TypeTransitions is a sequence of transition mappings, each with from
	// and to, and optionally on and rule.
	TypeTransitions FieldType = "transitions"
)

// Field is one field of a kind's schema.
type Field struct {
	// Name is the YAML key.
	Name string
	// Type is the value type the key accepts.
	Type FieldType
	// Required reports whether a block of the kind must carry the key.
	Required bool
	// Values lists the accepted values of an enum or enum-list field.
	Values []string
	// Doc says what the field holds, for the schema reference.
	Doc string
}

// Kind is the schema of one definition-block kind.
type Kind struct {
	// Name is the value of the block's kind field.
	Name string
	// Title is the kind's name in prose, singular.
	Title string
	// NameField is the field whose value names the block and gives the
	// slug of its anchor: name for most kinds, key for labels and
	// annotations, and type for events.
	NameField string
	// Catalog is the base name of the catalog that lists the kind.
	Catalog string
	// Owner says which page owns a block of the kind, for the schema
	// reference.
	Owner string
	// Contribution reports whether a block of the kind is stated on a part
	// page and listed on the page of the running unit its unit field names.
	Contribution bool
	// Fields are the kind's fields other than kind itself.
	Fields []Field
}

// Field returns the schema field of the given key.
func (k *Kind) Field(name string) (Field, bool) {
	for _, f := range k.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// The kind names.
const (
	KindSetting          = "setting"
	KindLimit            = "limit"
	KindError            = "error"
	KindAdmissionRefusal = "admission-refusal"
	KindConditionReason  = "condition-reason"
	KindMetric           = "metric"
	KindAlert            = "alert"
	KindEvent            = "event"
	KindLabel            = "label"
	KindAnnotation       = "annotation"
	KindFinalizer        = "finalizer"
	KindReadinessGate    = "readiness-gate"
	KindCustomResource   = "custom-resource"
	KindOperation        = "operation"
	KindState            = "state"
	KindRegister         = "register"
	KindGrant            = "grant"
	KindRBACGrant        = "rbac-grant"
	KindNetworkRule      = "network-rule"
)

// The fields several kinds share.
var (
	fieldSummary = Field{Name: "summary", Type: TypeString, Required: true, Doc: "One sentence that says what the defined thing is, used in the catalog row."}
	fieldName    = func(doc string) Field { return Field{Name: "name", Type: TypeString, Required: true, Doc: doc} }
	fieldWriters = func(required bool) Field {
		return Field{Name: "writers", Type: TypeList, Required: required, Doc: "The components or parts that write it, as codes or short phrases."}
	}
	fieldReaders = func(required bool) Field {
		return Field{Name: "readers", Type: TypeList, Required: required, Doc: "The components or parts that read it, as codes or short phrases."}
	}
	fieldRequirement = Field{Name: "requirement", Type: TypeString, Required: true, Doc: "The identifier of the requirement behind the grant or rule, such as `GWCLAIM-R4`."}
)

// kubernetesVerbs are the verbs an RBAC rule accepts.
var kubernetesVerbs = []string{"*", "bind", "create", "delete", "deletecollection", "escalate", "get", "impersonate", "list", "patch", "update", "use", "watch"}

// rbacFields are the fields of the two RBAC grant kinds.
func rbacFields(contribution bool) []Field {
	fields := []Field{fieldName("A short name for the grant, unique on the page, such as `podclaims write`.")}
	if contribution {
		fields = append(fields, Field{Name: "unit", Type: TypeCode, Required: true, Doc: "The code of the running unit whose ServiceAccount holds the grant, such as `GW`."})
	}
	return append(
		fields,
		Field{Name: "service-account", Type: TypeString, Doc: "The ServiceAccount, when the unit runs under more than one."},
		Field{Name: "api-groups", Type: TypeList, Required: true, Doc: "The API groups of the rule; the core group is `\"\"`."},
		Field{Name: "resources", Type: TypeList, Required: true, Doc: "The resources and subresources of the rule, such as `podclaims/status`."},
		Field{Name: "verbs", Type: TypeEnumList, Required: true, Values: kubernetesVerbs, Doc: "The verbs of the rule."},
		Field{Name: "namespace-scope", Type: TypeString, Required: true, Doc: "Where the grant applies: `cluster`, or the namespace it is bound in, such as `agent namespace`."},
		fieldRequirement,
		Field{Name: "summary", Type: TypeString, Doc: "What the grant is for, when the requirement does not say it."},
	)
}

// kinds is the schema table. Its order is the order of the schema
// reference; lookups go through KindByName.
var kinds = []*Kind{
	{
		Name: KindSetting, Title: "setting", NameField: "name", Catalog: "settings",
		Owner: "The capability whose object the setting configures for a behavioral setting; the component for a process or deployment setting.",
		Fields: []Field{
			fieldName("The setting's name as an operator writes it, such as `gateway.claimHoldTTLSeconds` or a flag such as `--create-qps`."),
			{Name: "type", Type: TypeString, Required: true, Doc: "The value type, such as `integer`, `duration`, `boolean`, `string`, `enum`, or `quantity`."},
			{Name: "default", Type: TypeScalar, Required: true, Doc: "The default value, or a phrase such as `none` when the setting has no default."},
			{Name: "range", Type: TypeScalarOrList, Doc: "The accepted range, as `[min, max]` or a phrase. A block carries `range` or `values`, never both."},
			{Name: "values", Type: TypeList, Doc: "The accepted values of an enumerated setting."},
			{Name: "unit", Type: TypeString, Doc: "The unit of a numeric value, such as `seconds`."},
			{Name: "scopes", Type: TypeList, Required: true, Doc: "Where the setting is set, such as `platform`, `tenant`, or `pool`."},
			{Name: "defaults-by-tier", Type: TypeMapping, Doc: "Per-tier defaults, keyed by capacity tier. The tier presets of the settings catalog are generated from it."},
			{Name: "read-by", Type: TypeCodes, Required: true, Doc: "The codes of the components whose processes read the setting. Each component page lists it in its generated `settings-read` section."},
			fieldSummary,
		},
	},
	{
		Name: KindLimit, Title: "limit", NameField: "name", Catalog: "limits",
		Owner: "Wherever the setting that carries the limit is owned; for a fixed limit, the capability that enforces it.",
		Fields: []Field{
			fieldName("The limit's name, such as `maxUploadBytes`."),
			{Name: "value", Type: TypeScalar, Required: true, Doc: "The limit's value."},
			{Name: "unit", Type: TypeString, Doc: "The unit of the value, such as `bytes`."},
			{Name: "scopes", Type: TypeList, Doc: "What the limit applies to, such as `session` or `tenant`."},
			{Name: "defaults-by-tier", Type: TypeMapping, Doc: "Per-tier values, keyed by capacity tier."},
			{Name: "setting", Type: TypeString, Doc: "The setting that overrides the limit, when one does."},
			{Name: "enforced-by", Type: TypeCodes, Doc: "The codes of the components that enforce the limit."},
			fieldSummary,
		},
	},
	{
		Name: KindError, Title: "error code", NameField: "name", Catalog: "errors",
		Owner: "The capability whose rule produces the code; the cross-cutting error model for a code with several causes.",
		Fields: []Field{
			fieldName("The error code as the wire carries it, such as `WARM_POOL_EXHAUSTED`."),
			{Name: "status", Type: TypeInteger, Required: true, Doc: "The HTTP status the code is returned with."},
			{Name: "category", Type: TypeEnum, Values: []string{"PERMANENT", "POLICY", "TRANSIENT", "UPSTREAM"}, Doc: "The error category of the shared error taxonomy."},
			{Name: "retryable", Type: TypeBoolean, Doc: "Whether a client may retry the request unchanged."},
			{Name: "message", Type: TypeString, Doc: "The exact message, when the code carries a fixed one."},
			{Name: "details", Type: TypeMapping, Doc: "The fields of the error's details object and their values."},
			{Name: "returned-by", Type: TypeList, Doc: "The identifiers of the operations that return the code."},
			{Name: "rule", Type: TypeScalarOrList, Doc: "The identifiers of the requirements whose rule produces the code."},
			fieldSummary,
		},
	},
	{
		Name: KindAdmissionRefusal, Title: "admission refusal", NameField: "name", Catalog: "errors",
		Owner: "The capability whose rule the admission webhook enforces. A refusal carries its exact message and no error code.",
		Fields: []Field{
			fieldName("A name for the refusal, such as `podclaim concurrent claim`."),
			{Name: "message", Type: TypeString, Required: true, Doc: "The exact message the webhook returns."},
			{Name: "status", Type: TypeInteger, Doc: "The HTTP status a Kubernetes client sees, `403` when absent."},
			{Name: "resource", Type: TypeString, Doc: "The resource whose writes the webhook reviews."},
			{Name: "operations", Type: TypeList, Doc: "The admission operations refused, such as `CREATE`."},
			{Name: "webhook", Type: TypeString, Doc: "The webhook that refuses the write."},
			{Name: "rule", Type: TypeScalarOrList, Doc: "The identifiers of the requirements the refusal enforces."},
			fieldSummary,
		},
	},
	{
		Name: KindConditionReason, Title: "condition reason", NameField: "name", Catalog: "errors",
		Owner: "The capability whose rule sets the condition.",
		Fields: []Field{
			fieldName("The reason string, such as `PodSetKeyCollision`."),
			{Name: "resource", Type: TypeString, Required: true, Doc: "The custom resource whose status carries the condition."},
			{Name: "condition", Type: TypeString, Required: true, Doc: "The condition type the reason is set on."},
			{Name: "rule", Type: TypeScalarOrList, Doc: "The identifiers of the requirements that set it."},
			fieldSummary,
		},
	},
	{
		Name: KindMetric, Title: "metric", NameField: "name", Catalog: "metrics",
		Owner: "The capability whose behavior the metric measures; the component for a unit's health as a process; the observability conventions for a metric every controller emits.",
		Fields: []Field{
			fieldName("The metric name, such as `lenny_pod_claim_duration_seconds`."),
			{Name: "type", Type: TypeEnum, Required: true, Values: []string{"counter", "gauge", "histogram", "summary"}, Doc: "The metric type."},
			{Name: "unit", Type: TypeString, Doc: "The unit, when the name does not carry it."},
			{Name: "labels", Type: TypeList, Required: true, Doc: "The label names, `[]` for none."},
			{Name: "emitter", Type: TypeCodes, Required: true, Doc: "The codes of the components that emit the metric. Each component page lists it in its generated `metrics-emitted` section."},
			fieldSummary,
		},
	},
	{
		Name: KindAlert, Title: "alert", NameField: "name", Catalog: "alerts",
		Owner: "The owner of the requirement whose breach the alert detects; the component for an alert on a unit's health.",
		Fields: []Field{
			fieldName("The alert name, such as `WarmPoolLow`."),
			{Name: "expr", Type: TypeString, Required: true, Doc: "The alert's expression, in PromQL or in words."},
			{Name: "severity", Type: TypeEnum, Required: true, Values: []string{"critical", "info", "warning"}, Doc: "The severity label."},
			{Name: "for", Type: TypeString, Doc: "How long the expression holds before the alert fires, such as `5m`."},
			{Name: "detects", Type: TypeScalarOrList, Doc: "The identifiers of the requirements whose breach the alert detects."},
			{Name: "runbook", Type: TypeString, Doc: "The runbook that handles the alert."},
			fieldSummary,
		},
	},
	{
		Name: KindEvent, Title: "event type", NameField: "type", Catalog: "events",
		Owner: "The interface that carries the event.",
		Fields: []Field{
			{Name: "type", Type: TypeString, Required: true, Doc: "The event type, such as `dev.lenny.session.created`."},
			{Name: "emitter", Type: TypeCodes, Required: true, Doc: "The codes of the components that emit the event. Each component page lists it in its generated `events-emitted` section."},
			{Name: "payload", Type: TypeString, Required: true, Doc: "What the event's data carries, in one sentence or as a list of fields."},
			fieldSummary,
		},
	},
	labelKind(KindLabel, "label", true),
	labelKind(KindAnnotation, "annotation", false),
	{
		Name: KindFinalizer, Title: "finalizer", NameField: "key", Catalog: "labels",
		Owner: "The Kubernetes labels and annotations page.",
		Fields: []Field{
			{Name: "key", Type: TypeString, Required: true, Doc: "The finalizer, such as `lenny.dev/session-cleanup`."},
			{Name: "carriers", Type: TypeList, Required: true, Doc: "The objects that carry it."},
			fieldWriters(true),
			fieldReaders(false),
			{Name: "meaning", Type: TypeScalarOrList, Doc: "The identifiers of the requirements that give it meaning."},
			fieldSummary,
		},
	},
	{
		Name: KindReadinessGate, Title: "readiness gate", NameField: "key", Catalog: "labels",
		Owner: "The Kubernetes labels and annotations page.",
		Fields: []Field{
			{Name: "key", Type: TypeString, Required: true, Doc: "The condition type of the gate, such as `lenny.dev/agent-pod-ready`."},
			{Name: "carriers", Type: TypeList, Required: true, Doc: "The objects that carry it."},
			fieldWriters(true),
			fieldReaders(false),
			{Name: "meaning", Type: TypeScalarOrList, Doc: "The identifiers of the requirements that give it meaning."},
			fieldSummary,
		},
	},
	{
		Name: KindCustomResource, Title: "custom resource", NameField: "name", Catalog: "custom-resources",
		Owner: "The custom resource's Kubernetes interface page.",
		Fields: []Field{
			fieldName("The resource kind, such as `PodClaim`."),
			{Name: "group", Type: TypeString, Required: true, Doc: "The API group, such as `lenny.dev`."},
			{Name: "version", Type: TypeString, Required: true, Doc: "The served version, such as `v1alpha1`."},
			{Name: "scope", Type: TypeString, Required: true, Doc: "`Namespaced` or `Cluster`, optionally followed by the namespace in parentheses."},
			{Name: "plural", Type: TypeString, Doc: "The plural resource name."},
			{Name: "crd", Type: TypeString, Doc: "The CustomResourceDefinition's name."},
			{Name: "short-name", Type: TypeString, Doc: "The short name, when the resource has one."},
			fieldWriters(false),
			fieldReaders(false),
			fieldSummary,
		},
	},
	{
		Name: KindOperation, Title: "operation", NameField: "name", Catalog: "api-operations",
		Owner: "The interface page that defines the operation. The block's anchor is the operation identifier `<CODE>-<name>` lower-cased, such as `admpools-createpool`.",
		Fields: []Field{
			fieldName("The operation's own name in its interface: the OpenAPI `operationId`, the gRPC method, the Kubernetes verb, the CLI command words, or, on an in-process interface page such as the gateway seams, the Go method, such as `createPool` or `LookupSession`."),
			{Name: "method", Type: TypeString, Doc: "The HTTP method, the RPC kind, the Kubernetes verb, or `Go method` for an operation of an in-process interface page."},
			{Name: "path", Type: TypeString, Doc: "The HTTP path, the resource the operation acts on, or, for an operation of an in-process interface page, the Go interface that declares it, such as `seam.Sessions`."},
			{Name: "returns", Type: TypeList, Doc: "The error codes and refusals the operation returns, by name."},
			{Name: "requirements", Type: TypeList, Doc: "The identifiers of the requirements behind the operation's outcomes."},
			fieldSummary,
		},
	},
	{
		Name: KindState, Title: "state machine", NameField: "name", Catalog: "state-machines",
		Owner: "The capability that governs the object's lifecycle.",
		Fields: []Field{
			fieldName("The state machine's name, such as `claim binding state`."),
			{Name: "object", Type: TypeString, Required: true, Doc: "The domain object whose states these are."},
			{Name: "states", Type: TypeList, Required: true, Doc: "Every state, in lifecycle order."},
			{Name: "initial", Type: TypeString, Doc: "The state an object starts in; one of `states`."},
			{Name: "terminal", Type: TypeList, Doc: "The states an object never leaves; each one of `states`."},
			{Name: "transitions", Type: TypeTransitions, Required: true, Doc: "Each transition as a mapping: `from` (a state or a list of states), `to` (a state), and optionally `on` (the trigger) and `rule` (a requirement identifier)."},
			fieldSummary,
		},
	},
	{
		Name: KindRegister, Title: "register", NameField: "name", Catalog: "channels",
		Owner: "The narrowest page that covers every writer: a part page, a component index, the gateway seams page, or a channel interface page.",
		Fields: []Field{
			fieldName("The register's name, such as `slot set`."),
			{Name: "identifier", Type: TypeString, Doc: "The register's channel identifier, such as `REG-SLOTSET`, when it mediates two participants."},
			{Name: "store", Type: TypeString, Required: true, Doc: "The store that holds it, such as `Redis` or `Postgres`."},
			{Name: "keys", Type: TypeList, Required: true, Doc: "The keys, tables, or objects it occupies."},
			{Name: "members", Type: TypeList, Doc: "The member or value forms it holds."},
			fieldWriters(true),
			fieldReaders(true),
			fieldSummary,
		},
	},
	{
		Name: KindGrant, Title: "RBAC grant", NameField: "name", Catalog: "contributions",
		Owner:  "The page of the running unit whose ServiceAccount holds the grant, for a grant the unit needs as a whole.",
		Fields: rbacFields(false),
	},
	{
		Name: KindRBACGrant, Title: "contributed RBAC grant", NameField: "name", Catalog: "contributions", Contribution: true,
		Owner:  "The part page whose requirement needs the grant. The unit's page lists it in its generated `rbac-grants` section.",
		Fields: rbacFields(true),
	},
	{
		Name: KindNetworkRule, Title: "contributed network rule", NameField: "name", Catalog: "contributions", Contribution: true,
		Owner: "The part page whose requirement needs the traffic. The unit's page lists it in its generated `network-rules` section.",
		Fields: []Field{
			fieldName("A short name for the rule, unique on the page, such as `gateway to adapter`."),
			{Name: "unit", Type: TypeCode, Required: true, Doc: "The code of the running unit whose NetworkPolicy carries the rule."},
			{Name: "direction", Type: TypeEnum, Required: true, Values: []string{"egress", "ingress"}, Doc: "The direction of the traffic, seen from the unit."},
			{Name: "peer", Type: TypeString, Required: true, Doc: "The other end, as a component code, a selector, or a CIDR."},
			{Name: "ports", Type: TypeList, Required: true, Doc: "The ports, such as `8443/TCP`."},
			fieldRequirement,
			{Name: "summary", Type: TypeString, Doc: "What the traffic is for, when the requirement does not say it."},
		},
	},
}

// labelKind returns the schema of the label and annotation kinds, which
// differ only in whether mutability is required.
func labelKind(name, title string, mutabilityRequired bool) *Kind {
	return &Kind{
		Name: name, Title: title, NameField: "key", Catalog: "labels",
		Owner: "The Kubernetes labels and annotations page.",
		Fields: []Field{
			{Name: "key", Type: TypeString, Required: true, Doc: "The key, such as `lenny.dev/pool`."},
			{Name: "carriers", Type: TypeList, Required: true, Doc: "The objects that carry it."},
			{Name: "values", Type: TypeScalarOrList, Required: true, Doc: "The values it takes, as a list or a phrase."},
			{Name: "mutability", Type: TypeString, Required: mutabilityRequired, Doc: "When it may change, such as `required; immutable` or `set once, from unset`."},
			{Name: "transitions", Type: TypeString, Doc: "The permitted value changes and who may make each, when mutability is a transition rule."},
			{Name: "refusal", Type: TypeString, Doc: "The error code or admission refusal a forbidden change receives."},
			fieldWriters(true),
			fieldReaders(true),
			{Name: "meaning", Type: TypeScalarOrList, Doc: "The identifiers of the requirements that give it meaning."},
			fieldSummary,
		},
	}
}

// KindByName returns the schema of a kind.
func KindByName(name string) (*Kind, bool) {
	for _, k := range kinds {
		if k.Name == name {
			return k, true
		}
	}
	return nil, false
}

// Kinds returns the schema table in reference order.
func Kinds() []*Kind {
	return append([]*Kind(nil), kinds...)
}
