// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance case pinning the §25.8 Release Channel Response
// schema. The release channel is a wire contract between an independently
// operated service (releases.lenny.dev, or an operator's re-signed
// mirror) and every lenny-ops consumer, so the JSON field set is a
// conformance surface: a mirror that serves a different set of fields, or
// a build that renames one, breaks interoperability silently. §25.8 fixes
// the response as version, the per-component images and digests maps,
// minUpgradeFrom, schemaVersion, crdVersion, and releaseNotes.
//
// This case marshals the production releasechannel.Manifest and asserts
// its JSON carries exactly the §25.8 fields with the specified types, and
// that the canonical §25.8 example document round-trips through the
// struct with unknown fields disallowed, so the struct is the schema the
// consumer accepts.
//
// spec: §25.8 (Release Channel Response).
package tier10_conformance_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// specExampleManifest is the §25.8 Release Channel Response example,
// verbatim in field set (values abbreviated). It is the document a
// conformant channel serves.
const specExampleManifest = `{
  "version": "1.5.0",
  "images": {
    "gateway": "lenny-gateway:1.5.0",
    "ops": "lenny-ops:1.5.0",
    "controllers": "lenny-controllers:1.5.0",
    "backup": "lenny-backup:1.5.0"
  },
  "digests": {
    "gateway": "sha256:a1b2c3",
    "ops": "sha256:d4e5f6",
    "controllers": "sha256:g7h8i9",
    "backup": "sha256:j0k1l2"
  },
  "minUpgradeFrom": "1.3.0",
  "schemaVersion": 42,
  "crdVersion": "v1beta2",
  "releaseNotes": "https://github.com/lennylabs/lenny/releases/tag/v1.5.0"
}`

// TestReleaseChannelManifestFieldSet pins the §25.8 top-level field set
// and the component keys of the images and digests maps.
//
// diagnosis: a failure means the release-channel manifest the consumer
// and publisher exchange has drifted from the §25.8 Release Channel
// Response field set, so a conformant mirror and lenny-ops disagree on
// the wire contract.
//
// spec: 25.8 (Release Channel Response field set).
func TestReleaseChannelManifestFieldSet(t *testing.T) {
	m := releasechannel.Manifest{
		Version:        "1.5.0",
		Images:         map[string]string{"gateway": "g", "ops": "o", "controllers": "c", "backup": "b"},
		Digests:        map[string]string{"gateway": "gd", "ops": "od", "controllers": "cd", "backup": "bd"},
		MinUpgradeFrom: "1.3.0",
		SchemaVersion:  42,
		CRDVersion:     "v1beta2",
		ReleaseNotes:   "https://example.test/notes",
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	wantFields := []string{"crdVersion", "digests", "images", "minUpgradeFrom", "releaseNotes", "schemaVersion", "version"}
	got := make([]string, 0, len(generic))
	for k := range generic {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, wantFields) {
		t.Fatalf("manifest JSON fields = %v, want exactly %v", got, wantFields)
	}

	for _, mapField := range []string{"images", "digests"} {
		var comps map[string]string
		if err := json.Unmarshal(generic[mapField], &comps); err != nil {
			t.Fatalf("unmarshal %s: %v", mapField, err)
		}
		for _, comp := range []string{"gateway", "ops", "controllers", "backup"} {
			if _, ok := comps[comp]; !ok {
				t.Errorf("%s map missing §25.8 component %q", mapField, comp)
			}
		}
	}

	// schemaVersion is a JSON number, not a string.
	var schemaVersion int
	if err := json.Unmarshal(generic["schemaVersion"], &schemaVersion); err != nil {
		t.Errorf("schemaVersion is not a JSON number: %v", err)
	}
}

// TestReleaseChannelSpecExampleRoundTrips confirms the canonical §25.8
// example decodes into the Manifest struct with unknown fields
// disallowed, so the struct is exactly the schema the consumer accepts
// and the fields carry the spec's values.
//
// diagnosis: a failure means the production Manifest struct no longer
// accepts the canonical §25.8 example document, so lenny-ops would reject
// a manifest a conformant release channel serves.
//
// spec: 25.8 (Release Channel Response example).
func TestReleaseChannelSpecExampleRoundTrips(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader([]byte(specExampleManifest)))
	dec.DisallowUnknownFields()
	var m releasechannel.Manifest
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("§25.8 example manifest does not decode into Manifest: %v", err)
	}
	if m.Version != "1.5.0" {
		t.Errorf("Version = %q, want 1.5.0", m.Version)
	}
	if m.MinUpgradeFrom != "1.3.0" {
		t.Errorf("MinUpgradeFrom = %q, want 1.3.0", m.MinUpgradeFrom)
	}
	if m.SchemaVersion != 42 {
		t.Errorf("SchemaVersion = %d, want 42", m.SchemaVersion)
	}
	if m.CRDVersion != "v1beta2" {
		t.Errorf("CRDVersion = %q, want v1beta2", m.CRDVersion)
	}
	if m.Images["controllers"] != "lenny-controllers:1.5.0" {
		t.Errorf("Images[controllers] = %q, want lenny-controllers:1.5.0", m.Images["controllers"])
	}
	if m.Digests["backup"] != "sha256:j0k1l2" {
		t.Errorf("Digests[backup] = %q, want sha256:j0k1l2", m.Digests["backup"])
	}
}
