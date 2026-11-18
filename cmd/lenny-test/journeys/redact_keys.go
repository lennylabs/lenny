// SPDX-License-Identifier: MIT

package journeys

import "regexp"

// Redaction by key removes the value written under a credential key, in
// any case:
//
//   - a key whose name ends in password, passwd, secret-key, api-key,
//     access-key, or master-key, with a hyphen, an underscore, or nothing
//     between the words (adminPassword, POSTGRES_PASSWORD, apiKey,
//     API_KEY, minio-secret-key, master-key);
//   - the key secret or client-secret on its own. A longer name that ends
//     in secret, such as existingSecret or rootCredentialsSecret, names a
//     Kubernetes Secret object in the chart's values rather than holding a
//     credential, so it stays.
//
// A credential such as a data-store password or a key the chart
// generates has no form of its own, so its key is the only sign of it.
// The value is removed in these spellings:
//
//   - YAML and JSON, quoted or not, JSON-escaped included:
//     password: v, "apiKey": "v", \"secret\": \"v\";
//   - an environment variable or a flag with an equals sign:
//     MINIO_SECRET_KEY=v, --password=v, --from-literal=apiKey=v;
//   - a flag and its value: --password v. A type name that command help
//     prints after a flag (--password string) stays.
//
// A value that refers to a credential rather than holding one stays: a
// shell variable or substitution ($VAR, ${VAR}, $(cmd)), a placeholder in
// angle brackets, and a value already redacted. A value of fewer than
// four characters stays, so prose such as "secret: no" is not touched.
// The password inside a connection string (postgres://user:pass@host) has
// no key and stays, so the scenarios' published data-store passwords
// stay readable there. A documentation example under such a key, such as
// accessKey: minioadmin, is removed like any other value.
//
// A value a scenario publishes in its own text (Scenario.PublicValues),
// and the base64 encoding of one, stays under these keys, so a reviewer
// can see the agent use the published placeholder. The comparison is on
// the exact value.

const suffixKeyWords = `(?:password|passwd|secret[-_]?key|api[-_]?key|access[-_]?key|master[-_]?key)`

// keyedValue is a value under such a key: it does not start with a
// character that opens a reference or a redaction, and it ends at white
// space, a quote, a backslash, or a delimiter.
const keyedValue = `([^\s"'\\$<\[{*(,;}\]][^\s"'\\,;}\]]{3,})`

// keyName is a credential key. The bare secret key must not continue an
// identifier, so it starts the text, follows a character outside one, or
// follows the two hyphens of a flag.
const keyName = `(?:[A-Za-z0-9_.-]*` + suffixKeyWords + `|(?:^|[^A-Za-z0-9_.-]|--)(?:client[-_]?)?secret)`

var (
	keyedAssignment = regexp.MustCompile(`(?i)(` + keyName + `(?:\\?["'])?\s*[:=]\s*(?:\\?["'])?)` + keyedValue)
	keyedFlag       = regexp.MustCompile(`(?i)(--(?:[A-Za-z0-9_.-]*` + suffixKeyWords + `|(?:client-)?secret)\s+(?:\\?["'])?)` + keyedValue)
)

// helpTypes are the type names command help prints after a flag.
var helpTypes = map[string]bool{"string": true, "strings": true, "stringArray": true, "stringSlice": true, "int": true, "bool": true, "duration": true, "file": true, "path": true}

// redactKeyed removes the value under each credential key in b, except a
// value public lists.
func redactKeyed(b []byte, public map[string]bool) []byte {
	b = replaceKeyed(b, keyedAssignment, public, nil)
	return replaceKeyed(b, keyedFlag, public, helpTypes)
}

func replaceKeyed(b []byte, re *regexp.Regexp, public, keep map[string]bool) []byte {
	return re.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := re.FindSubmatchIndex(m)
		key, value := m[:sub[3]], string(m[sub[4]:sub[5]])
		if public[value] || keep[value] {
			return m
		}
		return append(append([]byte{}, key...), Redacted...)
	})
}
