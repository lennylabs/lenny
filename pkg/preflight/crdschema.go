// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CRDSchemaVersionAnnotation is the `lenny.dev/schema-version` key
// stamped on every Lenny CRD's metadata.annotations. Controllers
// read it on startup and lenny-preflight reads it at install/upgrade
// time so a stale CRD never silently strips fields added by a newer
// gateway.
//
// spec: §10 line 437 ("`lenny.dev/schema-version` annotation on the
// CRD object") + line 443 ("preflight compares the installed CRD's
// `lenny.dev/schema-version` annotation against the version expected
// by the target chart release"). F-15.5.12.
const CRDSchemaVersionAnnotation = "lenny.dev/schema-version"

// CurrentCRDSchemaVersion is the schema revision the in-tree CRDs ship.
// Bumped together with breaking CRD changes; the controller binary
// embeds the value it expects and refuses to start when the installed
// CRDs are stale.
//
// spec: §10 line 437. F-15.5.12.
const CurrentCRDSchemaVersion = "1"

// LennyCRDNames lists every CRD the chart installs (and that the
// embedded stack mirrors). The schema-version check fans out across
// this set so a single missing or stale CRD is reported by name. The
// order is alphabetic so test output and operator-facing log lines
// stay stable.
//
// spec: §10 line 437 / §10 line 443. F-15.5.12.
var LennyCRDNames = []string{
	"runtimes.lenny.dev",
	"sandboxclaims.lenny.dev",
	"sandboxes.lenny.dev",
	"sandboxtemplates.lenny.dev",
	"sandboxwarmpools.lenny.dev",
}

// CRDSchemaVersionCheck runs the §10 line 443 preflight comparison:
// every CRD in Names is fetched, and the `lenny.dev/schema-version`
// annotation on it MUST equal Expected. A missing CRD, a missing
// annotation, or a mismatched value fails the check fail-closed so
// the upgrade aborts before the gateway rolls.
//
// spec: §10 line 443; §15.5 item 4. F-15.5.12.
type CRDSchemaVersionCheck struct {
	// Expected is the schema version the running binary embeds.
	Expected string
	// Names lists the CRDs to inspect. Empty falls back to the
	// chart-shipped LennyCRDNames so the lenny-preflight Job does not
	// need to pass them.
	Names []string
	// PostUpgrade selects the §17.6 "Helm post-upgrade CRD validation"
	// phrasing instead of the pre-upgrade preflight message. The
	// lenny-crd-validate Job (helm.sh/hook: post-upgrade) sets it so a
	// stale CRD surfaces the recovery command the operator runs, even
	// when preflight was skipped. F-17.6.4.
	PostUpgrade bool
	// Namespace is the release namespace interpolated into the
	// PostUpgrade recovery command. Empty renders the literal
	// "<namespace>" placeholder.
	Namespace string
}

// Decide reads the named CRDs through reader and reports the
// preflight outcome. Reader is the controller-runtime client the
// preflight Job already builds; the same shape is reused by the
// controllers' startup self-check.
//
// spec: §10 line 437 / §10 line 443. F-15.5.12.
func (c CRDSchemaVersionCheck) Decide(ctx context.Context, reader client.Reader) Decision {
	expected := strings.TrimSpace(c.Expected)
	if expected == "" {
		expected = CurrentCRDSchemaVersion
	}
	names := c.Names
	if len(names) == 0 {
		names = LennyCRDNames
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	var reasons []string
	for _, name := range sorted {
		var crd apiextensionsv1.CustomResourceDefinition
		if err := reader.Get(ctx, client.ObjectKey{Name: name}, &crd); err != nil {
			if apierrors.IsNotFound(err) {
				reasons = append(reasons, c.message(name, "", expected, "missing"))
				continue
			}
			reasons = append(reasons, fmt.Sprintf("get CRD %q: %v", name, err))
			continue
		}
		got := strings.TrimSpace(crd.Annotations[CRDSchemaVersionAnnotation])
		if got == "" {
			reasons = append(reasons, c.message(name, "", expected, "no-annotation"))
			continue
		}
		if got != expected {
			reasons = append(reasons, c.message(name, got, expected, "mismatch"))
		}
	}
	if len(reasons) == 0 {
		return Decision{Passed: true, Reason: fmt.Sprintf(
			"all %d Lenny CRDs match schema version %q", len(sorted), expected,
		)}
	}
	return Decision{Reason: strings.Join(reasons, "; ")}
}

// message renders the per-CRD failure reason. The pre-upgrade form is the
// §10 line 443 verbatim text operator runbooks grep for; the PostUpgrade
// form is the §17.6 "Helm post-upgrade CRD validation hook" message that
// names the recovery command. kind is one of "missing", "no-annotation",
// or "mismatch". F-15.5.12 / F-17.6.4.
func (c CRDSchemaVersionCheck) message(name, got, expected, kind string) string {
	if c.PostUpgrade {
		ns := strings.TrimSpace(c.Namespace)
		if ns == "" {
			ns = "<namespace>"
		}
		recover := fmt.Sprintf(
			"Run: kubectl apply -f charts/lenny/crds/ && kubectl rollout restart deployment -l app.kubernetes.io/part-of=lenny -n %s. See docs/runbooks/crd-upgrade.md.",
			ns,
		)
		switch kind {
		case "missing":
			return fmt.Sprintf("Post-upgrade CRD validation failed: CRD %q is missing. %s", name, recover)
		case "no-annotation":
			return fmt.Sprintf("Post-upgrade CRD validation failed: CRD %q is missing the %s annotation, expected %q. %s",
				name, CRDSchemaVersionAnnotation, expected, recover)
		default:
			// spec: §17.6 message verbatim — operator runbooks grep for it.
			return fmt.Sprintf("Post-upgrade CRD validation failed: CRD %q has schema version %q, expected %q. %s",
				name, got, expected, recover)
		}
	}
	switch kind {
	case "missing":
		return fmt.Sprintf("CRD %q is missing — apply updated CRDs before running helm upgrade", name)
	case "no-annotation":
		return fmt.Sprintf("CRD %q is missing the %s annotation — apply updated CRDs before running helm upgrade",
			name, CRDSchemaVersionAnnotation)
	default:
		// spec: §10 line 443 message verbatim — operator runbooks grep for it.
		return fmt.Sprintf("CRD %q schema version is %q; expected %q. Apply updated CRDs before running helm upgrade.",
			name, got, expected)
	}
}
