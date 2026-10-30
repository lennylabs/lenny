// SPDX-License-Identifier: MIT

// Package reinstall holds the pure helpers the Kind install script uses
// when it upgrades an existing Lenny release, so that a reinstall that
// changes a runtime image leaves no runtime row and no idle warm pod on
// the previous image.
//
// Two mechanisms are involved. The §17.6 bootstrap Job the chart runs on
// every `helm upgrade` uses the skip-by-default upsert, so it leaves an
// existing runtime row unchanged when the seed names a new image. The
// specification makes `lenny-ctl bootstrap --force-update` the operator
// action that applies changed seed values, so ForceUpdateBootstrapJob
// derives a one-off Job from the release's rendered bootstrap hook with
// that flag added. The warm pool builds each new pod from the Runtime
// CR's spec.image but leaves a running idle pod alone when the CR image
// changes, because §10.5 routes a production runtime image change through
// the pool upgrade state machine rather than an in-place roll. The test
// cluster has no such upgrade to run, so StaleIdlePods names the idle
// pods whose runtime container runs an image other than the one their
// Runtime CR now names, or whose platform-injected container (the adapter
// sidecar, the supervisor stage init container, or the egress capture
// container) runs an image other than the one the install now renders
// into the controller, and the script deletes them so the pool replaces
// them from the current CR and the current controller flags.
//
// It is test infrastructure: no product path calls it.
//
// spec: §17.6 (Packaging and Installation), §4.6.1 (Warm Pool
// Controller (Pod Lifecycle)), §10.5 (Upgrade and Rollback Strategy).
package reinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// BootstrapJobName is the name the chart gives the §17.6 bootstrap hook
// Job (charts/lenny/templates/bootstrap-job.yaml).
const BootstrapJobName = "lenny-bootstrap"

// bootstrapContainer is the container of the bootstrap Job that runs
// lenny-ctl.
const bootstrapContainer = "bootstrap"

// forceUpdateFlag is the §17.6 lenny-ctl bootstrap flag that replaces an
// existing resource whose fields differ from the seed.
const forceUpdateFlag = "--force-update"

// ForceUpdateBootstrapJob reads the multi-document YAML that `helm get
// hooks` prints for a release, finds the bootstrap Job, and returns a JSON
// manifest of a copy named name that runs the same lenny-ctl invocation
// with --force-update appended. The copy drops every helm.sh/ annotation
// so it is an ordinary Job rather than a hook, and sets backoffLimit to 0
// so a failed run fails at once with its own log rather than retrying a
// seed that is rejected deterministically.
func ForceUpdateBootstrapJob(hooks io.Reader, name string) ([]byte, error) {
	if name == "" || name == BootstrapJobName {
		return nil, fmt.Errorf("the force-update Job needs a name other than %q", BootstrapJobName)
	}
	job, err := findBootstrapJob(hooks)
	if err != nil {
		return nil, err
	}
	out := batchv1.Job{
		TypeMeta: job.TypeMeta,
		ObjectMeta: metaWithoutHelmHooks(job.ObjectMeta.Labels,
			job.ObjectMeta.Annotations, name, job.ObjectMeta.Namespace),
		Spec: job.Spec,
	}
	var zero int32
	out.Spec.BackoffLimit = &zero
	if err := appendForceUpdate(&out.Spec.Template.Spec); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// findBootstrapJob decodes each document of the hooks stream and returns
// the bootstrap Job. Documents of other kinds are skipped.
func findBootstrapJob(hooks io.Reader) (*batchv1.Job, error) {
	dec := utilyaml.NewYAMLOrJSONDecoder(hooks, 4096)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("no Job named %q in the release hooks", BootstrapJobName)
			}
			return nil, fmt.Errorf("decode release hooks: %w", err)
		}
		if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
			continue
		}
		var job batchv1.Job
		if err := json.Unmarshal(raw, &job); err != nil {
			return nil, fmt.Errorf("decode release hook document: %w", err)
		}
		if job.Kind == "Job" && job.Name == BootstrapJobName {
			return &job, nil
		}
	}
}

// metaWithoutHelmHooks builds the copy's metadata: the original labels,
// the original annotations minus every helm.sh/ key, the new name, and
// the original namespace. Server-populated fields are not carried.
func metaWithoutHelmHooks(labels, annotations map[string]string, name, namespace string) metav1.ObjectMeta {
	meta := metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}
	for k, v := range annotations {
		if strings.HasPrefix(k, "helm.sh/") {
			continue
		}
		if meta.Annotations == nil {
			meta.Annotations = map[string]string{}
		}
		meta.Annotations[k] = v
	}
	return meta
}

// appendForceUpdate appends --force-update to the bootstrap container's
// arguments. The lenny-ctl bootstrap parser reads its flags in any order
// after the subcommand, so appending is equivalent to inserting it after
// `bootstrap`. A container whose arguments carry no bootstrap subcommand
// is an error, because the flag would then reach the wrong command.
func appendForceUpdate(pod *corev1.PodSpec) error {
	for i := range pod.Containers {
		c := &pod.Containers[i]
		if c.Name != bootstrapContainer {
			continue
		}
		sub := false
		for _, a := range c.Args {
			if a == forceUpdateFlag {
				return nil
			}
			if a == "bootstrap" {
				sub = true
			}
		}
		if !sub {
			return fmt.Errorf("container %q of Job %q runs no bootstrap subcommand", bootstrapContainer, BootstrapJobName)
		}
		c.Args = append(c.Args, forceUpdateFlag)
		return nil
	}
	return fmt.Errorf("Job %q has no container named %q", BootstrapJobName, bootstrapContainer)
}

// StalePod is a warm pod whose runtime container image differs from the
// image its Runtime CR names, or whose platform-injected container image
// differs from the image the install deploys for that container.
type StalePod struct {
	// Name is the pod name.
	Name string
	// ResourceVersion is the pod's resourceVersion when it was listed. The
	// script deletes the pod with it as a precondition, so a pod claimed
	// after the listing (its state label changes, which bumps the version)
	// is not deleted.
	ResourceVersion string
	// Runtime is the Runtime CR the pod was built from.
	Runtime string
	// Image is the stale image the pod runs. For a runtime container it is
	// the bare image reference; for a platform-injected container it is
	// prefixed with the container name and "=", as in
	// "adapter=lenny-adapter:src-old".
	Image string
	// Want is the image the pod should run: the Runtime CR image for the
	// runtime container, or the deployed image for a platform-injected one.
	Want string
}

// PlatformImages maps the name of a platform-injected pod container (an
// ordinary container or an init container) to the image the install
// renders into the controller for it, such as
// {"adapter": "lenny-adapter:src-new"}. A pod that has no container of a
// listed name is not compared on that name, so the embedded deployment
// model, which runs no adapter sidecar, is unaffected by an adapter entry.
type PlatformImages map[string]string

// StaleIdlePods returns the pods that are idle and run a runtime image
// other than the one their Runtime CR names, or a platform-injected
// container image other than the one platform names, sorted by name.
// When both differ the runtime image is reported. A pod is idle
// only when its lenny.dev/state label is idle, it carries no deletion
// timestamp, and the Sandbox that owns it reports the §6.2 idle phase; a
// claimed, reserved, draining, or warming pod is never returned. A pod
// whose Runtime CR no longer exists, or that has no runtime container, is
// left alone because there is no current image to compare against.
func StaleIdlePods(pods *corev1.PodList, runtimes *v1alpha1.RuntimeList, sandboxes *v1alpha1.SandboxList, platform PlatformImages) []StalePod {
	return stalePods(pods, runtimes, sandboxes, platform, func(p *corev1.Pod, phase string) bool {
		return p.Labels[state.LabelState] == state.CoarseIdle && phase == string(state.Idle)
	})
}

// StaleWarmingPods returns the pods whose owning Sandbox is in the §6.2
// warming phase and whose runtime container runs an image other than the
// one their Runtime CR names, or whose platform-injected container runs an
// image other than the one platform names, sorted by name. Such a pod was created from
// the Runtime CR before the install applied the new image. It is not
// deleted while it warms; the script waits for it to reach idle and then
// rolls it with the idle pods.
func StaleWarmingPods(pods *corev1.PodList, runtimes *v1alpha1.RuntimeList, sandboxes *v1alpha1.SandboxList, platform PlatformImages) []StalePod {
	return stalePods(pods, runtimes, sandboxes, platform, func(_ *corev1.Pod, phase string) bool {
		return phase == string(state.Warming)
	})
}

// stalePods returns the pods that carry no deletion timestamp, whose
// owning Sandbox phase satisfies match, and whose runtime image differs
// from their Runtime CR's image or whose platform-injected container image
// differs from platform, sorted by name.
func stalePods(pods *corev1.PodList, runtimes *v1alpha1.RuntimeList, sandboxes *v1alpha1.SandboxList,
	platform PlatformImages, match func(p *corev1.Pod, sandboxPhase string) bool,
) []StalePod {
	want := make(map[string]string, len(runtimes.Items))
	for _, rt := range runtimes.Items {
		want[rt.Name] = rt.Spec.Image
	}
	phase := make(map[string]string, len(sandboxes.Items))
	for _, sb := range sandboxes.Items {
		phase[sb.Name] = sb.Status.Phase
	}
	var out []StalePod
	for i := range pods.Items {
		p := &pods.Items[i]
		sb, owned := owningSandbox(p)
		if !owned || p.DeletionTimestamp != nil || !match(p, phase[sb]) {
			continue
		}
		rt := p.Labels[state.LabelRuntime]
		image, ok := want[rt]
		if !ok || image == "" {
			continue
		}
		got, ok := runtimeImage(p)
		if !ok {
			continue
		}
		if got == image {
			got, image, ok = platformDrift(p, platform)
			if !ok {
				continue
			}
		}
		out = append(out, StalePod{
			Name: p.Name, ResourceVersion: p.ResourceVersion,
			Runtime: rt, Image: got, Want: image,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// owningSandbox returns the name of the Sandbox that controls the pod.
// The pod state label lags the Sandbox phase by one reconcile, so the
// idle selection reads both.
func owningSandbox(p *corev1.Pod) (string, bool) {
	for _, ref := range p.OwnerReferences {
		if ref.Kind == "Sandbox" && ref.Controller != nil && *ref.Controller {
			return ref.Name, true
		}
	}
	return "", false
}

// runtimeImage returns the image of the pod's runtime container, which
// carries the Runtime CR image in both the sidecar and the embedded
// deployment models.
func runtimeImage(p *corev1.Pod) (string, bool) {
	for _, c := range p.Spec.Containers {
		if c.Name == podspec.RuntimeContainerName {
			return c.Image, true
		}
	}
	return "", false
}

// platformDrift returns the first platform-injected container of the pod,
// in container-name order, whose image differs from the one platform
// names. The stale image is prefixed with the container name. It reports
// false when every listed container the pod has runs its deployed image.
func platformDrift(p *corev1.Pod, platform PlatformImages) (string, string, bool) {
	names := make([]string, 0, len(platform))
	for name, want := range platform {
		if want != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		for _, list := range [][]corev1.Container{p.Spec.InitContainers, p.Spec.Containers} {
			for _, c := range list {
				if c.Name == name && c.Image != platform[name] {
					return name + "=" + c.Image, platform[name], true
				}
			}
		}
	}
	return "", "", false
}
