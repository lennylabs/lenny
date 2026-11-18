// SPDX-License-Identifier: MIT

package journeys

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestRedactorRemovesValuesUnderCredentialKeys(t *testing.T) {
	masterKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("Kq7z", 16)))
	cases := map[string]string{
		"password: hunter2-db":                                   "password: " + Redacted,
		"data:\n  postgres-password: @V@\n":                      "data:\n  postgres-password: " + Redacted + "\n",
		"type: Opaque\ndata:\n  master-key: " + masterKey + "\n": "type: Opaque\ndata:\n  master-key: " + Redacted + "\n",
		`{"apiKey": "@V@"}`:                                      `{"apiKey": "` + Redacted + `"}`,
		`{"content":"{\"secret\": \"s3cr3t-value\"}"}`:           `{"content":"{\"secret\": \"` + Redacted + `\"}"}`,
		"MINIO_SECRET_KEY=@V@ helm install":                      "MINIO_SECRET_KEY=" + Redacted + " helm install",
		"export POSTGRES_PASSWORD='pg-pass-9876'":                "export POSTGRES_PASSWORD='" + Redacted + "'",
		"API_KEY=@V@":                                    "API_KEY=" + Redacted,
		"api_key: @V@":                                   "api_key: " + Redacted,
		"accessKey: lennyminio-other":                    "accessKey: " + Redacted,
		"lenny-ctl login --password=flag-pass-1":         "lenny-ctl login --password=" + Redacted,
		"mc alias set s3 https://minio --access-key @V@": "mc alias set s3 https://minio --access-key " + Redacted,
		"kubectl create secret generic kms --from-literal=master-key=@V@": "kubectl create secret generic kms --from-literal=master-key=" + Redacted,
		`@CURL@ -H "Authorization: Basic @V@" https://gw`:                 `@CURL@ -H "Authorization: Basic ` + Redacted + `" https://gw`,
		`{"command":"@CURL@ -H \"authorization: basic @V@\" https://gw"}`: `{"command":"@CURL@ -H \"authorization: basic ` + Redacted + `\" https://gw"}`,
		"secret: @V@":                         "secret: " + Redacted,
		"client_secret: abcd1234":             "client_secret: " + Redacted,
		"oidc login --client-secret abcd1234": "oidc login --client-secret " + Redacted,
		// A documentation example is removed as well; the pattern cannot
		// tell it from a credential.
		`--from-literal=apiKey="sk-ant-..."`: `--from-literal=apiKey="` + Redacted + `"`,
		"accessKey: minioadmin":              "accessKey: " + Redacted,
		// A published value stays only when the redactor is told about it.
		"password: lenny": "password: " + Redacted,
	}
	r := NewRedactor()
	for in, want := range cases {
		if got := r.String(fill(in)); got != fill(want) {
			t.Errorf("redact %q\n got %q\nwant %q", fill(in), got, fill(want))
		}
	}
}

// The operator-install scenario publishes its data-store passwords, and
// tenant-admin-onboarding its placeholder key, in their text
// (Scenario.PublicValues). They stay under a credential key, in clear
// or in base64, and connection-string passwords stay whatever the
// scenario publishes.
func TestRedactorKeepsPublishedValuesReferencesAndConnectionStrings(t *testing.T) {
	r := NewRedactor()
	r.Keep("lenny", "lennyminio", "lennyminio123", "")
	kept := []string{
		"password: lenny",
		`{"password":"lenny","user":"lenny"}`,
		`{"content":"{\"secretKey\": \"lennyminio123\"}"}`,
		"MINIO_SECRET_KEY=lennyminio123",
		"accessKey: lennyminio",
		"data:\n  password: " + base64.StdEncoding.EncodeToString([]byte("lenny")) + "\n",
		"dsn: postgres://lenny:lenny@lenny-postgres.lenny-system.svc:5432/lenny?sslmode=disable",
		"endpoint: https://lennyminio:lennyminio123@lenny-minio.lenny-system.svc:9000",
		"password: ${DB_PASSWORD}",
		"psql --password $PGPASSWORD",
		"apiKey=$(cat /tmp/key)",
		"password: <your-password>",
		"secret: " + Redacted,
		"secret: no",
		"user lenny, password lenny, without TLS; access key lennyminio, secret key lennyminio123",
		"secretName: lenny-tls",
		"existingSecret: lenny-db",
		`rootCredentialsSecret: \"lenny-minio-root\"`,
		"      --password string   Password for the user",
		"  --hide-secret     hide the secret value",
		"use the --hide-secret flag. secretsRef: lenny-creds",
		"kind: Secret",
		"kubectl get secret db -o jsonpath='{.data.password}'",
		"The gateway supports Basic authentication for the bootstrap user.",
	}
	for _, in := range kept {
		if got := r.String(in); got != in {
			t.Errorf("redact %q\n got %q\nwant it unchanged", in, got)
		}
	}
	if got := NewRedactor().String("dsn: postgres://lenny:lenny@db:5432/lenny"); got != "dsn: postgres://lenny:lenny@db:5432/lenny" {
		t.Errorf("a connection-string password was removed without a published value: %q", got)
	}
}

// The value under a credential key is the whole value as its spelling
// delimits it, so no part of a multi-word, punctuated, or nested value
// survives, and a published value is kept only on an exact match.
func TestRedactorRemovesWholeValuesInEverySpelling(t *testing.T) {
	r := NewRedactor()
	r.Keep("lenny", "lennyminio", "lennyminio123")
	R := Redacted
	cases := []struct{ in, want string }{
		// Quoted values run to the matching closing quote.
		{`"password": "correct horse battery staple"`, `"password": "` + R + `"`},
		{`"password":"lenny,realsecret99"`, `"password":"` + R + `"`},
		{`"password":"lenny\"x"`, `"password":"` + R + `"`},
		{`"db password": "hunter2-xyz"`, `"db password": "` + R + `"`},
		{`password: "lenny"`, `password: "lenny"`},
		// Unquoted YAML runs to the end of the line.
		{"password: ab,cdefghij", "password: " + R},
		{"password: correct horse battery", "password: " + R},
		{"password: lenny realsecret", "password: " + R},
		{`{"c":"password: ab cd ef\nuser: x"}`, `{"c":"password: ` + R + `\nuser: x"}`},
		// Assignments keep punctuation and stop at an ampersand or at a
		// comma that starts the next pair of a list.
		{"POSTGRES_PASSWORD=pa,ss;word99", "POSTGRES_PASSWORD=" + R},
		{"export PGPASSWORD=hunter2-exp", "export PGPASSWORD=" + R},
		{"url: postgres://x/db?password=hunter2&sslmode=disable", "url: postgres://x/db?password=" + R + "&sslmode=disable"},
		{"--set auth.password=lenny,minio.secretKey=lennyminio123", "--set auth.password=lenny,minio.secretKey=lennyminio123"},
		{"--set auth.password=hunter2,minio.secretKey=other-key-1", "--set auth.password=" + R + ",minio.secretKey=" + R},
		{"PASS=x&y=z", "PASS=x&y=z"},
		// Nested JSON escaping, such as a last-applied-configuration
		// annotation inside a transcript.
		{
			`{"metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"data\":{\"password\":\"aHVudGVyMg==\"}}"}}}`,
			`{"metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"data\":{\"password\":\"` + R + `\"}}"}}}`,
		},
		{`"content":"{\"x\":\"{\\\"password\\\":\\\"aHVudGVyMg==\\\"}\"}"`, `"content":"{\"x\":\"{\\\"password\\\":\\\"` + R + `\\\"}\"}"`},
		{`\\\"token\\\": \\\"abcdefghijkl\\\"`, `\\\"token\\\": \\\"` + R + `\\\"`},
		{`\\\"Authorization\\\": \\\"Basic dXNlcjpwYXNz\\\"`, `\\\"Authorization\\\": \\\"Basic ` + R + `\\\"`},
		// Block scalars, a value on the next indented line, and flow
		// sequences, raw and after an escaped line break.
		{"password: |\n  hunter2-block\n  line2\nnext: x", "password: |\n  " + R + "\nnext: x"},
		{"password: >-\n  hunter2-fold", "password: >-\n  " + R},
		{`{"c":"password: |\n  hunter2-block\nnext: x"}`, `{"c":"password: |\n  ` + R + `\nnext: x"}`},
		{"password:\n  hunter2-nextline", "password:\n  " + R},
		{`"password": ["hunter2-arr"]`, `"password": [` + R + `]`},
		{"password: [abc123def]", "password: [" + R + "]"},
		{`"password": ["lenny"]`, `"password": ["lenny"]`},
		// Values that start like a reference but are not one.
		{"password: *Xk29fjqq", "password: " + R},
		{"password: (Xk29fjqq)", "password: " + R},
		{"password: <Xk29fjqq", "password: " + R},
		{"password: {Xk29fjqq}", "password: " + R},
		{`"masterKey":"$2aXyz123"`, `"masterKey":"` + R + `"`},
		// Exact references, kept words, and keys that open a mapping stay.
		{"password: $ecretVal", "password: $ecretVal"},
		{"password: ${DB_PASSWORD}", "password: ${DB_PASSWORD}"},
		{"apiKey=$(cat /tmp/key)", "apiKey=$(cat /tmp/key)"},
		{"PASS=<your password>", "PASS=<your password>"},
		{"allowEmptyPassword: false", "allowEmptyPassword: false"},
		{"password: true", "password: true"},
		{"password: none", "password: none"},
		{`"apiKey": null`, `"apiKey": null`},
		{"volumes:\n- name: tls\n  secret:\n    defaultMode: 420\n    secretName: lenny-tls", "volumes:\n- name: tls\n  secret:\n    defaultMode: 420\n    secretName: lenny-tls"},
		{"adminPassword:\n  existingSecret: foo", "adminPassword:\n  existingSecret: foo"},
		{"Enter password:\nhunter2-not-indented", "Enter password:\nhunter2-not-indented"},
		// Flags.
		{"--password lenny", "--password lenny"},
		{"--password hunter2-flag", "--password " + R},
		{"--secret-key --insecure", "--secret-key --insecure"},
		{"      --password   Password for the user", "      --password   Password for the user"},
		// The credential keys.
		{"Secret Key: @V@", "Secret Key: " + R},
		{"Access Key: @V@", "Access Key: " + R},
		{"oidcClientSecret: @V@", "oidcClientSecret: " + R},
		{"webhookSecret: hunter2-wh", "webhookSecret: " + R},
		{"github-webhook-secret: hunter2-wh", "github-webhook-secret: " + R},
		{"signingSecret: hunter2-sig", "signingSecret: " + R},
		{"pass: abcdefgh", "pass: " + R},
		{"pwd=abcdefgh", "pwd=" + R},
		{"PGPASS=abcdefgh", "PGPASS=" + R},
		{"db_pass: abcdefgh", "db_pass: " + R},
		{"REDIS-PASS=abcdefgh", "REDIS-PASS=" + R},
		{"PWD=/home/agent", "PWD=/home/agent"},
		{"token_secret: abcdefgh", "token_secret: abcdefgh"},
		{"rootCredentialsSecret: lenny-root", "rootCredentialsSecret: lenny-root"},
		{"- name: POSTGRES_PASSWORD\n  value: hunter2-env", "- name: POSTGRES_PASSWORD\n  value: " + R},
		{`{"name":"DB_PASSWORD","value":"hunter2-envj"}`, `{"name":"DB_PASSWORD","value":"` + R + `"}`},
		{"- name: LOG_LEVEL\n  value: debug-level", "- name: LOG_LEVEL\n  value: debug-level"},
		// Basic credentials and the client's user option.
		{"@CURL@ -u admin:@V@ https://x", "@CURL@ -u admin:" + R + " https://x"},
		{"docker run --user 501:20 img", "docker run --user 501:20 img"},
		{"authorization=Basic dXNlcjpwYXNz", "authorization=Basic " + R},
		{`"Authorization": ["Basic dXNlcjpwYXNz"]`, `"Authorization": ["Basic ` + R + `"]`},
		{"-H 'Authorization:Basic dXNlcjpwYXNz'", "-H 'Authorization:Basic " + R + "'"},
	}
	for _, tc := range cases {
		in, want := fill(tc.in), fill(tc.want)
		got := r.String(in)
		if got != want {
			t.Errorf("redact %q\n got %q\nwant %q", in, got, want)
		}
		if again := r.String(got); again != got {
			t.Errorf("redacting %q again changed it to %q; redaction is not idempotent", got, again)
		}
	}
}

// A record stream holds a line that a credential key's value can follow,
// so the stream and the read-back of the records agree.
func TestRecordStreamAndReadBackAgreeOnKeyedValues(t *testing.T) {
	r := NewRedactor()
	dir := t.TempDir()
	for i, in := range []string{
		"Enter password:\nhunter2-stream\n",
		"data:\n  secret:\n    name: foo\n",
		"password: |\n  hunter2-block\nnext: x}\n",
		"- name: POSTGRES_PASSWORD\n  value: hunter2-env\n",
	} {
		path := filepath.Join(dir, fmt.Sprintf("record-%d", i))
		w, err := r.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.SplitAfter(in, "\n") {
			if _, err := w.Write([]byte(line)); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if rewritten, err := SweepRecords(r, path); err != nil || len(rewritten) != 0 {
			t.Errorf("the read-back rewrote %q written as a stream: %v, %v", in, rewritten, err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "hunter2-block") || strings.Contains(string(b), "hunter2-env") {
			t.Errorf("the stream kept a keyed value: %q", b)
		}
	}
}

func TestRedactorScansLongKeyedLinesInLinearTime(t *testing.T) {
	for _, unit := range []string{"password", "x.password=", "secret:", "--secret ", "password: |\n  ", `"pass":"`, "- name: PASS\n"} {
		assertLinear(t, fmt.Sprintf("%q", unit), func(size int) string { return strings.Repeat(unit, size/len(unit)+1) })
	}
}
