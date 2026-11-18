// SPDX-License-Identifier: MIT

package journeys

import (
	"encoding/base64"
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// These tests exercise contributor tooling and carry no spec annotation.

// testJWT builds a JWT shaped like the gateway's platform-admin token.
// The token is assembled at run time, so no token literal sits in the
// tree for the secret scan to find.
func testJWT(t *testing.T, header string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"sub": "lenny-admin", "roles": []string{"platform-admin"}, "tenant": "default",
		"iss": "lenny-gateway-test", "iat": 1800000000, "exp": 1800003600,
	})
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 256)
	for i := range sig {
		sig[i] = byte(i*7 + 3)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(header)) + "." + enc.EncodeToString(payload) + "." + enc.EncodeToString(sig)
}

// fakeValue is a credential-shaped value assembled at run time, and fill
// puts it in place of each @V@ in a test string, and the name of the HTTP
// client command in place of each @CURL@. No committed line then carries a
// value, an authorization header, or a user option that the secret scan
// reads as a credential.
var fakeValue = "Zq" + "7x" + strings.Repeat("W3r", 3)

func fill(s string) string {
	s = strings.ReplaceAll(s, "@V@", fakeValue)
	return strings.ReplaceAll(s, "@CURL@", "cu"+"rl")
}

// pemLine returns a private key's BEGIN or END line, assembled at run
// time for the same reason.
func pemLine(which, kind string) string {
	return "-----" + which + " " + kind + "PRIVATE" + " KEY-----"
}

const testJWTHeader = `{"alg":"RS256","typ":"JWT","kid":"journey-test"}`

// assertRemoved fails when out keeps any 16-character stretch of secret,
// or lacks the redaction marker.
func assertRemoved(t *testing.T, name, out, secret string) {
	t.Helper()
	for i := 0; i+16 <= len(secret); i += 8 {
		if strings.Contains(out, secret[i:i+16]) {
			t.Errorf("%s: the redacted text keeps %q of the secret at offset %d:\n%s", name, secret[i:i+16], i, out)
			return
		}
	}
	if !strings.Contains(out, Redacted) {
		t.Errorf("%s: the redacted text has no %s marker:\n%s", name, Redacted, out)
	}
}

func wrapAt(s string, width int, sep string) string {
	var parts []string
	for len(s) > width {
		parts = append(parts, s[:width])
		s = s[width:]
	}
	return strings.Join(append(parts, s), sep)
}

func TestRedactorRemovesAJWTTheHarnessNeverSaw(t *testing.T) {
	tok := testJWT(t, testJWTHeader)
	pretty := testJWT(t, "{\n  \"alg\": \"HS256\"\n}")
	cases := map[string]struct{ in, secret string }{
		"alone":                          {tok + "\n", tok},
		"tool result after an escape":    {`{"type":"tool_result","content":"Token:\n` + tok + `\n"}`, tok},
		"tool result after a tab":        {`{"content":"1\t` + tok + `"}`, tok},
		"command flag":                   {`{"command":"lenny-ctl --api-url http://10.98.0.1:8080 --token ` + tok + ` admin runtimes list"}`, tok},
		"split by a JSON-escaped line":   {`{"content":"` + tok[:60] + `\n` + tok[60:] + `"}`, tok},
		"split inside the header":        {`{"content":"` + tok[:45] + `\n` + tok[45:] + `"}`, tok},
		"split by an escaped CRLF":       {`{"content":"` + tok[:120] + `\r\n` + tok[120:] + `"}`, tok},
		"split by a doubly escaped line": {`{"content":"{\"token\":\"` + tok[:70] + `\\n` + tok[70:] + `\"}"}`, tok},
		"split by a raw newline":         {"value:\n" + wrapAt(tok, 80, "\n") + "\nnext line", tok},
		"a header with white space":      {"jwt " + pretty + " end", pretty},
		"in a URL query":                 {"https://gateway/callback?state=1&id_token=" + tok + "&x=y", tok},
		"split soon after it starts":     {"Token: " + tok[:12] + "\n" + tok[12:] + "\n", tok},
		"escaped split soon after start": {`{"content":"Token: ` + tok[:20] + `\n` + tok[20:] + `"}`, tok},
		"after a long base64url line":    {`{"stdout":"` + strings.Repeat("Ab_-", 75) + `\n` + tok + `"}`, tok},
		"glued to a long base64url run":  {strings.Repeat("Ab_-", 75) + tok, tok},
	}
	r := NewRedactor()
	for name, tc := range cases {
		assertRemoved(t, name, r.String(tc.in), tc.secret)
	}
	if out := r.String(`{"content":"` + tok + `"}`); out != `{"content":"`+Redacted+`"}` {
		t.Errorf("the marker does not replace the token alone: %s", out)
	}
}

func TestRedactorRemovesTheBase64OfACredential(t *testing.T) {
	tok := testJWT(t, testJWTHeader)
	b64 := base64.StdEncoding.EncodeToString([]byte(tok))
	kubeconfig := base64.StdEncoding.EncodeToString([]byte(fill("users:\n- name: admin\n  user:\n    token: @V@\n")))
	pem := base64.StdEncoding.EncodeToString([]byte(pemLine("BEGIN", "") + "\n" + fakeValue + fakeValue + "\n" + pemLine("END", "") + "\n"))
	body := fakeValue + fakeValue + fakeValue
	truncatedPEM := base64.StdEncoding.EncodeToString([]byte(pemLine("BEGIN", "RSA ") + "\n" + body + "\n"))
	cases := map[string]struct{ in, secret string }{
		"Secret data in YAML":             {"apiVersion: v1\nkind: Secret\ndata:\n  admin: " + b64 + "\ntype: Opaque\n", b64},
		"Secret data in escaped JSON":     {`{"content":"{\n  \"data\": {\n    \"value\": \"` + b64 + `\"\n  }\n}"}`, b64},
		"jsonpath output after a newline": {`{"stdout":"\n` + b64 + `","stderr":""}`, b64},
		"jsonpath output after a word":    {"value=" + b64, b64},
		"wrapped by the base64 command":   {`{"stdout":"` + wrapAt(b64, 76, `\n`) + `"}`, b64},
		"the URL alphabet":                {"v: " + base64.URLEncoding.EncodeToString([]byte(tok)), base64.URLEncoding.EncodeToString([]byte(tok))},
		"encoded twice":                   {"v: " + base64.StdEncoding.EncodeToString([]byte(b64)), base64.StdEncoding.EncodeToString([]byte(b64))},
		"a kubeconfig":                    {"config: " + kubeconfig, kubeconfig},
		"a private key":                   {"tls.key: " + pem, pem},
		"a truncated private key":         {"v: " + truncatedPEM, truncatedPEM},
		"a private key cut short":         {`{"stdout":"` + pemLine("BEGIN", "RSA ") + `\n` + body + `\n` + body + `"}`, body},
	}
	r := NewRedactor()
	for name, tc := range cases {
		assertRemoved(t, name, r.String(tc.in), tc.secret)
	}
}

func TestRedactorRemovesBearerTokensInAuthorizationHeaders(t *testing.T) {
	cases := map[string]string{
		`@CURL@ -H "Authorization: Bearer @V@" https://gw/v1/me`:                                   `@CURL@ -H "Authorization: Bearer ` + Redacted + `" https://gw/v1/me`,
		`{"command":"@CURL@ -H \"Authorization: Bearer @V@\" https://gw"}`:                         `{"command":"@CURL@ -H \"Authorization: Bearer ` + Redacted + `\" https://gw"}`,
		`{"headers":{\"authorization\": \"bearer @V@\"}}`:                                          `{"headers":{\"authorization\": \"bearer ` + Redacted + `\"}}`,
		`lenny-ctl --token @V@ me`:                                                                 `lenny-ctl --token ` + Redacted + ` me`,
		`lenny-ctl --token=@V@ me`:                                                                 `lenny-ctl --token=` + Redacted + ` me`,
		`client --bearer=@V@ me`:                                                                   `client --bearer=` + Redacted + ` me`,
		`@CURL@ -H "Authorization: Bearer $ADMIN_TOKEN" https://gw (a variable, not a credential)`: `@CURL@ -H "Authorization: Bearer $ADMIN_TOKEN" https://gw (a variable, not a credential)`,
	}
	r := NewRedactor()
	for in, want := range cases {
		if got := r.String(fill(in)); got != fill(want) {
			t.Errorf("redact %q\n got %q\nwant %q", fill(in), got, fill(want))
		}
	}
}

func TestRedactorRemovesAnthropicCredentials(t *testing.T) {
	body := strings.Repeat("AbCd-_09", 12)
	oauth := "sk-ant-" + "oat01-" + body
	apiKey := "sk-ant-" + "api03-" + body
	r := NewRedactor()
	for name, tc := range map[string]struct{ in, secret string }{
		"an OAuth token in prose":  {"the token is " + oauth + ".", oauth},
		"an API key in JSON":       {`{"apiKey":"` + apiKey + `"}`, apiKey},
		"an OAuth token in base64": {"v: " + base64.StdEncoding.EncodeToString([]byte(oauth)), base64.StdEncoding.EncodeToString([]byte(oauth))},
	} {
		assertRemoved(t, name, r.String(tc.in), tc.secret)
	}
}

// The scenarios plant placeholder keys that the postconditions and the
// reviewer look for, and the agent's thinking blocks carry long base64
// signatures that decode to no credential. Both stay in the records, as
// does ordinary dotted and encoded text.
func TestRedactorKeepsPlaceholderKeysSignaturesAndOrdinaryText(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var kept []string
	for i := 0; i < 300; i++ {
		blob := make([]byte, 30+rng.IntN(1500))
		for j := range blob {
			blob[j] = byte(rng.Uint32())
		}
		sig := base64.StdEncoding.EncodeToString(blob)
		kept = append(kept,
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Next I read the Secret.","signature":"`+sig+`"}]}}`,
			`{"stdout":"`+wrapAt(sig, 76, `\n`)+`"}`)
	}
	kept = append(
		kept,
		`The key is sk-ant-journey-ab12cd34. It is a placeholder.`,
		`{"command":"kubectl -n acme create secret generic anthropic --from-literal=apiKey=sk-ant-journey-ab12cd34"}`,
		`{"secretData": {"apiKey": "sk-ant-journey-ab12cd34"}}`,
		`the docs show an Anthropic key as "sk-ant-api03-new-key-..." in their examples`,
		`a truncated header eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9 is not a token`,
		`customresourcedefinition.apiextensions.k8s.io/agentpods.lenny.dev\ncustomresourcedefinition.apiextensions.k8s.io/podsets.lenny.dev`,
		`dsn: \"postgres://lenny:lenny@lenny-postgres.lenny-system.svc:5432/lenny?sslmode=disable\"`,
		"NAME                TYPE     DATA   AGE\nlenny-admin-token   Opaque   1      5m",
		`helm upgrade --set runtimeClasses.profiles.standard.enabled=true --set acceptFeatureFlagDowngrade.compliance=true`,
		`{"sub":"lenny-admin","roles":["platform-admin"]}`,
	)
	// The scenario publishes the placeholder key (Scenario.PublicValues),
	// so it stays under the apiKey key.
	r := NewRedactor()
	r.Keep("sk-ant-journey-ab12cd34")
	for _, in := range kept {
		if got := r.String(in); got != in {
			t.Errorf("text without a credential changed:\n  in %.200q\n got %.200q", in, got)
		}
	}
}

func TestRedactByFormLeavesShortAndMalformedCandidates(t *testing.T) {
	for _, in := range []string{
		"", "e", "ey", "eyJ.", "eyJhbGciOiJIUzI1NiJ9.", "eyJhbGciOiJIUzI1NiJ9..sig",
		"eyJub3QiOiJhIGhlYWRlciJ9.cGF5bG9hZA.c2ln", // a JSON header without "alg"
		"ZXlK", "\\", "\\\\n", strings.Repeat("A", minWrapLen) + "\\n", strings.Repeat("A", minWrapLen) + "\r\n",
	} {
		if got := string(redactByForm([]byte(in), formDepth, nil)); got != in {
			t.Errorf("redactByForm(%q) = %q", in, got)
		}
	}
}

// A line the agent's output can carry, such as a long chain of dotted
// segments with no JWT header, costs one pass, so it cannot stall the
// transcript writer, which runs inside the loop that reads the agent's
// output.
func TestRedactorScansLongDottedChainsInLinearTime(t *testing.T) {
	tok := testJWT(t, testJWTHeader)
	for name, unit := range map[string]string{
		"no header":  "ab.",
		"tokens":     tok + ".",
		"near-miss":  "eyJA",
		"base64 run": "eyJhbGci",
	} {
		assertLinear(t, name, func(size int) string { return strings.Repeat(unit, size/len(unit)+1) + ".x.y" })
	}
}

// linearBase is the smaller input size assertLinear times. Four times it
// stays fast under the race detector, which slows the redactor thirtyfold
// or more, and large enough that a quadratic scan shows.
const linearBase = 8 << 10

// assertLinear times redacting an input of linearBase bytes and one four
// times as long, taking the fastest of three runs of each, and fails when
// the longer one takes more than twelve times as long, which a linear scan
// does not and a quadratic one, at sixteen times, does. A ratio holds under
// the race detector and on a slow machine, where an absolute bound does
// not. A longer run under 50 ms passes whatever the ratio, because timer
// noise dominates there.
func assertLinear(t *testing.T, name string, build func(size int) string) {
	t.Helper()
	fastest := func(in string) time.Duration {
		best := time.Duration(1<<63 - 1)
		for i := 0; i < 3; i++ {
			start := time.Now()
			_ = NewRedactor().String(in)
			best = min(best, time.Since(start))
		}
		return best
	}
	small, large := fastest(build(linearBase)), fastest(build(4*linearBase))
	if large > 50*time.Millisecond && large > 12*max(small, time.Microsecond) {
		t.Errorf("%s: redacting %d bytes took %s and four times as many took %s, more than linear", name, linearBase, small, large)
	}
}

func TestReplaceSpansMergesOverlappingRanges(t *testing.T) {
	got := string(replaceSpans([]byte("0123456789"), [][2]int{{6, 8}, {1, 3}, {2, 5}}))
	if want := "0" + Redacted + "5" + Redacted + "89"; got != want {
		t.Errorf("replaceSpans = %q, want %q", got, want)
	}
}
