// SPDX-License-Identifier: MIT

package reinstall

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// hooksFixture mirrors `helm get hooks` output: a non-Job hook, a Job of
// another name, and the bootstrap Job with its hook annotations.
const hooksFixture = `---
# Source: lenny/templates/bootstrap-values.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: lenny-bootstrap-values
---
apiVersion: batch/v1
kind: Job
metadata:
  name: lenny-preflight
spec:
  template:
    spec:
      containers:
        - name: preflight
          image: lenny-preflight:e2e
---
apiVersion: batch/v1
kind: Job
metadata:
  name: lenny-bootstrap
  namespace: lenny-system
  labels:
    lenny.dev/component: bootstrap
  annotations:
    "helm.sh/hook": post-install,post-upgrade
    "helm.sh/hook-weight": "10"
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
    lenny.dev/note: kept
spec:
  backoffLimit: 3
  template:
    metadata:
      labels:
        lenny.dev/component: bootstrap
    spec:
      restartPolicy: Never
      containers:
        - name: bootstrap
          image: lenny-ctl:e2e
          args:
            - --api-url
            - http://lenny-gateway.lenny-system.svc:8080
            - bootstrap
            - --wait-timeout
            - "120"
            - --from-values
            - /etc/lenny/bootstrap-values.yaml
`

// TestForceUpdateBootstrapJobAddsFlagAndDropsHookAnnotations pins the
// one-off Job the reinstall runs: the same lenny-ctl bootstrap invocation
// with --force-update, an ordinary Job rather than a Helm hook, and no
// retries.
//
// spec: 17.6 (Packaging and Installation)
func TestForceUpdateBootstrapJobAddsFlagAndDropsHookAnnotations(t *testing.T) {
	b, err := ForceUpdateBootstrapJob(strings.NewReader(hooksFixture), "lenny-e2e-bootstrap-force-update")
	if err != nil {
		t.Fatalf("ForceUpdateBootstrapJob: %v", err)
	}
	var job batchv1.Job
	if err := json.Unmarshal(b, &job); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if job.Name != "lenny-e2e-bootstrap-force-update" || job.Namespace != "lenny-system" {
		t.Errorf("name/namespace = %s/%s", job.Namespace, job.Name)
	}
	if job.Kind != "Job" || job.APIVersion != "batch/v1" {
		t.Errorf("type = %s %s", job.APIVersion, job.Kind)
	}
	if want := map[string]string{"lenny.dev/note": "kept"}; !reflect.DeepEqual(job.Annotations, want) {
		t.Errorf("annotations = %v, want %v", job.Annotations, want)
	}
	if job.Labels["lenny.dev/component"] != "bootstrap" || job.Spec.Template.Labels["lenny.dev/component"] != "bootstrap" {
		t.Errorf("the bootstrap component label, which the NetworkPolicies select on, was not kept")
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("backoffLimit = %v, want 0", job.Spec.BackoffLimit)
	}
	args := job.Spec.Template.Spec.Containers[0].Args
	want := []string{
		"--api-url", "http://lenny-gateway.lenny-system.svc:8080", "bootstrap",
		"--wait-timeout", "120", "--from-values", "/etc/lenny/bootstrap-values.yaml", "--force-update",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
}

// TestForceUpdateBootstrapJobErrors pins the refusals: a missing bootstrap
// Job, a bootstrap container that runs no bootstrap subcommand, a missing
// container, a reused or empty name, and an already-present flag that is
// not duplicated.
//
// spec: 17.6 (Packaging and Installation)
func TestForceUpdateBootstrapJobErrors(t *testing.T) {
	cases := []struct {
		name, hooks, job, wantErr string
	}{
		{"no bootstrap job", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n", "copy", "no Job named"},
		{"empty stream", "", "copy", "no Job named"},
		{"reused name", hooksFixture, BootstrapJobName, "needs a name other than"},
		{"empty name", hooksFixture, "", "needs a name other than"},
		{
			"no subcommand",
			strings.Replace(hooksFixture, "            - bootstrap\n", "            - version\n", 1),
			"copy", "runs no bootstrap subcommand",
		},
		{
			"no bootstrap container",
			strings.Replace(hooksFixture, "- name: bootstrap", "- name: other", 1),
			"copy", "has no container named",
		},
		{"malformed yaml", "kind: [", "copy", "decode release hooks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ForceUpdateBootstrapJob(strings.NewReader(tc.hooks), tc.job)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}

	t.Run("flag already present", func(t *testing.T) {
		hooks := strings.Replace(hooksFixture, "            - bootstrap\n", "            - bootstrap\n            - --force-update\n", 1)
		b, err := ForceUpdateBootstrapJob(strings.NewReader(hooks), "copy")
		if err != nil {
			t.Fatalf("ForceUpdateBootstrapJob: %v", err)
		}
		if n := strings.Count(string(b), "--force-update"); n != 1 {
			t.Errorf("--force-update appears %d times, want 1", n)
		}
	})
}

func pod(name, rv, runtime, image, stateLabel, sandbox string, deleting bool) corev1.Pod {
	ctrl := true
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, ResourceVersion: rv,
			Labels: map[string]string{"lenny.dev/runtime": runtime, "lenny.dev/state": stateLabel},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "adapter", Image: "lenny-adapter:x"},
			{Name: "runtime", Image: image},
		}},
	}
	if sandbox != "" {
		p.OwnerReferences = []metav1.OwnerReference{{Kind: "Sandbox", Name: sandbox, Controller: &ctrl}}
	}
	if deleting {
		now := metav1.Now()
		p.DeletionTimestamp = &now
	}
	return p
}

// TestStaleIdlePodsSelectsOnlyIdleImageMismatches pins the pod selection
// the reinstall deletes from: an idle pod on an old image is returned, and
// a claimed, draining, warming, terminating, unowned, or current pod is
// not, nor is a pod whose Runtime CR is gone or that has no runtime
// container.
//
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 6.2 (Pod State Machine), 10.5 (Upgrade and Rollback Strategy)
func TestStaleIdlePodsSelectsOnlyIdleImageMismatches(t *testing.T) {
	const oldImg, newImg = "lenny-runtime-echo:src-old@sha256:aa", "lenny-runtime-echo:src-new@sha256:bb"
	noRuntimeContainer := pod("no-runtime-container", "9", "echo", oldImg, "idle", "no-runtime-container", false)
	noRuntimeContainer.Spec.Containers = noRuntimeContainer.Spec.Containers[:1]
	pods := &corev1.PodList{Items: []corev1.Pod{
		pod("stale-b", "2", "echo", oldImg, "idle", "stale-b", false),
		pod("stale-a", "1", "echo", oldImg, "idle", "stale-a", false),
		pod("current", "3", "echo", newImg, "idle", "current", false),
		pod("claimed", "4", "echo", oldImg, "active", "claimed", false),
		pod("label-lags", "5", "echo", oldImg, "idle", "label-lags", false),
		pod("terminating", "6", "echo", oldImg, "idle", "terminating", true),
		pod("unowned", "7", "echo", oldImg, "idle", "", false),
		pod("gone-runtime", "8", "removed", oldImg, "idle", "gone-runtime", false),
		noRuntimeContainer,
		pod("draining", "10", "echo", oldImg, "draining", "draining", false),
	}}
	runtimes := &v1alpha1.RuntimeList{Items: []v1alpha1.Runtime{
		{ObjectMeta: metav1.ObjectMeta{Name: "echo"}, Spec: v1alpha1.RuntimeSpec{Image: newImg}},
	}}
	phases := map[string]string{
		"stale-a": "idle", "stale-b": "idle", "current": "idle", "claimed": "claimed",
		"label-lags": "claimed", "terminating": "idle", "gone-runtime": "idle",
		"no-runtime-container": "idle", "draining": "draining",
	}
	sandboxes := &v1alpha1.SandboxList{}
	for name, ph := range phases {
		sb := v1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: name}}
		sb.Status.Phase = ph
		sandboxes.Items = append(sandboxes.Items, sb)
	}

	got := StaleIdlePods(pods, runtimes, sandboxes, nil)
	want := []StalePod{
		{Name: "stale-a", ResourceVersion: "1", Runtime: "echo", Image: oldImg, Want: newImg},
		{Name: "stale-b", ResourceVersion: "2", Runtime: "echo", Image: oldImg, Want: newImg},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StaleIdlePods = %+v, want %+v", got, want)
	}
}

// TestStaleIdlePodsEmptyInputs pins that empty lists select nothing.
//
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle))
func TestStaleIdlePodsEmptyInputs(t *testing.T) {
	if got := StaleIdlePods(&corev1.PodList{}, &v1alpha1.RuntimeList{}, &v1alpha1.SandboxList{}, PlatformImages{"adapter": "a:new"}); len(got) != 0 {
		t.Errorf("StaleIdlePods on empty lists = %+v, want none", got)
	}
}

// TestStaleWarmingPodsSelectsWarmingImageMismatches pins the warming-pod
// selection the reinstall waits on: a warming pod on an old image is
// returned, and an idle, current, or terminating one is not.
//
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 6.2 (Pod State Machine)
func TestStaleWarmingPodsSelectsWarmingImageMismatches(t *testing.T) {
	const oldImg, newImg = "img:old", "img:new"
	pods := &corev1.PodList{Items: []corev1.Pod{
		pod("warming-old", "1", "echo", oldImg, "", "warming-old", false),
		pod("warming-new", "2", "echo", newImg, "", "warming-new", false),
		pod("idle-old", "3", "echo", oldImg, "idle", "idle-old", false),
		pod("warming-terminating", "4", "echo", oldImg, "", "warming-terminating", true),
	}}
	runtimes := &v1alpha1.RuntimeList{Items: []v1alpha1.Runtime{
		{ObjectMeta: metav1.ObjectMeta{Name: "echo"}, Spec: v1alpha1.RuntimeSpec{Image: newImg}},
	}}
	sandboxes := &v1alpha1.SandboxList{}
	for name, ph := range map[string]string{
		"warming-old": "warming", "warming-new": "warming", "idle-old": "idle", "warming-terminating": "warming",
	} {
		sb := v1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: name}}
		sb.Status.Phase = ph
		sandboxes.Items = append(sandboxes.Items, sb)
	}
	got := StaleWarmingPods(pods, runtimes, sandboxes, nil)
	want := []StalePod{{Name: "warming-old", ResourceVersion: "1", Runtime: "echo", Image: oldImg, Want: newImg}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StaleWarmingPods = %+v, want %+v", got, want)
	}
}

// TestStalePodsDetectsPlatformContainerDrift pins that a pod whose runtime
// image is current is still rolled when a platform-injected container (the
// adapter sidecar or the supervisor stage init container) runs an image
// other than the one the install deploys, that the runtime drift is
// reported when both differ, that a pod with no container of a listed name
// (the embedded model, which has no adapter) is not compared on it, and
// that a pod whose platform containers are current is left alone. A
// reinstall that rebuilt only the adapter left every sidecar-mode idle pod
// on the previous adapter image before this comparison existed.
//
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 6.2 (Pod State Machine), 10.5 (Upgrade and Rollback Strategy)
func TestStalePodsDetectsPlatformContainerDrift(t *testing.T) {
	const rtImg, rtOld = "lenny-runtime-echo:src-new@sha256:bb", "lenny-runtime-echo:src-old@sha256:aa"
	const adOld, adNew = "lenny-adapter:x", "lenny-adapter:src-new"
	const supOld, supNew = "lenny-supervisor:src-old", "lenny-supervisor:src-new"

	withInit := func(p corev1.Pod, image string) corev1.Pod {
		p.Spec.InitContainers = []corev1.Container{{Name: "lenny-supervisor-stage", Image: image}}
		return p
	}
	current := pod("current", "1", "echo", rtImg, "idle", "current", false)
	current.Spec.Containers[0].Image = adNew
	current = withInit(current, supNew)
	supOnly := withInit(pod("sup-only", "6", "echo", rtImg, "idle", "sup-only", false), supOld)
	supOnly.Spec.Containers[0].Image = adNew
	embedded := pod("embedded", "4", "echo", rtImg, "idle", "embedded", false)
	embedded.Spec.Containers = embedded.Spec.Containers[1:]
	pods := &corev1.PodList{Items: []corev1.Pod{
		current,
		pod("adapter-old", "2", "echo", rtImg, "idle", "adapter-old", false),
		pod("both-old", "3", "echo", rtOld, "idle", "both-old", false),
		embedded,
		pod("adapter-old-claimed", "5", "echo", rtImg, "active", "adapter-old-claimed", false),
		supOnly,
		pod("adapter-old-warming", "7", "echo", rtImg, "", "adapter-old-warming", false),
	}}
	runtimes := &v1alpha1.RuntimeList{Items: []v1alpha1.Runtime{
		{ObjectMeta: metav1.ObjectMeta{Name: "echo"}, Spec: v1alpha1.RuntimeSpec{Image: rtImg}},
	}}
	sandboxes := &v1alpha1.SandboxList{}
	for name, ph := range map[string]string{
		"current": "idle", "adapter-old": "idle", "both-old": "idle", "embedded": "idle",
		"adapter-old-claimed": "claimed", "sup-only": "idle", "adapter-old-warming": "warming",
	} {
		sb := v1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: name}}
		sb.Status.Phase = ph
		sandboxes.Items = append(sandboxes.Items, sb)
	}
	platform := PlatformImages{"adapter": adNew, "lenny-supervisor-stage": supNew, "egress-capture": "lenny-egress-capture:src-new"}

	got := StaleIdlePods(pods, runtimes, sandboxes, platform)
	want := []StalePod{
		{Name: "adapter-old", ResourceVersion: "2", Runtime: "echo", Image: "adapter=" + adOld, Want: adNew},
		{Name: "both-old", ResourceVersion: "3", Runtime: "echo", Image: rtOld, Want: rtImg},
		{Name: "sup-only", ResourceVersion: "6", Runtime: "echo", Image: "lenny-supervisor-stage=" + supOld, Want: supNew},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StaleIdlePods = %+v, want %+v", got, want)
	}

	gotWarming := StaleWarmingPods(pods, runtimes, sandboxes, platform)
	wantWarming := []StalePod{{Name: "adapter-old-warming", ResourceVersion: "7", Runtime: "echo", Image: "adapter=" + adOld, Want: adNew}}
	if !reflect.DeepEqual(gotWarming, wantWarming) {
		t.Errorf("StaleWarmingPods = %+v, want %+v", gotWarming, wantWarming)
	}

	if got := StaleIdlePods(pods, runtimes, sandboxes, PlatformImages{"adapter": ""}); len(got) != 1 || got[0].Name != "both-old" {
		t.Errorf("an empty platform image must not be compared; StaleIdlePods = %+v, want only both-old", got)
	}
}
