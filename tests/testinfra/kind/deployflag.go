// SPDX-License-Identifier: MIT

package kind

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// flagOverrideAnnotationPrefix prefixes the Deployment annotation that
// records a flag's original argument while a test overrides it. The
// annotation is what lets a later run restore the original after a run that
// was killed before its t.Cleanup ran.
const flagOverrideAnnotationPrefix = "lenny.dev/e2e-override."

// flagAbsent is the annotation value recording that the flag had no argument
// on the Deployment before the override, so the restore removes it.
const flagAbsent = "<absent>"

// SetFlagArg returns args with the --flag=value argument set to value,
// replacing an existing --flag=... argument in place or appending one when
// none is present. It also returns the argument it replaced, or flagAbsent
// when the flag had no argument. The input slice is not modified.
func SetFlagArg(args []string, flag, value string) (out []string, previous string) {
	prefix := "--" + flag + "="
	out = make([]string, 0, len(args)+1)
	previous = flagAbsent
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			if previous == flagAbsent {
				previous = a
				out = append(out, prefix+value)
			}
			continue
		}
		out = append(out, a)
	}
	if previous == flagAbsent {
		out = append(out, prefix+value)
	}
	return out, previous
}

// RestoreFlagArg returns args with the --flag argument put back to original,
// an argument recorded by SetFlagArg. A flagAbsent original removes every
// --flag=... argument. The input slice is not modified.
func RestoreFlagArg(args []string, flag, original string) []string {
	prefix := "--" + flag + "="
	if original == flagAbsent {
		out := make([]string, 0, len(args))
		for _, a := range args {
			if !strings.HasPrefix(a, prefix) {
				out = append(out, a)
			}
		}
		return out
	}
	out, _ := SetFlagArg(args, flag, strings.TrimPrefix(original, prefix))
	return out
}

// deploymentContainer is the subset of a Deployment's first container the
// flag override reads.
type deploymentContainer struct {
	name        string
	args        []string
	annotations map[string]string
}

// readDeploymentContainer reads the first container's name and args and the
// Deployment's annotations.
func (c *Cluster) readDeploymentContainer(t testing.TB, namespace, deployment string) (deploymentContainer, error) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", namespace, "get", "deploy", deployment, "-o", "json")
	if err != nil {
		return deploymentContainer{}, fmt.Errorf("get deploy %s: %w\n%s", deployment, err, out)
	}
	var obj struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Name string   `json:"name"`
						Args []string `json:"args"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		return deploymentContainer{}, fmt.Errorf("decode deploy %s: %w", deployment, err)
	}
	cs := obj.Spec.Template.Spec.Containers
	if len(cs) == 0 {
		return deploymentContainer{}, fmt.Errorf("deploy %s has no containers", deployment)
	}
	return deploymentContainer{name: cs[0].Name, args: cs[0].Args, annotations: obj.Metadata.Annotations}, nil
}

// patchDeploymentArgs replaces the first container's args and sets the
// override annotation to annotation, or removes it when annotation is nil,
// in one strategic-merge patch, then waits for the rollout.
func (c *Cluster) patchDeploymentArgs(t testing.TB, namespace, deployment, container, flag string, args []string, annotation *string) error {
	t.Helper()
	var ann any
	if annotation != nil {
		ann = *annotation
	}
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": map[string]any{flagOverrideAnnotationPrefix + flag: ann}},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []map[string]any{{"name": container, "args": args}},
		}}},
	})
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	if out, err := c.KubectlOut(t, "-n", namespace, "patch", "deploy", deployment,
		"--type=strategic", "-p", string(patch)); err != nil {
		return fmt.Errorf("patch deploy %s: %w\n%s", deployment, err, out)
	}
	if out, err := c.KubectlOut(t, "-n", namespace, "rollout", "status", "deploy/"+deployment,
		"--timeout=180s"); err != nil {
		return fmt.Errorf("rollout of deploy %s: %w\n%s", deployment, err, out)
	}
	return nil
}

// RestoreDeploymentFlag puts back a flag an OverrideDeploymentFlag changed,
// reading the original from the override annotation. It is a no-op when the
// Deployment carries no override annotation for the flag. The override's
// t.Cleanup calls it, and OverrideDeploymentFlag calls it at start, so a run
// killed before its t.Cleanup does not leave the Deployment overridden for
// every later run.
func (c *Cluster) RestoreDeploymentFlag(t testing.TB, namespace, deployment, flag string) error {
	t.Helper()
	dc, err := c.readDeploymentContainer(t, namespace, deployment)
	if err != nil {
		return err
	}
	original, ok := dc.annotations[flagOverrideAnnotationPrefix+flag]
	if !ok {
		return nil
	}
	t.Logf("restoring --%s on deploy/%s to %q", flag, deployment, original)
	return c.patchDeploymentArgs(t, namespace, deployment, dc.name, flag,
		RestoreFlagArg(dc.args, flag, original), nil)
}

// OverrideDeploymentFlag sets --flag=value on the first container of a
// Deployment and waits for the rollout. It first restores any override an
// earlier killed run left, records the original argument in an annotation on
// the Deployment, and registers a t.Cleanup that restores it. It returns an
// error when the Deployment cannot be read or patched, so the caller decides
// whether that blocks the test.
func (c *Cluster) OverrideDeploymentFlag(t testing.TB, namespace, deployment, flag, value string) error {
	t.Helper()
	if err := c.RestoreDeploymentFlag(t, namespace, deployment, flag); err != nil {
		return fmt.Errorf("restore earlier override: %w", err)
	}
	dc, err := c.readDeploymentContainer(t, namespace, deployment)
	if err != nil {
		return err
	}
	args, previous := SetFlagArg(dc.args, flag, value)
	t.Cleanup(func() {
		if err := c.RestoreDeploymentFlag(t, namespace, deployment, flag); err != nil {
			t.Errorf("restore --%s on deploy/%s: %v", flag, deployment, err)
		}
	})
	return c.patchDeploymentArgs(t, namespace, deployment, dc.name, flag, args, &previous)
}
