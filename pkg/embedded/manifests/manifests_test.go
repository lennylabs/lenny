// SPDX-License-Identifier: MIT

package manifests_test

import (
	"bytes"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/embedded/manifests"
)

// docMeta is the minimal projection of a rendered manifest the assertions
// below key on: the kind, the metadata name, the metadata annotations,
// and the Service spec.type/nodePort for the gateway NodePort check.
type docMeta struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Handler string `json:"handler"` // RuntimeClass.handler
	Spec    struct {
		Type  string `json:"type"`
		Ports []struct {
			Name     string `json:"name"`
			NodePort int    `json:"nodePort"`
		} `json:"ports"`
	} `json:"spec"`
}

// decodeEmbedded splits the embedded multi-document stream and decodes
// each non-empty document into a docMeta. The embedded file is the
// `make generate` render (helm template --no-hooks) of charts/lenny under
// the development profile.
func decodeEmbedded(t *testing.T) []docMeta {
	t.Helper()
	raw, err := manifests.FS.ReadFile("manifests.yaml")
	if err != nil {
		t.Fatalf("read embedded manifests.yaml: %v", err)
	}
	var out []docMeta
	for _, chunk := range bytes.Split(raw, []byte("\n---")) {
		trimmed := bytes.TrimSpace(chunk)
		if len(trimmed) == 0 {
			continue
		}
		// Drop leading comment-only documents (the generated header).
		if hasOnlyComments(trimmed) {
			continue
		}
		var d docMeta
		if err := yaml.Unmarshal(trimmed, &d); err != nil {
			t.Fatalf("decode embedded document: %v\n---\n%s", err, trimmed)
		}
		if d.Kind == "" {
			continue
		}
		out = append(out, d)
	}
	return out
}

func hasOnlyComments(b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		return false
	}
	return true
}

// TestEmbeddedManifestsDecodeToControlPlane pins that the embedded set is
// the §17.4 in-cluster control plane: the gateway, controller, and the
// mandatory lenny-ops Deployments rendered from the chart under the
// development profile. A failure means the render lost a control-plane
// Deployment, so Embedded Mode would bring up an incomplete control plane.
//
// spec: §17.4 (in-cluster control plane).
func TestEmbeddedManifestsDecodeToControlPlane_spec_17_4(t *testing.T) {
	docs := decodeEmbedded(t)
	if len(docs) == 0 {
		t.Fatal("embedded manifests decoded to zero documents")
	}
	deployments := map[string]bool{}
	for _, d := range docs {
		if d.Kind == "Deployment" {
			deployments[d.Metadata.Name] = true
		}
	}
	// The lenny-ops Deployment is keyed by name rather than a
	// lenny.dev/component label: the ops template labels it `app:
	// lenny-ops`, not lenny.dev/component, unlike gateway and controller.
	for _, want := range []string{"lenny-gateway", "lenny-controller", "lenny-ops"} {
		if !deployments[want] {
			t.Errorf("embedded manifests missing the %q Deployment; the §17.4 in-cluster control plane requires the gateway, controller, and mandatory lenny-ops Deployments", want)
		}
	}
}

// TestEmbeddedManifestsRenderRuncRuntimeClass pins that the development
// profile rendered the `runc` RuntimeClass the echo pool's `standard`
// isolation profile resolves to. Without it the WarmPoolController marks
// the pool Degraded and suppresses pod creation, so the echo pool never
// warms.
//
// spec: §17.4 (in-cluster control plane), §5.3 (isolation profiles).
func TestEmbeddedManifestsRenderRuncRuntimeClass_spec_17_4(t *testing.T) {
	docs := decodeEmbedded(t)
	for _, d := range docs {
		if d.Kind == "RuntimeClass" && d.Metadata.Name == "runc" {
			if d.Handler != "runc" {
				t.Errorf("runc RuntimeClass handler = %q; want runc", d.Handler)
			}
			return
		}
	}
	t.Error("embedded manifests missing the `runc` RuntimeClass the echo pool's standard isolation profile resolves to")
}

// TestEmbeddedGatewayServiceIsNodePort pins that the gateway Service is a
// NodePort with the fixed development port, the stable endpoint the
// loopback host-side forwarder targets. The chart default is ClusterIP,
// which the host forwarder could not reach.
//
// spec: §17.4 (gateway loopback forwarder).
func TestEmbeddedGatewayServiceIsNodePort_spec_17_4(t *testing.T) {
	docs := decodeEmbedded(t)
	for _, d := range docs {
		if d.Kind == "Service" && d.Metadata.Name == "lenny-gateway" {
			if d.Spec.Type != "NodePort" {
				t.Errorf("gateway Service type = %q; want NodePort", d.Spec.Type)
			}
			var found bool
			for _, p := range d.Spec.Ports {
				if p.NodePort == 30080 {
					found = true
				}
			}
			if !found {
				t.Errorf("gateway Service does not pin nodePort 30080; ports = %+v", d.Spec.Ports)
			}
			return
		}
	}
	t.Error("embedded manifests missing the lenny-gateway Service")
}

// TestEmbeddedManifestsExcludeHooksAndCertManager pins the §17.4 decisions
// that the embedded set carries no helm.sh/hook-annotated resource (which a
// plain server-side apply would create as an ordinary object) and no
// cert-manager.io object (which a bare k3s without a CA injector could not
// resolve). The check is on the embedded bytes, so it holds in-process
// without re-rendering through helm.
//
// spec: §17.4 (pre-rendered embedded manifests).
func TestEmbeddedManifestsExcludeHooksAndCertManager_spec_17_4(t *testing.T) {
	raw, err := manifests.FS.ReadFile("manifests.yaml")
	if err != nil {
		t.Fatalf("read embedded manifests.yaml: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "helm.sh/hook") {
		t.Error("embedded manifests contain a helm.sh/hook annotation; the set must be rendered with --no-hooks")
	}
	if strings.Contains(body, "cert-manager.io/") {
		t.Error("embedded manifests reference cert-manager.io; the development profile disables the admission/mTLS stack")
	}
	for _, d := range decodeEmbedded(t) {
		if strings.HasPrefix(d.APIVersion, "cert-manager.io/") {
			t.Errorf("embedded manifests carry a %s/%s object on cert-manager API group %s", d.Kind, d.Metadata.Name, d.APIVersion)
		}
	}
}
