// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	metav1unstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/lennylabs/lenny/pkg/ops/doctor"
)

// dirHelmRenderSource is the production doctor.HelmRenderSource backing
// the §25.6 bootstrapConfigDrift and prometheusRuleMissing remediations.
// lenny-ops does not hold the chart, so the operator mounts the rendered
// references (the bootstrap ConfigMap and the monitoring PrometheusRule /
// ServiceMonitor manifests) into a directory threaded through chart
// values, and this source reads them from that directory at fix time.
//
// Layout under Dir:
//   - bootstrap-configmap.yaml: the rendered lenny-bootstrap ConfigMap.
//   - monitoring/*.yaml:        the rendered PrometheusRule / ServiceMonitor
//     manifests (one object per file or a multi-document stream).
//
// An empty Dir yields a nil source (both findings report not_detected).
//
// spec: §25.6 lines 2953, 2955. F-DR-1.
type dirHelmRenderSource struct {
	// dir is the operator-mounted render directory.
	dir string
	// namespace is the release namespace the rendered objects target.
	namespace string
}

// newHelmRenderSource returns a doctor.HelmRenderSource reading from dir,
// or nil when dir is empty so the two findings that depend on it report
// not_detected rather than a false success.
func newHelmRenderSource(dir, namespace string) doctor.HelmRenderSource {
	if dir == "" {
		return nil
	}
	return &dirHelmRenderSource{dir: dir, namespace: namespace}
}

const (
	bootstrapRenderFile    = "bootstrap-configmap.yaml"
	monitoringRenderSubdir = "monitoring"
)

// BootstrapConfigMap reads the rendered lenny-bootstrap ConfigMap. An
// absent file reports ok=false (the operator supplied no bootstrap
// render), so bootstrapConfigDrift is not detected. A present but
// unparseable file is an error so the run fails rather than silently
// reporting no drift.
func (s *dirHelmRenderSource) BootstrapConfigMap(ctx context.Context) (doctor.RenderedConfigMap, bool, error) {
	path := filepath.Join(s.dir, bootstrapRenderFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return doctor.RenderedConfigMap{}, false, nil
	}
	if err != nil {
		return doctor.RenderedConfigMap{}, false, fmt.Errorf("read bootstrap render %s: %w", path, err)
	}
	var obj metav1unstructured.Unstructured
	if err := yaml.Unmarshal(raw, &obj.Object); err != nil {
		return doctor.RenderedConfigMap{}, false, fmt.Errorf("parse bootstrap render %s: %w", path, err)
	}
	name := obj.GetName()
	if name == "" {
		return doctor.RenderedConfigMap{}, false, fmt.Errorf("bootstrap render %s has no metadata.name", path)
	}
	data, _, err := metav1unstructured.NestedStringMap(obj.Object, "data")
	if err != nil {
		return doctor.RenderedConfigMap{}, false, fmt.Errorf("read data from bootstrap render %s: %w", path, err)
	}
	return doctor.RenderedConfigMap{Name: name, Data: data}, true, nil
}

// Monitoring reads the rendered PrometheusRule / ServiceMonitor manifests
// from the monitoring subdirectory. An absent or empty subdirectory
// reports ok=false (monitoring not enabled in the release), so
// prometheusRuleMissing is not detected.
func (s *dirHelmRenderSource) Monitoring(ctx context.Context) (doctor.RenderedMonitoring, bool, error) {
	dir := filepath.Join(s.dir, monitoringRenderSubdir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return doctor.RenderedMonitoring{}, false, nil
	}
	if err != nil {
		return doctor.RenderedMonitoring{}, false, fmt.Errorf("read monitoring render dir %s: %w", dir, err)
	}
	var objs []doctor.RenderedObject
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return doctor.RenderedMonitoring{}, false, fmt.Errorf("read monitoring render %s: %w", path, err)
		}
		o, err := s.parseMonitoringObject(raw, path)
		if err != nil {
			return doctor.RenderedMonitoring{}, false, err
		}
		objs = append(objs, o)
	}
	if len(objs) == 0 {
		return doctor.RenderedMonitoring{}, false, nil
	}
	return doctor.RenderedMonitoring{Objects: objs}, true, nil
}

// parseMonitoringObject decodes one rendered monitoring manifest into a
// doctor.RenderedObject, resolving its group/version/resource from the
// apiVersion and kind so the remediator can address it via the dynamic
// client. The namespace defaults to the release namespace when the
// manifest does not carry one.
func (s *dirHelmRenderSource) parseMonitoringObject(raw []byte, path string) (doctor.RenderedObject, error) {
	var obj metav1unstructured.Unstructured
	if err := yaml.Unmarshal(raw, &obj.Object); err != nil {
		return doctor.RenderedObject{}, fmt.Errorf("parse monitoring render %s: %w", path, err)
	}
	gv := obj.GroupVersionKind()
	if gv.Kind == "" || gv.Version == "" {
		return doctor.RenderedObject{}, fmt.Errorf("monitoring render %s has no apiVersion/kind", path)
	}
	name := obj.GetName()
	if name == "" {
		return doctor.RenderedObject{}, fmt.Errorf("monitoring render %s has no metadata.name", path)
	}
	ns := obj.GetNamespace()
	if ns == "" {
		ns = s.namespace
	}
	return doctor.RenderedObject{
		Group:     gv.Group,
		Version:   gv.Version,
		Resource:  monitoringResourceForKind(gv.Kind),
		Namespace: ns,
		Name:      name,
		Manifest:  obj.Object,
	}, nil
}

// monitoringResourceForKind maps a monitoring kind to its lowercase
// plural resource for dynamic-client addressing. The Prometheus Operator
// kinds are the two the §25.6 monitoring bundle renders; an unknown kind
// falls back to a naive lowercased-plural so a future monitoring object
// is still addressable.
func monitoringResourceForKind(kind string) string {
	switch kind {
	case "PrometheusRule":
		return "prometheusrules"
	case "ServiceMonitor":
		return "servicemonitors"
	case "PodMonitor":
		return "podmonitors"
	default:
		return naivePlural(kind)
	}
}

// naivePlural lowercases a kind and appends the common English plural
// suffix, matching Kubernetes's default resource naming for kinds not in
// the explicit map above.
func naivePlural(kind string) string {
	lower := ""
	for _, r := range kind {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		lower += string(r)
	}
	switch {
	case len(lower) == 0:
		return lower
	case lower[len(lower)-1] == 's':
		return lower + "es"
	case lower[len(lower)-1] == 'y':
		return lower[:len(lower)-1] + "ies"
	default:
		return lower + "s"
	}
}
