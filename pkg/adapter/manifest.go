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

	"github.com/lennylabs/lenny/pkg/adapter/localtools"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// MCPNonceBytes is the §15.4.3 intra-pod MCP nonce length: 256 bits.
const MCPNonceBytes = 32

// ManifestVersion is the §15.4 adapter-manifest schema version.
const ManifestVersion = 1

// ManifestFilename is the §15.4 adapter-manifest file name. The adapter
// writes it into the pod's /run/lenny directory before spawning the
// runtime; the runtime reads it at startup to discover session
// metadata.
const ManifestFilename = "adapter-manifest.json"

// ManifestExperimentContext is the §8.3 / §10.7 experiment enrollment
// recorded in the adapter manifest so the runtime can tag traces with
// variant metadata.
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

// ManifestLifecycleChannel is the §15.4.6 lifecycleChannel manifest
// object: the Unix socket a Full-level runtime dials to reach the
// lifecycle channel.
type ManifestLifecycleChannel struct {
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

// ManifestLLM is the §4.7 llm manifest object: the LLM provider
// configuration the runtime uses to set up its SDK. The adapter derives it
// from the session's assigned §4.9 credential lease(s).
type ManifestLLM struct {
	// DeliveryMode is the §4.9 credential delivery mode: "direct" or
	// "proxy". It tells the runtime whether to use the upstream provider's
	// native SDK (direct) or point its SDK at the proxy (proxy).
	DeliveryMode string `json:"deliveryMode"`
	// Dialect is the wire format the runtime's SDK speaks to the proxy:
	// "openai" or "anthropic". Set in proxy mode; omitted in direct mode.
	Dialect string `json:"dialect,omitempty"`
	// BaseURL is the proxy endpoint the runtime configures its SDK against
	// (the lease's proxyUrl). Set in proxy mode; omitted in direct mode.
	BaseURL string `json:"baseUrl,omitempty"`
	// APIKeyEnv is the canonical env var the runtime's SDK reads for its
	// API key (ANTHROPIC_API_KEY for anthropic, OPENAI_API_KEY for openai).
	// Set in proxy mode where the runtime exports the lease token into it.
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`
}

// Manifest is the §15.4 adapter manifest the runtime reads at startup.
// v1 carries the session metadata a Basic-level runtime needs, the
// §15.4.3 intra-pod MCP nonce, and the §15 adapter-local tool
// descriptors; the platformMcpServer and connectorServers socket
// fields are added with the MCP socket layer.
type Manifest struct {
	Version   int    `json:"version"`
	SessionID string `json:"sessionId"`
	// TaskID is the §4.7 current task identifier. In session mode it is
	// the session id (the session is its single task); task-mode pods
	// rewrite it before each task.
	TaskID            string                     `json:"taskId"`
	ExperimentContext *ManifestExperimentContext `json:"experimentContext,omitempty"`
	// TracingContext is the §8.3 opaque tracing-identifier map the
	// runtime uses to stitch its native traces into the parent's trace
	// tree. Omitted when no tracing context is set.
	TracingContext map[string]string `json:"tracingContext,omitempty"`
	// MCPNonce is the §15.4.3 intra-pod MCP authentication nonce: a
	// random 256-bit hex string the runtime presents on the MCP
	// initialize handshake to every adapter-local MCP server. The
	// adapter rejects an intra-pod MCP connection that does not present
	// it. Required at the Standard and Full levels (§4.7).
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
	// LLM is the §4.7 LLM provider configuration derived from the session's
	// credential lease. Null (JSON null) when the session has no active LLM
	// lease. The field is always present per §4.7.
	LLM *ManifestLLM `json:"llm"`
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
	// LifecycleChannel points a Full-level runtime at the §15.4.6
	// lifecycle channel's Unix socket. Omitted when the adapter runs no
	// lifecycle channel (a Basic-level deployment).
	LifecycleChannel *ManifestLifecycleChannel `json:"lifecycleChannel,omitempty"`
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
// over its /run/lenny read-only mount (spec: §4.7 line 846) without
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
	path := filepath.Join(dir, ManifestFilename)
	if err := os.WriteFile(path, b, ManifestFileMode); err != nil {
		return fmt.Errorf("adapter: write manifest: %w", err)
	}
	// os.WriteFile honors the process umask, so an inherited umask could
	// strip the group-read bit the agent runtime needs. Chmod the file to
	// the exact mode to guarantee the §13.1 group-read boundary regardless
	// of umask.
	if err := os.Chmod(path, ManifestFileMode); err != nil {
		return fmt.Errorf("adapter: chmod manifest: %w", err)
	}
	return nil
}

// manifestInputs bundles the per-session §15.4 manifest data the gateway
// delivers through StartSession / Resume (or that the adapter derives) for
// writeSessionManifest to assemble.
type manifestInputs struct {
	sessionID          string
	taskID             string
	experimentContext  *adapterv1.ExperimentContext
	tracingContext     map[string]string
	agentInterface     []byte // opaque JSON; nil writes a null manifest field
	minPlatformVersion string
}

// writeSessionManifest writes the §15.4 adapter manifest for a session —
// carrying the §4.7 taskId / agentInterface / minPlatformVersion / llm /
// observability fields, the §8.3 experimentContext and tracingContext, the
// §15 adapter-local tools, and the §15.4.3 MCP nonce — when a ManifestDir
// is configured. StartSession, ConfigureWorkspace, and Resume call it so a
// runtime started on a fresh, SDK-warm, or resumed pod reads the same
// manifest. It returns the generated MCP nonce so the caller can start the
// platform MCP server with the same nonce; when no ManifestDir is
// configured it is a no-op and returns an empty nonce.
func (s *Server) writeSessionManifest(in manifestInputs) (string, error) {
	if s.ManifestDir == "" {
		return "", nil
	}
	nonce, err := newMCPNonce()
	if err != nil {
		return "", err
	}
	// §4.7: in session mode the manifest taskId defaults to the session id
	// (the session is its single task) when the gateway supplies none.
	taskID := in.taskID
	if taskID == "" {
		taskID = in.sessionID
	}
	m := Manifest{
		Version:            ManifestVersion,
		SessionID:          in.sessionID,
		TaskID:             taskID,
		ExperimentContext:  manifestExperimentContext(in.experimentContext),
		TracingContext:     in.tracingContext,
		MCPNonce:           nonce,
		AgentInterface:     manifestAgentInterface(in.agentInterface),
		MinPlatformVersion: in.minPlatformVersion,
		Observability:      s.manifestObservability(),
		LLM:                s.manifestLLM(),
		AdapterLocalTools:  manifestLocalTools(),
	}
	if s.MCPSocket != "" {
		m.PlatformMcpServer = &ManifestMCPServer{Socket: s.MCPSocket}
	}
	if s.Lifecycle != nil {
		m.LifecycleChannel = &ManifestLifecycleChannel{Socket: s.Lifecycle.SocketPath()}
	}
	if err := WriteManifest(s.ManifestDir, m); err != nil {
		return "", err
	}
	return nonce, nil
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

// manifestLLM derives the §4.7 llm manifest object from the session's
// assigned §4.9 credential lease. It returns nil (a JSON null field) when
// no lease is assigned. When more than one provider lease is present the
// lease is selected deterministically by provider name; the full
// per-provider set is always in /run/lenny/credentials.json.
func (s *Server) manifestLLM() *ManifestLLM {
	s.mu.Lock()
	leases := s.credLeases
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

// llmPayload is the subset of the §4.7 credential-file entry the manifest
// llm field is derived from.
type llmPayload struct {
	DeliveryMode       string `json:"deliveryMode"`
	MaterializedConfig struct {
		ProxyURL     string `json:"proxyUrl"`
		ProxyDialect string `json:"proxyDialect"`
	} `json:"materializedConfig"`
}

// manifestLLMFromPayload builds the §4.7 llm manifest object from one
// credential lease's payload. Proxy-mode leases carry the dialect and base
// URL the runtime points its SDK at; direct-mode leases omit them because
// the runtime uses the upstream provider's native SDK.
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
		llm.BaseURL = p.MaterializedConfig.ProxyURL
		llm.APIKeyEnv = apiKeyEnvForDialect(p.MaterializedConfig.ProxyDialect)
	}
	return llm
}

// apiKeyEnvForDialect returns the §4.7 canonical API-key env var the
// runtime's SDK reads for a proxy dialect. An unrecognized dialect yields
// an empty string (the field is then omitted).
// spec: §4.9 lines 1473-1474; §26.5/§26.8/§26.9 (google); §26.6 line 297
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
// context into its manifest form. It returns nil for an unenrolled
// session.
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
