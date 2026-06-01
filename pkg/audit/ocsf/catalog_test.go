// SPDX-License-Identifier: MIT

package ocsf_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/pkg/audit/ocsf"
)

// repoRel resolves a path relative to the repository root from the
// pkg/audit/ocsf test working directory (three levels deep).
func repoRel(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", ".."}, parts...)...)
}

// TestMappingYAMLInSync is the §11.7 line 414 CI guard: the committed
// schemas/ocsf-mapping.yaml must equal the generator's output. A
// catalog change in pkg/audit/ocsf/mapping.go that is not regenerated
// fails here.
// spec: 11_security-trust-model.md line 414.
func TestMappingYAMLInSync(t *testing.T) {
	want, err := ocsf.MarshalMappingYAML()
	if err != nil {
		t.Fatalf("MarshalMappingYAML: %v", err)
	}
	got, err := os.ReadFile(repoRel("schemas", "ocsf-mapping.yaml"))
	if err != nil {
		t.Fatalf("read committed mapping: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("schemas/ocsf-mapping.yaml is stale; run `go run ./cmd/lenny-ocsf-mapping-gen`")
	}
}

// TestMappingYAMLRoundTrips confirms the generated YAML deserializes
// back into the same catalog the Go source produces — every event type
// the translator can resolve appears in the mirror.
// spec: 11_security-trust-model.md line 414.
func TestMappingYAMLRoundTrips(t *testing.T) {
	data, err := ocsf.MarshalMappingYAML()
	if err != nil {
		t.Fatalf("MarshalMappingYAML: %v", err)
	}
	var doc struct {
		SchemaVersion string              `yaml:"schema_version"`
		Mappings      []ocsf.CatalogEntry `yaml:"mappings"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal generated yaml: %v", err)
	}
	if doc.SchemaVersion != ocsf.MappingSchemaVersion {
		t.Errorf("schema_version = %q, want %q", doc.SchemaVersion, ocsf.MappingSchemaVersion)
	}
	want := ocsf.Catalog()
	if len(doc.Mappings) != len(want) {
		t.Fatalf("mapping count = %d, want %d", len(doc.Mappings), len(want))
	}
	for i := range want {
		if doc.Mappings[i] != want[i] {
			t.Errorf("mapping[%d] = %+v, want %+v", i, doc.Mappings[i], want[i])
		}
	}
}

// TestMappingYAMLResolvesEveryExactEvent confirms each exact-match row
// in the mirror agrees with the live LookupClass result, so an external
// verifier reading only the YAML reaches the same class/activity the
// translator would.
// spec: 11_security-trust-model.md line 414.
func TestMappingYAMLResolvesEveryExactEvent(t *testing.T) {
	for _, e := range ocsf.Catalog() {
		if e.Match != "exact" {
			continue
		}
		m, ok := ocsf.LookupClass(e.EventType)
		if !ok {
			t.Errorf("%s: LookupClass returned not-found", e.EventType)
			continue
		}
		if m.ClassUID != e.ClassUID || m.ActivityID != e.ActivityID || m.CategoryUID != e.CategoryUID {
			t.Errorf("%s: mirror=(%d/%d/%d) lookup=(%d/%d/%d)",
				e.EventType, e.ClassUID, e.CategoryUID, e.ActivityID,
				m.ClassUID, m.CategoryUID, m.ActivityID)
		}
	}
}

// TestActivityNameDisambiguatesLogon checks the Authentication-class
// overlap: activity id 1 is "Logon" under class 3002 and "Create"
// elsewhere.
func TestActivityNameDisambiguatesLogon(t *testing.T) {
	if got := ocsf.ActivityName(ocsf.ClassAuthentication, ocsf.ActivityLogon); got != "Logon" {
		t.Errorf("authn activity 1 = %q, want Logon", got)
	}
	if got := ocsf.ActivityName(ocsf.ClassEntityManagement, ocsf.ActivityCreate); got != "Create" {
		t.Errorf("entity-mgmt activity 1 = %q, want Create", got)
	}
	if got := ocsf.ActivityName(ocsf.ClassAccountChange, ocsf.ActivityDisable); got != "Disable" {
		t.Errorf("account-change activity 5 = %q, want Disable", got)
	}
}

// TestAuditEventsRegistryV1 validates the §11.7 line 365
// schemas/audit-events/v1.json registry: it parses, declares JSON
// Schema 2020-12, and pins event_schema_version to the version named in
// its filename.
// spec: 11_security-trust-model.md line 365.
func TestAuditEventsRegistryV1(t *testing.T) {
	data, err := os.ReadFile(repoRel("schemas", "audit-events", "v1.json"))
	if err != nil {
		t.Fatalf("read audit-events/v1.json: %v", err)
	}
	var doc struct {
		Schema     string `json:"$schema"`
		Properties struct {
			EventSchemaVersion struct {
				Const string `json:"const"`
			} `json:"event_schema_version"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("audit-events/v1.json is not valid JSON: %v", err)
	}
	if doc.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %q, want draft 2020-12", doc.Schema)
	}
	if doc.Properties.EventSchemaVersion.Const != ocsf.MappingSchemaVersion {
		t.Errorf("event_schema_version const = %q, want %q",
			doc.Properties.EventSchemaVersion.Const, ocsf.MappingSchemaVersion)
	}
	wantRequired := map[string]bool{"tenant_id": false, "sequence_number": false, "event_type": false, "event_schema_version": false}
	for _, r := range doc.Required {
		if _, ok := wantRequired[r]; ok {
			wantRequired[r] = true
		}
	}
	for field, present := range wantRequired {
		if !present {
			t.Errorf("required is missing the chain-critical field %q", field)
		}
	}
}
