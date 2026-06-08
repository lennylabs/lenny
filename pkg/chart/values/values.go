// SPDX-License-Identifier: MIT

// Package values is the source of truth for the Lenny Helm chart's
// values.schema.json (Draft 2020-12). The Root struct mirrors the
// documented top-level keys of charts/lenny/values.yaml; the schema is
// reflected from it by Generate and committed at
// charts/lenny/values.schema.json so Helm validates -f / --set inputs on
// every install and upgrade. The build fails when the committed file
// drifts from the regenerated output (see cmd/lenny-chart-schema-gen and
// pkg/chart/values TestSchemaIsCommitted).
//
// Sections with spec-named value constraints (Global, Playground) are
// fully enumerated so the schema rejects unknown keys, wrong types, and
// out-of-range values within them. The remaining sections are typed as
// Object: the root rejects unknown top-level keys (the common operator
// typo, e.g. `gatway:`), while each section's documented sub-keys remain
// permissive pending field-level modeling.
//
// Schema constraints are declared with the struct tags `enum` (pipe
// separated), `pattern`, `min`, `max`, `maxLength`, `minLength`, and
// `desc`. Separate tag keys are used (rather than one comma-joined tag)
// because a comma inside a regex quantifier such as {1,128} collides with
// reflect.StructTag's strconv.Unquote handling.
//
// spec: §17.6 lines 651-666 (Helm values.schema.json + lenny-ctl values
// validate), §17.9.2 line 1374 (answer-file CI lint).
package values

// Object is a documented chart-values section whose internal structure is
// not yet modeled field-by-field. It generates {"type":"object"} with no
// additionalProperties constraint, so the schema validates the top-level
// structure (the key must be an object) without rejecting the section's
// documented sub-keys.
type Object map[string]any

// Root models every documented top-level key of charts/lenny/values.yaml.
// Field types match the YAML node kind (object, array, or scalar) so the
// generated schema accepts the chart's default values document verbatim.
type Root struct {
	AgentNamespaces            []Object `json:"agentNamespaces,omitempty"`
	Controller                 Object   `json:"controller,omitempty"`
	PoolScalingController      Object   `json:"poolScalingController,omitempty"`
	Admin                      Object   `json:"admin,omitempty"`
	AdmissionWebhooks          Object   `json:"admissionWebhooks,omitempty"`
	AdmissionPolicies          Object   `json:"admissionPolicies,omitempty"`
	WebhookIngressCIDR         string   `json:"webhookIngressCIDR,omitempty"`
	KubeAPIServerCIDR          string   `json:"kubeApiServerCIDR,omitempty"`
	KubeAPIServerPorts         []int    `json:"kubeApiServerPorts,omitempty"`
	EgressCIDRs                Object   `json:"egressCIDRs,omitempty"`
	CoreDNS                    Object   `json:"coredns,omitempty"`
	IngressControllerNamespace string   `json:"ingressControllerNamespace,omitempty"`
	Ingress                    Object   `json:"ingress,omitempty"`
	TokenService               Object   `json:"tokenService,omitempty"`
	Redis                      Object   `json:"redis,omitempty"`
	Backends                   string   `json:"backends,omitempty"`
	// Cluster is the §17.9.1 cluster-type composition dimension
	// (laptop | eks | gke | aks | openshift | vanilla). The curated
	// §17.9.2 answer files pin it; the chart validates the value with the
	// lenny.clusterType helper. Left a plain string (no enum) so the
	// chart default may leave it unset (the docker-compose answer file's
	// cluster=n/a case). spec: §17.9.1 line 1351.
	Cluster string `json:"cluster,omitempty" desc:"Cluster-type composition dimension (laptop, eks, gke, aks, openshift, or vanilla). §17.9.1 line 1351."`
	// IsolationProfile is the §17.9.1 isolation-profile composition
	// dimension (baseline | sandboxed | hypervisor). It sets the default
	// isolationProfile stamped on the §26 seeded reference runtimes; the
	// lenny.seededIsolationProfile helper maps it to the §5.3 profile
	// (baseline→standard, sandboxed→sandboxed, hypervisor→microvm).
	// spec: §17.9.1 line 1354.
	IsolationProfile           string     `json:"isolationProfile,omitempty" desc:"Isolation-profile composition dimension (baseline, sandboxed, or hypervisor): sets the default RuntimeClass for seeded runtimes. §17.9.1 line 1354."`
	ComplianceProfile          string     `json:"complianceProfile,omitempty"`
	ObjectStorage              Object     `json:"objectStorage,omitempty"`
	MinIO                      Object     `json:"minio,omitempty"`
	GC                         Object     `json:"gc,omitempty"`
	KMS                        Object     `json:"kms,omitempty"`
	Environment                string     `json:"environment,omitempty" desc:"Target environment (local, dev, or prod): drives alert thresholds, warm-pool sizes, and log verbosity (§17.6 question phase, §17.9.1)."`
	Monitoring                 Object     `json:"monitoring,omitempty"`
	SLO                        Object     `json:"slo,omitempty"`
	Observability              Object     `json:"observability,omitempty"`
	Tracing                    Object     `json:"tracing,omitempty"`
	PgBouncer                  Object     `json:"pgbouncer,omitempty"`
	Postgres                   Object     `json:"postgres,omitempty"`
	StoreRouter                Object     `json:"storeRouter,omitempty"`
	EventBus                   Object     `json:"eventBus,omitempty"`
	Billing                    Object     `json:"billing,omitempty"`
	Audit                      Object     `json:"audit,omitempty"`
	Features                   Object     `json:"features,omitempty"`
	ImageVerification          Object     `json:"imageVerification,omitempty"`
	Platform                   Object     `json:"platform,omitempty"`
	Tenancy                    Object     `json:"tenancy,omitempty"`
	Auth                       Object     `json:"auth,omitempty"`
	Global                     Global     `json:"global,omitempty"`
	MTLS                       Object     `json:"mtls,omitempty"`
	CertManager                Object     `json:"certmanager,omitempty"`
	Adapter                    Object     `json:"adapter,omitempty"`
	Sandbox                    Object     `json:"sandbox,omitempty"`
	RuntimeClasses             Object     `json:"runtimeClasses,omitempty"`
	Isolation                  Object     `json:"isolation,omitempty"`
	Gateway                    Object     `json:"gateway,omitempty"`
	Delegation                 Object     `json:"delegation,omitempty"`
	Autoscaling                Object     `json:"autoscaling,omitempty"`
	Playground                 Playground `json:"playground,omitempty"`
	Memory                     Object     `json:"memory,omitempty"`
	ReferenceRuntimes          Object     `json:"referenceRuntimes,omitempty"`
	Ops                        Object     `json:"ops,omitempty"`
	Security                   Object     `json:"security,omitempty"`
	AcceptFeatureFlagDowngrade Object     `json:"acceptFeatureFlagDowngrade,omitempty"`
	CapacityPlanning           Object     `json:"capacityPlanning,omitempty"`
	ResourceGovernance         Object     `json:"resourceGovernance,omitempty"`
	Credentials                Object     `json:"credentials,omitempty"`
	EtcdEncryption             Object     `json:"etcdEncryption,omitempty"`
	Preflight                  Object     `json:"preflight,omitempty"`
	Migrate                    Object     `json:"migrate,omitempty"`
	Storage                    Object     `json:"storage,omitempty"`
	Backups                    Object     `json:"backups,omitempty"`
	Bootstrap                  Object     `json:"bootstrap,omitempty"`
}

// Global models the platform-wide chart values. The section is fully
// enumerated so the schema rejects unknown global.* keys and validates
// the security-affecting noEnvironmentPolicy enum. spec: §17.6 line 365,
// §16.1.1 (deploymentTier), §10.3 (spiffeTrustDomain, saTokenAudience).
type Global struct {
	// DeploymentTier is the §16.1.1 static metric tier label. It is left a
	// free string (not an enum) because the chart legitimately sets it to
	// the empty string to omit the deployment_tier metric relabel.
	DeploymentTier string `json:"deploymentTier,omitempty" desc:"Static metric tier label (tier1/tier2/tier3, or empty to omit the deployment_tier relabel). §16.1.1."`
	DevMode        bool   `json:"devMode,omitempty" desc:"Relaxes multi-tenant admission controls for local development. Never enable on a multi-tenant cluster. §4.9."`
	// MaintenanceMode is the §25.6 line 2974 global guard: when true the
	// doctor --fix auto-remediation surface skips every remediation.
	MaintenanceMode bool `json:"maintenanceMode,omitempty" desc:"When true, the §25.6 doctor --fix auto-remediation surface skips every remediation. §25.6 line 2974."`
	// NoEnvironmentPolicy is the §10.6/§17.6 line 365 platform-wide default
	// access policy. The gateway refuses to start when it is unset outside
	// --dev-mode, so the schema constrains it to the two documented values.
	NoEnvironmentPolicy string  `json:"noEnvironmentPolicy,omitempty" enum:"deny-all|allow-all" desc:"Platform-wide default access policy for a session that names no environment. §10.6 / §17.6 line 365."`
	SpiffeTrustDomain   string  `json:"spiffeTrustDomain,omitempty" desc:"SPIFFE trust domain anchoring agent-pod and interceptor identities. Required (no default); helm templating fails when unset. §10.3 line 316."`
	TraceSamplingRate   float64 `json:"traceSamplingRate,omitempty" min:"0" max:"1" desc:"Default probabilistic tail-sampling rate the OpenTelemetry Collector applies to normal traces. §16.3 line 359."`
	SaTokenAudience     string  `json:"saTokenAudience,omitempty" desc:"Projected SA-token audience the gateway validates on every pod→gateway request. §10.3 line 334."`
}

// Playground models the §27 web-playground chart values. The section is
// fully enumerated so the schema enforces the §17.6 line 373
// devTenantId pattern (^[a-zA-Z0-9_-]{1,128}$) and the authMode enum, and
// rejects out-of-range bearer-token TTLs.
type Playground struct {
	Enabled  bool   `json:"enabled,omitempty" desc:"Turns the §27 playground on. Off by default."`
	AuthMode string `json:"authMode,omitempty" enum:"oidc|apiKey|dev" desc:"Playground authentication mode. §27.3."`
	// DevTenantID is the §17.6 line 373 schema-constrained value. The
	// pattern matches the canonical tenant_id regex from §10.2 so a typo
	// (trailing whitespace, a `.` character) is rejected at helm
	// install/upgrade time rather than as a gateway CrashLoopBackOff.
	DevTenantID           string            `json:"devTenantId,omitempty" pattern:"^[a-zA-Z0-9_-]{1,128}$" maxLength:"128" desc:"Tenant bound to every dev-mode playground token; applies only when authMode is dev. §27.3."`
	AllowedRuntimes       string            `json:"allowedRuntimes,omitempty" desc:"Comma-separated glob list of runtime IDs the playground runtime picker offers. Default is every visible runtime."`
	MaxSessionMinutes     int               `json:"maxSessionMinutes,omitempty" min:"1" desc:"§27.6 hard cap on a playground-initiated session's duration."`
	MaxIdleTimeSeconds    int               `json:"maxIdleTimeSeconds,omitempty" min:"60" desc:"§27.6 hard idle-timeout override for a playground-initiated session."`
	OIDCSessionTTLSeconds int               `json:"oidcSessionTtlSeconds,omitempty" min:"60" desc:"Lifetime of the §27.3.1 server-side playground session record and its cookie."`
	BearerTTLSeconds      int               `json:"bearerTtlSeconds,omitempty" min:"60" max:"3600" desc:"TTL of an MCP bearer token minted by POST /v1/playground/token. The gateway bounds it to 60..3600."`
	GatewayHost           string            `json:"gatewayHost,omitempty" desc:"Public gateway host the playground UI connects to over the MCP WebSocket. §27.7 connect-src CSP."`
	SessionLabels         map[string]string `json:"sessionLabels,omitempty" desc:"§27.2 operator-tunable label map stamped on every playground session record and audit event."`
	AcknowledgeAPIKeyMode bool              `json:"acknowledgeApiKeyMode,omitempty" desc:"Acknowledges the §27.9 paste-form phishing surface for authMode=apiKey outside dev mode."`
}
