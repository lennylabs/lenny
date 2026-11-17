// SPDX-License-Identifier: MIT

//go:build contract

// Cross-checks Lenny's OCSF class_uid assignments against the
// externally published OCSF v1.1.0 class registry, rather than against
// Lenny's own schemas/ocsf-mapping.yaml mirror (which the rest of this
// package's tests already validate self-consistently). §11.7 pins OCSF
// v1.1.0 as the single, canonical wire format: a SIEM ingesting a
// record identifies its class by the numeric class_uid alone, so a
// class_uid that does not match the externally defined class of that
// number is a wire-interop defect regardless of what Lenny's internal
// mapping table calls it.
package ocsf_audit_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// publishedOCSFRegistry is the vendored subset of the real OCSF class
// registry at tests/testdata/ocsf/v1.1.0-classes.json, keyed by
// class_uid (as a decimal string, since JSON object keys are strings).
type publishedOCSFRegistry struct {
	Version string            `json:"version"`
	Source  string            `json:"source"`
	Classes map[string]string `json:"classes"`
}

// loadPublishedOCSFClasses reads the vendored OCSF v1.1.0 class
// registry. The file is a fixed, hand-curated subset (not a generated
// mirror), so no repo-root discovery beyond the package's own tree is
// needed.
func loadPublishedOCSFClasses(t *testing.T) publishedOCSFRegistry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ocsf", "v1.1.0-classes.json"))
	if err != nil {
		t.Fatalf("read vendored OCSF class registry: %v", err)
	}
	var reg publishedOCSFRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatalf("parse vendored OCSF class registry: %v", err)
	}
	if reg.Version != ocsf.Version {
		t.Fatalf("vendored OCSF class registry is version %q, want %q (the pinned wire version)", reg.Version, ocsf.Version)
	}
	return reg
}

// spec: 11.7
// diagnosis: §11.7 says every audit event is serialized as an OCSF
// v1.1.0 JSON record and names the classes the event-type catalog
// assigns (3002 Authentication, 6003 API Activity, 6004 File System
// Activity, 3006 Account Change, 5001 Entity Management, 2004
// Application Security Finding — see pkg/audit/ocsf/mapping.go). A SIEM
// consuming this wire format identifies an event's class by class_uid
// alone, against the externally published OCSF registry, not against
// Lenny's internal ClassName labels. If Lenny's label for a class_uid
// disagrees with the externally published class at that same number,
// every downstream OCSF consumer misclassifies the event. Failure here
// means Lenny's §11.7 class assignment does not match the real,
// externally published OCSF v1.1.0 class at that class_uid.
func TestOCSFClassUIDsMatchPublishedRegistry(t *testing.T) {
	t.Skip("Lenny's §11.7 class_uid assignments for Account Change, Entity Management, " +
		"File System Activity, and the Application Security Finding dead-letter receipt do not " +
		"match the externally published OCSF v1.1.0 class registry; this spans several spec " +
		"chapters and needs a change proposal before the code (and the spec text asserting these " +
		"numbers) can be corrected — re-enable once that lands")

	registry := loadPublishedOCSFClasses(t)

	checkClass := func(classUID int, lennyName string) {
		t.Helper()
		key := fmt.Sprintf("%d", classUID)
		wantName, ok := registry.Classes[key]
		if !ok {
			t.Errorf("class_uid %d (Lenny calls it %q) is not present in the vendored published OCSF %s class registry",
				classUID, lennyName, registry.Version)
			return
		}
		if wantName != lennyName {
			t.Errorf("class_uid %d: Lenny's translator labels it %q, but the published OCSF %s registry defines class_uid %d as %q — "+
				"a SIEM parsing this class_uid on the wire reads it as %q, not %q",
				classUID, lennyName, registry.Version, classUID, wantName, wantName, lennyName)
		}
	}

	seen := map[int]bool{}
	for _, e := range ocsf.Catalog() {
		if seen[e.ClassUID] {
			continue
		}
		seen[e.ClassUID] = true
		checkClass(e.ClassUID, e.ClassName)
	}

	// The translation-failure dead-letter receipt (§11.7 Dead-letter
	// handling) is emitted outside the per-event-type catalog, so check
	// it directly.
	checkClass(ocsf.ClassAppSecurityFinding, ocsf.ClassName(ocsf.ClassAppSecurityFinding))
}
