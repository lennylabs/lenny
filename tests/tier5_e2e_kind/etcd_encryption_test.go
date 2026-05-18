// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for Phase 5.4 — etcd encryption at rest.
//
// The test stands up a dedicated Kind cluster whose control-plane
// kube-apiserver is started with --encryption-provider-config pointing
// at the apiserver.config.k8s.io/v1 EncryptionConfiguration that the
// Lenny Helm chart renders into the lenny-etcd-encryption ConfigMap.
// It then writes a Kubernetes Secret, reads the raw value straight out
// of etcd with etcdctl, and asserts the stored bytes are ciphertext
// (the k8s:enc: prefix) and do not contain the plaintext Secret value.
//
// The cluster is dedicated (its own name, not the shared lenny-e2e
// cluster) because it needs a control-plane apiserver flag the shared
// tests/testinfra/kind/cluster.yaml does not set, and it is torn down
// via t.Cleanup so it does not leak past the test.

package tier5_e2e_kind_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

const (
	// etcEncClusterName is the dedicated cluster name. It is distinct
	// from the shared lenny-e2e cluster so the encryption-provider-config
	// apiserver flag does not bleed into other tier-5 tests.
	etcEncClusterName = "lenny-etcd-enc-e2e"
	// etcEncNodeImage pins the control-plane node image to the same
	// version the shared kind cluster.yaml uses.
	etcEncNodeImage = "kindest/node:v1.31.4"
	// etcEncConfigNodePath is where the EncryptionConfiguration file is
	// mounted inside the control-plane node and where the apiserver
	// reads it from via --encryption-provider-config.
	etcEncConfigNodePath = "/etc/kubernetes/enc/encryption-config.yaml"
)

// spec: 4 (§4 encryption at rest — a Secret is written, etcdctl on
//
//	the raw key returns ciphertext; Phase 5.4)
//
// diagnosis: The control-plane apiserver was started with
//
//	--encryption-provider-config pointing at the chart's
//	EncryptionConfiguration, a Secret was written, and its raw
//	etcd value was read with etcdctl. A failure means either the
//	apiserver did not load the config (raw value is plaintext
//	JSON, not k8s:enc:aescbc), the chart did not render the
//	EncryptionConfiguration, or the dedicated Kind cluster could
//	not be brought up with the custom apiserver flag. Inspect the
//	logged kind / kubectl / docker exec output.
func TestEtcdEncryption(t *testing.T) {
	kind.PrerequisitesAvailable(t)

	root := repoRoot(t)

	// Render the EncryptionConfiguration the Helm chart ships. The
	// chart cannot reconfigure the apiserver itself, so the test plays
	// the operator role: it extracts the document and wires it into the
	// apiserver via the Kind cluster config below.
	encConfig := renderEncryptionConfig(t, root)
	if !strings.Contains(encConfig, "apiserver.config.k8s.io/v1") ||
		!strings.Contains(encConfig, "kind: EncryptionConfiguration") {
		t.Fatalf("chart did not render an EncryptionConfiguration:\n%s", encConfig)
	}
	if !strings.Contains(encConfig, "aescbc") {
		t.Fatalf("EncryptionConfiguration lacks an aescbc provider:\n%s", encConfig)
	}

	// Write the rendered document to a host directory. extraMounts in
	// the Kind config bind-mounts this directory into the control-plane
	// node, and the apiserver reads the file from inside the node.
	hostEncDir := t.TempDir()
	hostEncPath := filepath.Join(hostEncDir, "encryption-config.yaml")
	if err := os.WriteFile(hostEncPath, []byte(encConfig), 0o600); err != nil {
		t.Fatalf("write encryption config: %v", err)
	}

	// Write the dedicated Kind cluster config. kubeadmConfigPatches add
	// --encryption-provider-config to the apiserver and declare the
	// hostPath volume; extraMounts bind-mounts the host directory.
	kindCfgPath := filepath.Join(hostEncDir, "kind-cluster.yaml")
	if err := os.WriteFile(kindCfgPath, []byte(kindClusterConfig(hostEncDir)), 0o600); err != nil {
		t.Fatalf("write kind cluster config: %v", err)
	}

	// Bring up the dedicated cluster. Always register teardown first so
	// a failure mid-bring-up still cleans the partial cluster up.
	t.Cleanup(func() {
		out, err := exec.Command("kind", "delete", "cluster", "--name", etcEncClusterName).CombinedOutput()
		if err != nil {
			t.Logf("kind delete cluster %s: %v\n%s", etcEncClusterName, err, out)
		}
	})
	// Delete any stale cluster of the same name from a prior aborted run.
	_ = exec.Command("kind", "delete", "cluster", "--name", etcEncClusterName).Run()

	create := exec.Command("kind", "create", "cluster",
		"--name", etcEncClusterName,
		"--image", etcEncNodeImage,
		"--config", kindCfgPath,
		"--wait", "120s",
	)
	create.Stdout = os.Stdout
	create.Stderr = os.Stderr
	if err := create.Run(); err != nil {
		t.Fatalf("kind create cluster %s with encryption-provider-config failed: %v", etcEncClusterName, err)
	}

	kubeconfig := writeKubeconfig(t, etcEncClusterName)

	// Confirm the apiserver actually loaded the encryption flag. If the
	// flag did not take, the rest of the test would still write a
	// Secret but the etcd value would be plaintext; checking the flag
	// first gives a precise diagnosis.
	assertAPIServerEncryptionFlag(t, kubeconfig)

	// Write a Secret with a recognizable plaintext value. The value is
	// the needle the test searches for in the raw etcd bytes.
	const (
		secretNS    = "default"
		secretName  = "lenny-etcd-enc-probe"
		secretKey   = "probe"
		secretValue = "lenny-etcd-encryption-plaintext-canary-2f9c"
	)
	createSecret := exec.Command("kubectl", "--kubeconfig", kubeconfig,
		"create", "secret", "generic", secretName,
		"-n", secretNS,
		"--from-literal="+secretKey+"="+secretValue,
	)
	if out, err := createSecret.CombinedOutput(); err != nil {
		t.Fatalf("kubectl create secret: %v\n%s", err, out)
	}

	// Read the raw value straight from etcd. etcd runs as a static pod
	// (mirrored as etcd-<control-plane-node> in kube-system); the etcd
	// container image bundles etcdctl and mounts the static etcd PKI at
	// /etc/kubernetes/pki/etcd. The key path follows the registry
	// convention /registry/secrets/<namespace>/<name>.
	controlPlaneNode := etcEncClusterName + "-control-plane"
	etcdKey := fmt.Sprintf("/registry/secrets/%s/%s", secretNS, secretName)
	rawValue := etcdGetRaw(t, kubeconfig, controlPlaneNode, etcdKey)

	// The stored bytes must be ciphertext: Kubernetes prefixes encrypted
	// values with k8s:enc:<provider>:. With the chart's aescbc provider
	// the prefix is k8s:enc:aescbc:v1:.
	if !strings.Contains(rawValue, "k8s:enc:") {
		t.Fatalf("Secret %s/%s is not encrypted at rest: raw etcd value lacks the k8s:enc: prefix.\nraw value:\n%q",
			secretNS, secretName, rawValue)
	}
	if !strings.Contains(rawValue, "k8s:enc:aescbc:") {
		t.Errorf("Secret %s/%s carries an unexpected encryption prefix; want k8s:enc:aescbc:.\nraw value:\n%q",
			secretNS, secretName, rawValue)
	}

	// The plaintext value must not appear anywhere in the stored bytes.
	if strings.Contains(rawValue, secretValue) {
		t.Fatalf("Secret %s/%s plaintext value is readable in etcd; encryption at rest is not effective.\nraw value:\n%q",
			secretNS, secretName, rawValue)
	}
	if strings.Contains(rawValue, secretKey+":"+secretValue) ||
		strings.Contains(rawValue, secretKey+"="+secretValue) {
		t.Fatalf("Secret %s/%s key/value pair is readable in etcd.\nraw value:\n%q",
			secretNS, secretName, rawValue)
	}

	t.Logf("Secret %s/%s stored as ciphertext in etcd (k8s:enc:aescbc); plaintext canary absent", secretNS, secretName)
}

// renderEncryptionConfig renders the chart's etcd-encryption template
// and returns the embedded EncryptionConfiguration document.
func renderEncryptionConfig(t *testing.T, root string) string {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skipf("helm not on PATH: %v", err)
	}
	chart := filepath.Join(root, "charts", "lenny")
	cmd := exec.Command(helm, "template", chart,
		"--show-only", "templates/etcd-encryption.yaml",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template etcd-encryption.yaml: %v\n%s", err, stderr.String())
	}
	doc := extractConfigMapData(t, stdout.String())
	return doc
}

// extractConfigMapData pulls the encryption-config.yaml value out of
// the rendered ConfigMap. The value is a literal block scalar indented
// under `  encryption-config.yaml: |`; this returns the de-indented
// document body.
func extractConfigMapData(t *testing.T, rendered string) string {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	const marker = "encryption-config.yaml: |"
	start := -1
	for i, l := range lines {
		if strings.Contains(l, marker) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("rendered ConfigMap has no encryption-config.yaml block:\n%s", rendered)
	}
	// The block scalar is indented four spaces under the two-space
	// `data:` mapping; de-indent by the indentation of the first body
	// line so the apiserver receives a valid document.
	var body []string
	indent := -1
	for _, l := range lines[start:] {
		if strings.TrimSpace(l) == "" {
			body = append(body, "")
			continue
		}
		curIndent := len(l) - len(strings.TrimLeft(l, " "))
		if indent < 0 {
			indent = curIndent
		}
		if curIndent < indent {
			break
		}
		body = append(body, l[indent:])
	}
	return strings.TrimRight(strings.Join(body, "\n"), "\n") + "\n"
}

// kindClusterConfig returns a Kind cluster config whose control-plane
// apiserver is started with --encryption-provider-config. hostEncDir is
// bind-mounted into the node at the directory holding the config file.
func kindClusterConfig(hostEncDir string) string {
	nodeEncDir := filepath.Dir(etcEncConfigNodePath)
	return fmt.Sprintf(`# Dedicated Kind cluster for the Phase 5.4 etcd-encryption e2e test.
# The single control-plane node runs its kube-apiserver with
# --encryption-provider-config so Kubernetes Secrets are encrypted at
# rest in etcd. extraMounts bind-mounts the host directory carrying the
# rendered EncryptionConfiguration into the node.
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: %s
nodes:
  - role: control-plane
    image: %s
    extraMounts:
      - hostPath: %s
        containerPath: %s
        readOnly: true
    kubeadmConfigPatches:
      - |
        kind: ClusterConfiguration
        apiServer:
          extraArgs:
            encryption-provider-config: %s
          extraVolumes:
            - name: encryption-config
              hostPath: %s
              mountPath: %s
              readOnly: true
              pathType: File
`,
		etcEncClusterName,
		etcEncNodeImage,
		hostEncDir,
		nodeEncDir,
		etcEncConfigNodePath,
		etcEncConfigNodePath,
		etcEncConfigNodePath,
	)
}

// writeKubeconfig fetches the dedicated cluster's kubeconfig and writes
// it to a temp file, returning the path.
func writeKubeconfig(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("kind", "get", "kubeconfig", "--name", name).Output()
	if err != nil {
		t.Fatalf("kind get kubeconfig --name %s: %v", name, err)
	}
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

// assertAPIServerEncryptionFlag confirms the control-plane apiserver
// static pod carries the --encryption-provider-config flag. It retries
// because the apiserver pod takes a moment to register after the
// cluster reports ready.
func assertAPIServerEncryptionFlag(t *testing.T, kubeconfig string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var lastOut string
	var lastErr error
	for time.Now().Before(deadline) {
		cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig,
			"-n", "kube-system", "get", "pod",
			"-l", "component=kube-apiserver",
			"-o", "jsonpath={.items[*].spec.containers[*].command}",
		)
		out, err := cmd.CombinedOutput()
		lastOut, lastErr = string(out), err
		if err == nil && strings.Contains(string(out), "--encryption-provider-config") {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("kube-apiserver did not come up with --encryption-provider-config within the deadline.\nlast err: %v\nlast output: %s",
		lastErr, lastOut)
}

// etcdGetRaw reads the raw value at etcdKey straight from etcd via
// etcdctl, using the static etcd PKI. etcd runs as a static pod
// mirrored into kube-system as etcd-<control-plane-node>; the etcd
// container image bundles etcdctl, so the read goes through
// kubectl exec rather than docker exec on the node (the kindest node
// image does not ship etcdctl on its own PATH).
func etcdGetRaw(t *testing.T, kubeconfig, controlPlaneNode, etcdKey string) string {
	t.Helper()
	etcdPod := "etcd-" + controlPlaneNode
	// etcd 3.4+ defaults etcdctl to the v3 API. --print-value-only keeps
	// the key out of the output so the search for the plaintext canary
	// is unambiguous. Args are passed directly with no shell wrapper
	// because the etcd container image has no shell.
	args := []string{
		"--kubeconfig", kubeconfig,
		"-n", "kube-system", "exec", etcdPod, "--",
		"etcdctl",
		"--endpoints=https://127.0.0.1:2379",
		"--cacert=/etc/kubernetes/pki/etcd/ca.crt",
		"--cert=/etc/kubernetes/pki/etcd/server.crt",
		"--key=/etc/kubernetes/pki/etcd/server.key",
		"get", etcdKey, "--print-value-only",
	}
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl exec %s -- etcdctl get %s: %v\n%s", etcdPod, etcdKey, err, out)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		t.Fatalf("etcdctl get %s returned no value; the Secret key path may be wrong", etcdKey)
	}
	return string(out)
}

// repoRoot walks up from the working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for d := wd; d != "/" && d != ""; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod found from %s", wd)
	return ""
}
