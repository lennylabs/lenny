// SPDX-License-Identifier: MIT

package journeys

import (
	"encoding/base64"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestRedactorRemovesValuesUnderCredentialKeys(t *testing.T) {
	masterKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("Kq7z", 16)))
	cases := map[string]string{
		"password: hunter2-db":                                                         "password: " + Redacted,
		"data:\n  postgres-password: c2VjcmV0dmFsdWU=\n":                               "data:\n  postgres-password: " + Redacted + "\n",
		"type: Opaque\ndata:\n  master-key: " + masterKey + "\n":                       "type: Opaque\ndata:\n  master-key: " + Redacted + "\n",
		`{"apiKey": "abc123def456"}`:                                                   `{"apiKey": "` + Redacted + `"}`,
		`{"content":"{\"secret\": \"s3cr3t-value\"}"}`:                                 `{"content":"{\"secret\": \"` + Redacted + `\"}"}`,
		"MINIO_SECRET_KEY=minio-secret-1234 helm install":                              "MINIO_SECRET_KEY=" + Redacted + " helm install",
		"export POSTGRES_PASSWORD='pg-pass-9876'":                                      "export POSTGRES_PASSWORD='" + Redacted + "'",
		"API_KEY=k-0123456789":                                                         "API_KEY=" + Redacted,
		"api_key: k-0123456789":                                                        "api_key: " + Redacted,
		"accessKey: lennyminio-other":                                                  "accessKey: " + Redacted,
		"lenny-ctl login --password=flag-pass-1":                                       "lenny-ctl login --password=" + Redacted,
		"mc alias set s3 https://minio --access-key AKIA1234567890":                    "mc alias set s3 https://minio --access-key " + Redacted,
		"kubectl create secret generic kms --from-literal=master-key=0123456789abcdef": "kubectl create secret generic kms --from-literal=master-key=" + Redacted,
		`curl -H "Authorization: Basic dXNlcjpwYXNzd29yZA==" https://gw`:               `curl -H "Authorization: Basic ` + Redacted + `" https://gw`,
		`{"command":"curl -H \"authorization: basic dXNlcjpwYXNz\" https://gw"}`:       `{"command":"curl -H \"authorization: basic ` + Redacted + `\" https://gw"}`,
		"secret: xyz123-value":                                                         "secret: " + Redacted,
		"client_secret: abcd1234":                                                      "client_secret: " + Redacted,
		"oidc login --client-secret abcd1234":                                          "oidc login --client-secret " + Redacted,
		// A documentation example is removed as well; the pattern cannot
		// tell it from a credential.
		`--from-literal=apiKey="sk-ant-..."`: `--from-literal=apiKey="` + Redacted + `"`,
		"accessKey: minioadmin":              "accessKey: " + Redacted,
		// A published value stays only when the redactor is told about it.
		"password: lenny": "password: " + Redacted,
	}
	r := NewRedactor()
	for in, want := range cases {
		if got := r.String(in); got != want {
			t.Errorf("redact %q\n got %q\nwant %q", in, got, want)
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
