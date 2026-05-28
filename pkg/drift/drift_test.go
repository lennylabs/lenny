// SPDX-License-Identifier: MIT

package drift_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/drift"
)

func changeByPath(changes []drift.Change, path string) (drift.Change, bool) {
	for _, c := range changes {
		if c.Path == path {
			return c, true
		}
	}
	return drift.Change{}, false
}

func TestDiffIdenticalStateHasNoDrift(t *testing.T) {
	state := map[string]any{"warmCount": float64(3), "image": "claude:v2"}
	if changes := drift.Diff(state, state); len(changes) != 0 {
		t.Errorf("Diff of identical state = %v, want no drift", changes)
	}
}

func TestDiffDetectsModifiedField(t *testing.T) {
	desired := map[string]any{"warmCount": float64(3)}
	actual := map[string]any{"warmCount": float64(5)}
	changes := drift.Diff(desired, actual)
	c, ok := changeByPath(changes, "warmCount")
	if !ok || c.Kind != drift.Modified {
		t.Fatalf("warmCount drift = %+v, want a modified change", c)
	}
	if c.Desired != float64(3) || c.Actual != float64(5) {
		t.Errorf("modified change values = %v/%v, want 3/5", c.Desired, c.Actual)
	}
}

func TestDiffDetectsRemovedAndAddedFields(t *testing.T) {
	desired := map[string]any{"runtimeName": "echo"}
	actual := map[string]any{"label": "manual"}
	changes := drift.Diff(desired, actual)
	if c, ok := changeByPath(changes, "runtimeName"); !ok || c.Kind != drift.Removed {
		t.Errorf("runtimeName drift = %+v, want removed", c)
	}
	if c, ok := changeByPath(changes, "label"); !ok || c.Kind != drift.Added {
		t.Errorf("label drift = %+v, want added", c)
	}
}

func TestDiffRecursesIntoNestedObjects(t *testing.T) {
	desired := map[string]any{
		"runtimeOptions": map[string]any{"isolationProfile": "gvisor", "warmCount": float64(2)},
	}
	actual := map[string]any{
		"runtimeOptions": map[string]any{"isolationProfile": "none", "warmCount": float64(2)},
	}
	changes := drift.Diff(desired, actual)
	c, ok := changeByPath(changes, "runtimeOptions.isolationProfile")
	if !ok || c.Kind != drift.Modified {
		t.Fatalf("nested drift = %+v, want runtimeOptions.isolationProfile modified", c)
	}
	if len(changes) != 1 {
		t.Errorf("nested diff produced %d changes, want only the changed leaf", len(changes))
	}
}

func TestClassifySeverity(t *testing.T) {
	cases := map[string]drift.Severity{
		"image":                           drift.SeverityHigh,
		"runtimeOptions.isolationProfile": drift.SeverityHigh,
		"securityContext":                 drift.SeverityHigh,
		"labels.team":                     drift.SeverityLow,
		"description":                     drift.SeverityLow,
		"warmCount":                       drift.SeverityMedium,
		"someUnrecognizedField":           drift.SeverityMedium,
	}
	for path, want := range cases {
		if got := drift.Classify(path); got != want {
			t.Errorf("Classify(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestClassifyQuotaScalingSpec25_10_3773 pins the §25.10 line 3773
// "scaling parameters and quota values are medium" rule for the common
// §6.x / §17.x field names operators actually drift. The implementation
// matched these by default-falling to medium; the explicit keyword set
// guards against future low-keyword substring matches that would
// downgrade them. F-25.10.11.
func TestClassifyQuotaScalingSpec25_10_3773(t *testing.T) {
	for _, path := range []string{
		"pool.scaling.replicas",
		"pool.scaling.minWarm",
		"pool.scaling.maxSize",
		"resourceQuota.requestsCPU",
		"resourceQuota.pods",
		"deployment.strategy.rollingUpdate.maxSurge",
		"deployment.strategy.rollingUpdate.maxUnavailable",
		"limits.memory",
		"warmCount",
	} {
		if got := drift.Classify(path); got != drift.SeverityMedium {
			t.Errorf("Classify(%q) = %q, want medium (§25.10 line 3773 scaling/quota)", path, got)
		}
	}
}

// TestClassifyDoesNotDowngradeLabelSelectorSpec25_10_3773 pins the §25.10
// line 3773 distinction between metadata bags (low) and structural
// configuration whose name happens to contain "label" (medium). Before
// F-25.10.11 a path like "pool.scaling.labelSelector" — structural
// config — was downgraded to low because the classifier substring-
// matched "label". The fix matches the low keyword as a whole segment.
func TestClassifyDoesNotDowngradeLabelSelectorSpec25_10_3773(t *testing.T) {
	for _, path := range []string{
		"pool.scaling.labelSelector",
		"pool.scaling.labelExpression",
		"deployment.spec.selector.matchLabelExpressions",
		"runtime.annotationFilter",
		"runtime.metadataValidator",
		"runtime.descriptionTemplate",
	} {
		if got := drift.Classify(path); got == drift.SeverityLow {
			t.Errorf("Classify(%q) = low, want medium (structural config, not metadata bag)", path)
		}
	}
}

// TestClassifyKeepsMetadataBagsLowSpec25_10_3773 confirms the §25.10
// metadata-bag whole-segment paths still classify low: a path with a
// "labels", "annotations", "description", or "metadata" segment is the
// low-severity case. F-25.10.11.
func TestClassifyKeepsMetadataBagsLowSpec25_10_3773(t *testing.T) {
	for _, path := range []string{
		"labels.team",
		"runtime.labels.owner",
		"runtime.annotations.checksum",
		"runtime.description",
		"runtime.metadata.uid",
		"deployment.spec.template.metadata.labels.app",
	} {
		if got := drift.Classify(path); got != drift.SeverityLow {
			t.Errorf("Classify(%q) = %q, want low (§25.10 metadata bag)", path, got)
		}
	}
}

func TestDiffSeverityIsAttachedToChanges(t *testing.T) {
	desired := map[string]any{"image": "claude:v1"}
	actual := map[string]any{"image": "claude:v2"}
	changes := drift.Diff(desired, actual)
	if len(changes) != 1 || changes[0].Severity != drift.SeverityHigh {
		t.Errorf("image drift severity = %v, want high", changes)
	}
}

func TestSnapshotStale(t *testing.T) {
	day := 86400
	if drift.SnapshotStale(3*day, 7) {
		t.Error("a 3-day-old snapshot is not stale against a 7-day threshold")
	}
	if !drift.SnapshotStale(10*day, 7) {
		t.Error("a 10-day-old snapshot is stale against a 7-day threshold")
	}
	if drift.SnapshotStale(100*day, 0) {
		t.Error("a zero threshold disables the staleness warning")
	}
}
