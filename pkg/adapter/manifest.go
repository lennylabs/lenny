// SPDX-License-Identifier: MIT

package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

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
// v1 carries the session metadata a Basic-level runtime needs; the
// intra-pod MCP fields (mcpNonce, platformMcpServer, adapterLocalTools)
// are added with the MCP fabric.
type Manifest struct {
	Version           int                        `json:"version"`
	SessionID         string                     `json:"sessionId"`
	WorkspaceRoot     string                     `json:"workspaceRoot"`
	ExperimentContext *ManifestExperimentContext `json:"experimentContext,omitempty"`
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
