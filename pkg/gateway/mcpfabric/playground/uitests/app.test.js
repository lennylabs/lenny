"use strict";

// Node unit test for the "Copy as client SDK snippet" generator
// embedded in the playground's browser bundle (spec: §27.9, "The
// 'Copy as client SDK snippet' feature generates code that never
// includes credentials; snippets reference environment variables /
// OIDC flow only.").
//
// This file sits in uitests/ rather than beside app.js in ui/ because
// the gateway embeds the whole ui/ subtree and serves every file in it
// at GET /playground/<path> (spec: §27.2, §27.7). Test source is not
// one of the static assets §27.7 serves, so it stays outside the
// embedded subtree and reaches app.js by relative path.
//
// app.js has no build step (see its header comment) and is not an ES
// module; it is a plain CommonJS-compatible IIFE that Node can
// `require` directly. When loaded under Node's CommonJS loader (as
// opposed to a browser `<script>` tag), app.js exports its
// snippet-generation internals instead of calling its browser-only
// `start()` bootstrap. This test stubs the two globals app.js touches
// unconditionally at module-load time (`document.getElementById` and
// `window.addEventListener`) and then drives the exported functions
// directly, so it needs no DOM library and no npm dependency.

const assert = require("node:assert/strict");
const test = require("node:test");
const path = require("node:path");

global.document = {
  getElementById() {
    return null;
  },
};
global.window = {
  addEventListener() {},
};

const app = require(path.join(__dirname, "..", "ui", "app.js"));

// A realistic-shaped live bearer. If a regression interpolated the
// live credential into the generated snippet, this is the value that
// would leak into copyable source code.
const LIVE_BEARER =
  "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.fake-live-session-signature";

// copiedSnippetFor puts a live bearer (and a live apiKey-mode token)
// into the in-memory client state, invokes the real copy-to-clipboard
// path for the given language, and returns exactly what was written
// to the clipboard.
function copiedSnippetFor(lang) {
  app.state.bearer = LIVE_BEARER;
  app.state.bearerExpiresAt = Date.now() + 5 * 60 * 1000;
  app.state.apiKeyToken = "operator-issued-live-service-account-token";
  app.state.runtime = { id: "acme-runtime" };

  let copied = null;
  const savedNavigator = global.navigator;
  const savedAlert = global.alert;
  global.navigator = { clipboard: { writeText: (text) => { copied = text; } } };
  global.alert = () => {};
  try {
    app.copySnippet(lang);
  } finally {
    global.navigator = savedNavigator;
    global.alert = savedAlert;
  }
  assert.notStrictEqual(copied, null, "copySnippet(" + lang + ") did not write to the clipboard");
  return copied;
}

test("Go SDK snippet omits a live bearer and sources the token from an env var / OIDC flow", () => {
  const snippet = copiedSnippetFor("go");
  assert.ok(!snippet.includes(LIVE_BEARER), "Go snippet must not embed the live bearer token");
  assert.ok(!snippet.includes(app.state.apiKeyToken), "Go snippet must not embed the live apiKey-mode token");
  assert.ok(snippet.includes('os.Getenv("LENNY_BEARER_TOKEN")'), "Go snippet must source the token from an environment variable");
  assert.match(snippet, /oidc/i, "Go snippet must reference the OIDC flow");
});

test("Python SDK snippet omits a live bearer and sources the token from an env var / OIDC flow", () => {
  const snippet = copiedSnippetFor("python");
  assert.ok(!snippet.includes(LIVE_BEARER), "Python snippet must not embed the live bearer token");
  assert.ok(!snippet.includes(app.state.apiKeyToken), "Python snippet must not embed the live apiKey-mode token");
  assert.ok(snippet.includes("os.environ['LENNY_BEARER_TOKEN']"), "Python snippet must source the token from an environment variable");
  assert.match(snippet, /oidc/i, "Python snippet must reference the OIDC flow");
});

test("TypeScript SDK snippet omits a live bearer and sources the token from an env var / OIDC flow", () => {
  const snippet = copiedSnippetFor("typescript");
  assert.ok(!snippet.includes(LIVE_BEARER), "TypeScript snippet must not embed the live bearer token");
  assert.ok(!snippet.includes(app.state.apiKeyToken), "TypeScript snippet must not embed the live apiKey-mode token");
  assert.ok(snippet.includes("process.env.LENNY_BEARER_TOKEN"), "TypeScript snippet must source the token from an environment variable");
  assert.match(snippet, /oidc/i, "TypeScript snippet must reference the OIDC flow");
});
