// SPDX-License-Identifier: MIT

package adapter

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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

// Manifest is the §15.4 adapter manifest the runtime reads at startup.
// v1 carries the session metadata a Basic-level runtime needs and the
// §15.4.3 intra-pod MCP nonce; the platformMcpServer and
// adapterLocalTools fields are added with the MCP server.
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
// — carrying the §8.3 experimentContext and tracingContext — when a
// ManifestDir is configured. It is a no-op when ManifestDir is empty,
// so an adapter without one is unchanged. StartSession and Resume both
// call it so a runtime started on a fresh or a resumed pod reads the
// same manifest.
func (s *Server) writeSessionManifest(sessionID string, ec *adapterv1.ExperimentContext, tc map[string]string) error {
	if s.ManifestDir == "" {
		return nil
	}
	nonce, err := newMCPNonce()
	if err != nil {
		return err
	}
	return WriteManifest(s.ManifestDir, Manifest{
		Version:           ManifestVersion,
		SessionID:         sessionID,
		WorkspaceRoot:     s.WorkspaceRoot,
		ExperimentContext: manifestExperimentContext(ec),
		TracingContext:    tc,
		MCPNonce:          nonce,
	})
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
