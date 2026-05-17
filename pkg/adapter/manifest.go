// SPDX-License-Identifier: MIT

package adapter

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

// Manifest is the §15.4 adapter manifest the runtime reads at startup.
// v1 carries the session metadata a Basic-level runtime needs, the
// §15.4.3 intra-pod MCP nonce, and the §15 adapter-local tool
// descriptors; the platformMcpServer and connectorServers socket
// fields are added with the MCP socket layer.
type Manifest struct {
	Version           int                        `json:"version"`
	SessionID         string                     `json:"sessionId"`
	WorkspaceRoot     string                     `json:"workspaceRoot"`
	ExperimentContext *ManifestExperimentContext `json:"experimentContext,omitempty"`
	// TracingContext is the §8.3 opaque tracing-identifier map the
	// runtime uses to stitch its native traces into the parent's trace
	// tree. Omitted when no tracing context is set.
	TracingContext map[string]string `json:"tracingContext,omitempty"`
	// MCPNonce is the §15.4.3 intra-pod MCP authentication nonce: a
	// random 256-bit hex string the runtime presents on the MCP
	// initialize handshake to every adapter-local MCP server. The
	// adapter rejects an intra-pod MCP connection that does not present
	// it. Omitted when no manifest directory is configured.
	MCPNonce string `json:"mcpNonce,omitempty"`
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

// WriteManifest writes m as adapter-manifest.json into dir. The file is
// created mode 0644 so the agent-container runtime, which runs as a
// different user, can read it.
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
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), b, 0o644); err != nil {
		return fmt.Errorf("adapter: write manifest: %w", err)
	}
	return nil
}

// writeSessionManifest writes the §15.4 adapter manifest for a session
// — carrying the §8.3 experimentContext and tracingContext, the §15
// adapter-local tools, and the §15.4.3 MCP nonce — when a ManifestDir
// is configured. StartSession and Resume both call it so a runtime
// started on a fresh or a resumed pod reads the same manifest. It
// returns the generated MCP nonce so the caller can start the platform
// MCP server with the same nonce; when no ManifestDir is configured it
// is a no-op and returns an empty nonce.
func (s *Server) writeSessionManifest(sessionID string, ec *adapterv1.ExperimentContext, tc map[string]string) (string, error) {
	if s.ManifestDir == "" {
		return "", nil
	}
	nonce, err := newMCPNonce()
	if err != nil {
		return "", err
	}
	m := Manifest{
		Version:           ManifestVersion,
		SessionID:         sessionID,
		WorkspaceRoot:     s.WorkspaceRoot,
		ExperimentContext: manifestExperimentContext(ec),
		TracingContext:    tc,
		MCPNonce:          nonce,
		AdapterLocalTools: manifestLocalTools(),
	}
	if s.MCPSocket != "" {
		m.PlatformMcpServer = &ManifestMCPServer{Socket: s.MCPSocket}
	}
	if err := WriteManifest(s.ManifestDir, m); err != nil {
		return "", err
	}
	return nonce, nil
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
