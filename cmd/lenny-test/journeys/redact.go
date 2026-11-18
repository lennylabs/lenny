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
// flag in either spelling), the value of a --token flag, a kubeconfig's
// token, client key, and client certificate, the token key of a Secret's
// data, the token variables the agent receives, a private key, from its
// BEGIN line to its END line or, when output was cut short, to the end
// of its base64 body, an Anthropic credential (an OAuth token,
// sk-ant-oat01-..., or an API key, sk-ant-api03-...), the credentials of
// an Authorization: Basic header (written with a colon or an equals sign,
// or as the first element of a header map's list), and the password of
// curl -u or --user user:password, unless it is all digits, as a uid:gid
// pair is. Each quote these patterns allow can carry any number of the
// backslashes of nested JSON escaping. The Anthropic form needs
// letters and then digits before the second hyphen, so a scenario's
// placeholder key, sk-ant-journey-<nonce>, does not match it. The values
// under credential keys such as password are removed by redactKeyed
// (redact_keys.go).
//
// Each pattern runs only when the text contains one of its hints, in any
// case, which keeps a long line without them to a cheap scan.
var secretPatterns = []struct {
	hints []string
	re    *regexp.Regexp
	with  string
}{
	{[]string{"bearer"}, regexp.MustCompile(`(?i)(bearer[\s=]+)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Redacted},
	{[]string{"basic"}, regexp.MustCompile(`(?i)(authorization(?:\\*["'])?[ \t]*[:=][ \t]*(?:\[[ \t]*)?(?:\\*["'])?[ \t]*basic[ \t]+)[A-Za-z0-9+/=._~-]{4,}`), "${1}" + Redacted},
	{[]string{"-u"}, regexp.MustCompile(`((?:^|[\s"'])(?:-u|--user)[ =]+(?:\\*["'])?[^\s:"'\\]+:)[^\s"'\\]*[^\s"'\\0-9][^\s"'\\]*`), "${1}" + Redacted},
	{[]string{"--token"}, regexp.MustCompile(`(--token[= ](?:\\*["'])?)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Redacted},
	{[]string{"token", "-data"}, regexp.MustCompile(`((?:client-key-data|client-certificate-data|token)(?:\\*["'])?[ \t]*:[ \t]*(?:\\*["'])?)[A-Za-z0-9._~+/=-]{8,}`), "${1}" + Redacted},
	{[]string{"_token="}, regexp.MustCompile(`((?:LENNY_API_TOKEN|CLAUDE_CODE_OAUTH_TOKEN)=)\S+`), "${1}" + Redacted},
	{[]string{"private key"}, regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), Redacted},
	{[]string{"private key"}, regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----(?:[A-Za-z0-9+/=\s]|\\+[rnt])*`), Redacted},
	{[]string{"sk-ant-"}, regexp.MustCompile(`sk-ant-[a-z]+[0-9]+-[A-Za-z0-9_-]{20,}`), Redacted},
}

// Redactor removes secrets from what the harness writes to a record:
// every exact value it was given, the base64 encoding of each, and every
// credential recognised by its form (redact_forms.go). Every record file
// is written through its methods in record.go, and SweepRecords checks
// the written records again at the end of a run.
type Redactor struct {
	values []string
	// public holds the values a scenario publishes, and their base64
	// encodings, which stay under a credential key (redact_keys.go).
	public map[string]bool
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

// Keep registers values a scenario publishes, which stay in the records
// when they appear, exactly, as the value of a credential key. Keep does
// not exempt a value from the known values or from the other forms.
func (r *Redactor) Keep(values ...string) {
	if r.public == nil {
		r.public = map[string]bool{}
	}
	for _, v := range values {
		if v != "" {
			r.public[v] = true
			r.public[base64.StdEncoding.EncodeToString([]byte(v))] = true
		}
	}
}

// Redact returns b with every known value replaced, then every
// credential recognised by its form. A nil redactor knows no values and
// still redacts by form, so a record written without one is not left in
// clear.
func (r *Redactor) Redact(b []byte) []byte {
	var public map[string]bool
	if r != nil {
		for _, v := range r.values {
			b = bytes.ReplaceAll(b, []byte(v), []byte(Redacted))
		}
		public = r.public
	}
	return redactByForm(b, formDepth, public)
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
