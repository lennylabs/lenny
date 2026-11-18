// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"encoding/base64"
	"os"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// Redacted replaces a secret in a record.
const Redacted = "[REDACTED]"

// minSecretLen keeps short strings out of the exact-value list, so a
// one-character value cannot blank out unrelated text.
const minSecretLen = 8

// secretPatterns match secrets by their form wherever they appear, in
// plain text or inside the JSON strings of a stream-json transcript: a
// bearer token (which covers an Authorization header and a --bearer
// flag), the value of a --token flag, a kubeconfig's token, client key,
// and client certificate, the token key of a Secret's data, the token
// variables the agent receives, a private key, and an Anthropic
// credential (an OAuth token, sk-ant-oat01-..., or an API key,
// sk-ant-api03-...). The Anthropic form needs letters and then digits
// before the second hyphen, so a scenario's placeholder key,
// sk-ant-journey-<nonce>, does not match it.
var secretPatterns = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Redacted},
	{regexp.MustCompile(`(--token[= ](?:\\?["'])?)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Redacted},
	{regexp.MustCompile(`((?:client-key-data|client-certificate-data|token)(?:\\"|")?\s*:\s*(?:\\"|")?)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Redacted},
	{regexp.MustCompile(`((?:LENNY_API_TOKEN|CLAUDE_CODE_OAUTH_TOKEN)=)\S+`), "${1}" + Redacted},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), Redacted},
	{regexp.MustCompile(`sk-ant-[a-z]+[0-9]+-[A-Za-z0-9_-]{20,}`), Redacted},
}

// Redactor removes secrets from what the harness writes to a record:
// every exact value it was given, the base64 encoding of each, and every
// credential recognised by its form (redact_forms.go). Every record file
// is written through its methods in record.go, so no record reaches the
// disk unredacted.
type Redactor struct {
	values []string
}

// NewRedactor returns a redactor for the given secret values. Values
// shorter than eight characters are ignored.
func NewRedactor(values ...string) *Redactor {
	r := &Redactor{}
	r.Add(values...)
	return r
}

// Add registers more secret values.
func (r *Redactor) Add(values ...string) {
	for _, v := range values {
		if len(v) < minSecretLen {
			continue
		}
		r.values = append(r.values, v, base64.StdEncoding.EncodeToString([]byte(v)))
	}
	// Longest first, so a value that contains another is replaced whole.
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
}

// Redact returns b with every known value replaced, then every
// credential recognised by its form. A nil redactor knows no values and
// still redacts by form, so a record written without one is not left in
// clear.
func (r *Redactor) Redact(b []byte) []byte {
	if r != nil {
		for _, v := range r.values {
			b = bytes.ReplaceAll(b, []byte(v), []byte(Redacted))
		}
	}
	return redactByForm(b, formDepth)
}

// String redacts a string.
func (r *Redactor) String(s string) string { return string(r.Redact([]byte(s))) }

// KubeconfigSecrets returns the token, client key, and client certificate
// values of every user in the kubeconfig at path. A missing or unreadable
// file has none.
func KubeconfigSecrets(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return KubeconfigSecretsFrom(b)
}

// KubeconfigSecretsFrom returns the secret values of a kubeconfig's
// content, as KubeconfigSecrets does for a file.
func KubeconfigSecretsFrom(b []byte) []string {
	var doc struct {
		Users []struct {
			User map[string]any `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil
	}
	var out []string
	for _, u := range doc.Users {
		for _, k := range []string{"token", "client-key-data", "client-certificate-data", "password"} {
			if v, ok := u.User[k].(string); ok {
				out = append(out, v)
				if raw, err := base64.StdEncoding.DecodeString(v); err == nil {
					out = append(out, string(raw))
				}
			}
		}
	}
	return out
}
