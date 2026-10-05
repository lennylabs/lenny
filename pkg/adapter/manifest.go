// SPDX-License-Identifier: MIT

package adapter

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter/localtools"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// MCPNonceBytes is the §15.4.3 intra-pod MCP nonce length: 256 bits.
const MCPNonceBytes = 32

// ManifestVersion is the §15.4 adapter-manifest schema version.
const ManifestVersion = 1

// ManifestFilename is the §15.4 adapter-manifest file name. The adapter
// writes it into the pod's /run/lenny directory before each runtime start;
// the runtime reads it to discover the pod-scoped sockets, the MCP nonce,
// and the adapter-local tools.
const ManifestFilename = "adapter-manifest.json"

// ManifestExperimentContext is the §8.3 / §10.7 experiment enrollment a
// session's session_start frame carries, so the runtime can tag the
// session's traces with variant metadata. The manifest does not carry it.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start).
type ManifestExperimentContext struct {
	ExperimentID string `json:"experimentId"`
	VariantID    string `json:"variantId"`
	Inherited    bool   `json:"inherited"`
}

// ManifestTool advertises one §15 adapter-local tool in the manifest's
// adapterLocalTools array: the tool name, a human-readable description,
// and the JSON Schema for its arguments object.
type ManifestTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ManifestMCPServer is the §4.7 platformMcpServer manifest object: the
// Unix socket the runtime connects to for the platform MCP server.
type ManifestMCPServer struct {
	Socket string `json:"socket"`
}

// ManifestConnector is one §4.7 connector MCP server entry: a
// connector identifier and the Unix socket its MCP server listens on.
type ManifestConnector struct {
	ID     string `json:"id"`
	Socket string `json:"socket"`
}

// ManifestRuntimeOps is the §15.4.6 runtimeOps manifest
// object: the Unix socket a Full-level runtime dials to reach the
// CH-RUNTIMEOPS.
type ManifestRuntimeOps struct {
	Socket string `json:"socket"`
}

// ManifestObservability is the §4.7 observability manifest object: the
// OTLP collector a runtime points its OpenTelemetry SDK at. Omitted when
// the deployment configures no collector.
type ManifestObservability struct {
	// OTLPEndpoint is the §4.7 / §16.3 OTLP collector URL. Production
	// profiles require https:// per §13.2 (NET-059).
	OTLPEndpoint string `json:"otlpEndpoint,omitempty"`
	// OTLPTLSEnabled is set false only for the dev / make-run profile to
	// permit an http:// endpoint; omitted (nil) otherwise.
	OTLPTLSEnabled *bool `json:"otlpTlsEnabled,omitempty"`
}

// ManifestLLM is the llm object of a session's session_start frame: the
// LLM provider configuration the runtime uses to set up its SDK for that
// session. The adapter derives it from the session's assigned §4.9
// credential lease(s). The manifest does not carry it.
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start).
type ManifestLLM struct {
	// DeliveryMode is the §4.9 credential delivery mode: "direct" or
	// "proxy". It tells the runtime whether to use the upstream provider's
	// native SDK (direct) or point its SDK at the proxy (proxy).
	DeliveryMode string `json:"deliveryMode"`
	// Dialect is the wire format the runtime's SDK speaks to the proxy:
	// "openai" or "anthropic". Set in proxy mode; omitted in direct mode.
	Dialect string `json:"dialect,omitempty"`
	// APIKeyEnv is the canonical env var the runtime's SDK reads for its
	// API key (ANTHROPIC_API_KEY for anthropic, OPENAI_API_KEY for openai).
	// Set in proxy mode where the runtime exports the lease token into it.
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`
}

// Manifest is the §15.4 adapter manifest: one pod-global document the
// adapter rewrites before each runtime start and the runtime reads to find
// the pod's intra-pod sockets, the §15.4.3 MCP nonce, the §15 adapter-local
// tool descriptors, and the runtime-definition descriptors. It carries
// only pod-scoped fields. A session's own identifier, credential path,
// experiment and tracing context, and LLM configuration reach the runtime
// in that session's session_start frame on CH-MSGSOCK, so a later start's
// rewrite of this file changes none of them for an earlier session that
// the same runtime process still serves. The schema version stays 1.
// spec: §4.7.6 (Adapter Manifest Field Reference); §28.5.3 (CH-MSGSOCK,
// Inbound: session_start).
type Manifest struct {
	Version int `json:"version"`
	// MCPNonce is the §15.4.3 intra-pod MCP authentication nonce: a
	// random 256-bit hex string the runtime presents on the MCP
	// initialize handshake to every adapter-local MCP server. The
	// adapter rejects an intra-pod MCP connection that does not present
	// it. The current writer mints a fresh nonce on each manifest write,
	// which happens once per runtime start. A running pod-wide MCP server
	// keeps validating the nonce of the start that armed it.
	// spec: §4.7.6 (Adapter Manifest Field Reference, mcpNonce row);
	// §15.4.3.
	MCPNonce string `json:"mcpNonce"`
	// AgentInterface is the runtime's §5.1 agentInterface descriptor,
	// carried verbatim from the Runtime definition. Null (JSON null) when
	// the runtime declares none. The field is always present per §4.7.
	AgentInterface json.RawMessage `json:"agentInterface"`
	// MinPlatformVersion is the runtime's §5.1 minPlatformVersion (semver).
	// Informational; omitted when the runtime specifies no minimum.
	MinPlatformVersion string `json:"minPlatformVersion,omitempty"`
	// Observability carries the §4.7 OTLP collector endpoint. Omitted when
	// the deployment configures no collector.
	Observability *ManifestObservability `json:"observability,omitempty"`
	// AdapterLocalTools advertises the §15 adapter-local tools the
	// runtime may call over the tool_call binary protocol. The runtime
	// discovers the tool set by reading this array.
	AdapterLocalTools []ManifestTool `json:"adapterLocalTools"`
	// PlatformMcpServer points the runtime at the §4.7 platform MCP
	// server's Unix socket. Omitted when the adapter runs no platform
	// MCP server (a Basic-level deployment).
	PlatformMcpServer *ManifestMCPServer `json:"platformMcpServer,omitempty"`
	// ConnectorServers lists the §4.7 per-connector MCP servers. Empty
	// when no connectors are authorized; never absent.
	ConnectorServers []ManifestConnector `json:"connectorServers"`
	// RuntimeMcpServers is the §4.7 slot reserved for type:mcp runtimes.
	// Empty in v1; never absent.
	RuntimeMcpServers []ManifestConnector `json:"runtimeMcpServers"`
	// RuntimeOps points a Full-level runtime at the §15.4.6
	// CH-RUNTIMEOPS's Unix socket. Omitted when the adapter runs no
	// CH-RUNTIMEOPS (a Basic-level deployment).
	RuntimeOps *ManifestRuntimeOps `json:"runtimeOps,omitempty"`
}

// newMCPNonce returns a fresh §15.4.3 intra-pod MCP nonce: a random
// 256-bit value, lowercase hex-encoded. A new nonce is generated for
// every session manifest write.
func newMCPNonce() (string, error) {
	b := make([]byte, MCPNonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("adapter: generate MCP nonce: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ManifestFileMode is the adapter-manifest permission bits: read/write
// for the owning adapter UID and read for the lenny-cred-readers group,
// no access for other UIDs. The agent-container runtime runs as a
// distinct UID (§13.1) but shares the lenny-cred-readers supplementary
// group via the pod fsGroup, so 0o640 lets the runtime read the manifest
// over its /run/lenny read-only mount (spec: §4.7) without
// exposing the §15.4.3 mcpNonce to any other UID in the pod. The mode
// mirrors the credential file's group-read boundary (credfile.FileMode).
const ManifestFileMode = 0o640

// WriteManifest writes m as adapter-manifest.json into dir at
// ManifestFileMode.
func WriteManifest(dir string, m Manifest) error {
	// §4.7 / §15: connectorServers, runtimeMcpServers, and
	// adapterLocalTools are "never absent" — a nil slice must serialize
	// as an empty array, not null.
	if m.ConnectorServers == nil {
		m.ConnectorServers = []ManifestConnector{}
	}
	if m.RuntimeMcpServers == nil {
		m.RuntimeMcpServers = []ManifestConnector{}
	}
	if m.AdapterLocalTools == nil {
		m.AdapterLocalTools = []ManifestTool{}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("adapter: encode manifest: %w", err)
	}
	return publishManifestBytes(dir, b)
}

// publishManifestBytes writes b as the manifest document in dir so that a
// reader on the pod observes one whole document. Two sessions starting at
// once on the same pod both rewrite the one pod-global file with no
// ordering between them (spec: §4.7), so a write applied in place onto the
// live path can interleave with the other and leave bytes that decode as
// neither session's manifest. The bytes are staged in a sibling temporary
// file and renamed over ManifestFilename instead: rename is atomic within
// the directory, so the published file always carries exactly one start's
// document, whichever rename landed last.
func publishManifestBytes(dir string, b []byte) (err error) {
	tmp, err := os.CreateTemp(dir, ManifestFilename+".*.tmp")
	if err != nil {
		return fmt.Errorf("adapter: stage manifest: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, werr := tmp.Write(b); werr != nil {
		_ = tmp.Close()
		return fmt.Errorf("adapter: write manifest: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return fmt.Errorf("adapter: close staged manifest: %w", cerr)
	}
	// os.CreateTemp creates the file at 0o600 and any explicit mode would
	// still be filtered by the process umask, so chmod the staged file to
	// the exact mode before it is published. That guarantees the §13.1
	// group-read boundary the agent runtime needs, and doing it before the
	// rename means the published path never exists at the wrong mode.
	if cerr := os.Chmod(tmpPath, ManifestFileMode); cerr != nil {
		return fmt.Errorf("adapter: chmod manifest: %w", cerr)
	}
	if rerr := os.Rename(tmpPath, filepath.Join(dir, ManifestFilename)); rerr != nil {
		return fmt.Errorf("adapter: publish manifest: %w", rerr)
	}
	return nil
}

// manifestInputs bundles the data a start receives through StartSession,
// Resume, or ConfigureWorkspace (or that the adapter derives) for the two
// writes the start makes: writeSessionManifest reads the runtime-definition
// descriptors and the connectors, and buildSessionStartFrame reads the
// session's experiment and tracing context.
type manifestInputs struct {
	experimentContext  *adapterv1.ExperimentContext
	tracingContext     map[string]string
	agentInterface     []byte // opaque JSON; nil writes a null manifest field
	minPlatformVersion string
	// connectors are the §9.3 per-connector MCP servers the adapter opens
	// for this session, rendered into the manifest connectorServers array
	// so the runtime can dial each one. Empty leaves the array empty (never
	// absent per §4.7). F-9.1.2.
	connectors []sessionConnector
}

// writeSessionManifest writes the §15.4 pod-scoped adapter manifest
// before a runtime start — the §4.7 agentInterface / minPlatformVersion /
// observability fields, the intra-pod socket fields, the §15 adapter-local
// tools, and a freshly minted §15.4.3 MCP nonce — when a ManifestDir is
// configured. StartSession, ConfigureWorkspace, and Resume call it so a
// runtime started on a fresh, SDK-warm, or resumed pod reads the same
// manifest; the session's own context goes in its session_start frame
// instead. It returns the generated MCP nonce so the caller can start the
// platform MCP server with the same nonce; when no ManifestDir is
// configured it is a no-op and returns an empty nonce.
// spec: §4.7.6 (Adapter Manifest Field Reference).
func (s *Server) writeSessionManifest(in manifestInputs) (string, error) {
	if s.ManifestDir == "" {
		return "", nil
	}
	nonce, err := newMCPNonce()
	if err != nil {
		return "", err
	}
	m := Manifest{
		Version:            ManifestVersion,
		MCPNonce:           nonce,
		AgentInterface:     manifestAgentInterface(in.agentInterface),
		MinPlatformVersion: in.minPlatformVersion,
		Observability:      s.manifestObservability(),
		AdapterLocalTools:  manifestLocalTools(),
	}
	if s.MCPSocket != "" {
		m.PlatformMcpServer = &ManifestMCPServer{Socket: s.MCPSocket}
	}
	// §9.3 — render one connectorServers entry per connector the
	// session's effective delegation policy permits, so the runtime can
	// dial each connector's intra-pod MCP server. F-9.1.2.
	for _, c := range in.connectors {
		m.ConnectorServers = append(m.ConnectorServers, ManifestConnector(c))
	}
	if s.Lifecycle != nil {
		m.RuntimeOps = &ManifestRuntimeOps{Socket: s.Lifecycle.SocketPath()}
	}
	if err := WriteManifest(s.ManifestDir, m); err != nil {
		return "", err
	}
	return nonce, nil
}

// sessionCredentialsPath derives the credentialsPath member of the named
// session's session_start frame: the same
// /run/lenny/slots/{sessionId}/credentials.json the credential handlers
// write, resolved from the session identifier through the one slot layout.
// An adapter wired with no credentials root resolves to the empty string,
// and the frame then omits the member. A session identifier that is not a
// safe path segment is an error rather than a path outside the slot tree.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start); §6.1 (per-session
// credential file).
func (s *Server) sessionCredentialsPath(sessionID string) (string, error) {
	paths, err := s.resolveSlotPaths(sessionID)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument,
			"resolve credential path for session %s: %v", sessionID, err)
	}
	return paths.CredentialsFile, nil
}

// manifestAgentInterface validates the gateway-supplied agentInterface
// JSON and returns it for the manifest. A nil or empty value yields a JSON
// null, matching the spec's "object or null" field. Invalid JSON is
// dropped to null rather than corrupting the manifest.
func manifestAgentInterface(b []byte) json.RawMessage {
	if len(b) == 0 || !json.Valid(b) {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

// manifestObservability builds the §4.7 observability manifest object from
// the adapter's configured OTLP endpoint. It returns nil (the field is
// omitted) when no endpoint is configured.
func (s *Server) manifestObservability() *ManifestObservability {
	if s.OTLPEndpoint == "" {
		return nil
	}
	o := &ManifestObservability{OTLPEndpoint: s.OTLPEndpoint}
	if s.OTLPTLSDisabled {
		disabled := false
		o.OTLPTLSEnabled = &disabled
	}
	return o
}

// manifestLLM derives the llm object of the named session's session_start
// frame from the session's own §6.1 lease set, which is where every assignment lands. It
// returns nil (a JSON null field) when no lease is assigned. When more
// than one provider lease is present the lease is selected
// deterministically by provider name; the full per-provider set is always
// in that session's own credential file. spec: §6.1; §28.5.3 (CH-MSGSOCK,
// Inbound: session_start).
func (s *Server) manifestLLM(sessionID string) *ManifestLLM {
	s.mu.Lock()
	var leases map[string]*adapterv1.CredentialLease
	if st, ok := s.slotStateLocked(sessionID); ok {
		leases = st.creds
	}
	s.mu.Unlock()
	if len(leases) == 0 {
		return nil
	}
	providers := make([]string, 0, len(leases))
	for p := range leases {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	return manifestLLMFromPayload(leases[providers[0]].GetPayload())
}

// llmPayload is the subset of the §4.7 credential-file entry the
// session_start llm object is derived from.
type llmPayload struct {
	DeliveryMode       string `json:"deliveryMode"`
	MaterializedConfig struct {
		ProxyDialect string `json:"proxyDialect"`
	} `json:"materializedConfig"`
}

// manifestLLMFromPayload builds the session_start llm object from one credential
// lease's payload. Proxy-mode leases carry the dialect and API-key variable
// the runtime configures its SDK with; direct-mode leases omit them because
// the runtime uses the upstream provider's native SDK. The proxy URL is
// not carried: it stays in the session's credential file alone, at
// materializedConfig.proxyUrl. spec: §4.7.11, item 4; §28.5.3 (CH-MSGSOCK,
// Inbound: session_start).
func manifestLLMFromPayload(payload []byte) *ManifestLLM {
	var p llmPayload
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	if p.DeliveryMode == "" {
		return nil
	}
	llm := &ManifestLLM{DeliveryMode: p.DeliveryMode}
	if p.DeliveryMode == "proxy" {
		llm.Dialect = p.MaterializedConfig.ProxyDialect
		llm.APIKeyEnv = apiKeyEnvForDialect(p.MaterializedConfig.ProxyDialect)
	}
	return llm
}

// apiKeyEnvForDialect returns the §4.7 canonical API-key env var the
// runtime's SDK reads for a proxy dialect. An unrecognized dialect yields
// an empty string (the field is then omitted).
// spec: §4.9; §26.5/§26.8/§26.9 (google); §26.6
// (cursor).
func apiKeyEnvForDialect(dialect string) string {
	switch dialect {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "google":
		return "GOOGLE_API_KEY"
	case "cursor":
		return "CURSOR_API_KEY"
	default:
		return ""
	}
}

// manifestLocalTools converts the localtools built-in descriptors into
// their manifest form, the single source of the adapter-local tool set
// the adapter both advertises and dispatches.
func manifestLocalTools() []ManifestTool {
	descriptors := localtools.Descriptors()
	tools := make([]ManifestTool, len(descriptors))
	for i, d := range descriptors {
		tools[i] = ManifestTool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		}
	}
	return tools
}

// ErrManifestVersionTooHigh reports an adapter manifest whose version
// exceeds the highest version this build understands. Per §4.7 a runtime
// MUST reject such a manifest because every version increment is a
// breaking change to existing field semantics.
var ErrManifestVersionTooHigh = fmt.Errorf("adapter: manifest version exceeds the highest understood version %d", ManifestVersion)

// ReadManifest decodes the adapter manifest from dir and enforces the §4.7
// forward-compatibility rule: a manifest whose version is higher than
// ManifestVersion is rejected with ErrManifestVersionTooHigh. A Go runtime
// SDK reads the manifest through this helper so the version check is
// applied uniformly rather than re-encoded at each call site.
func ReadManifest(dir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		return Manifest{}, fmt.Errorf("adapter: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("adapter: decode manifest: %w", err)
	}
	if m.Version > ManifestVersion {
		return Manifest{}, ErrManifestVersionTooHigh
	}
	return m, nil
}

// manifestExperimentContext converts the StartSession proto experiment
// context into the session_start experimentContext member. It returns nil
// for an unenrolled session, which the frame writes as JSON null.
func manifestExperimentContext(ec *adapterv1.ExperimentContext) *ManifestExperimentContext {
	if ec == nil {
		return nil
	}
	return &ManifestExperimentContext{
		ExperimentID: ec.GetExperimentId(),
		VariantID:    ec.GetVariantId(),
		Inherited:    ec.GetInherited(),
	}
}
