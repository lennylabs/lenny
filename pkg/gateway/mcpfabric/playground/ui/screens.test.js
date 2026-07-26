"use strict";

// Node unit test that walks the playground SPA's three §27.4 screens
// (runtime picker, session configuration, chat) end to end and drives
// the chat pane's Interrupt button and raw-frame inspector.
//
// spec: §27.4 ("The playground ships as a single-page React app with
// three screens: 1. Runtime picker ... 2. Session configuration ...
// 3. Chat. A single-session chat pane backed by the MCP WebSocket.
// Renders messages, tool-call events, delegation events, and errors.
// Includes an Interrupt button, a Cancel button, a raw-frame inspector
// (expandable panel that shows the exact MCP frames for debugging),
// and a 'Copy as client SDK snippet' button...")
//
// app.js has no build step (see its header comment) and is not an ES
// module; it is a plain CommonJS-compatible IIFE that Node can
// `require` directly (the same pattern app.test.js already uses for
// the SDK-snippet generator). Rather than pull in a browser-emulation
// dependency, this file implements the minimal DOM surface app.js
// actually touches — element creation, attributes, class lists, event
// listeners, and text content — so the real renderRuntimePicker,
// renderSessionConfig, and renderChat functions run unmodified against
// a fake `document`/`window`/`fetch`/`WebSocket`, and the test asserts
// on the resulting DOM tree exactly as a browser-driven test would.

const assert = require("node:assert/strict");
const test = require("node:test");
const path = require("node:path");

// ---- minimal DOM shim ----
// Supports exactly the operations app.js performs: creating elements
// and text nodes, appendChild/removeChild/insertBefore, className and
// classList, setAttribute, addEventListener/dispatch, and a
// textContent getter/setter that mirrors the browser's tree-flattening
// behavior closely enough for content assertions.
function DomNode(tag) {
  this.tagName = tag ? String(tag).toUpperCase() : null;
  this.children = [];
  this.parentNode = null;
  this.attributes = {};
  this._className = "";
  this._listeners = {};
  this._nodeValue = null;
  this.value = "";
  this.checked = false;
  this.hidden = false;
}
Object.defineProperty(DomNode.prototype, "className", {
  get: function () { return this._className; },
  set: function (v) { this._className = v == null ? "" : String(v); },
});
Object.defineProperty(DomNode.prototype, "textContent", {
  get: function () {
    if (this._nodeValue != null) return this._nodeValue;
    return this.children.map(function (c) { return c.textContent; }).join("");
  },
  set: function (v) {
    this.children = [];
    this._nodeValue = null;
    if (v != null && v !== "") {
      var t = new DomNode(null);
      t._nodeValue = String(v);
      t.parentNode = this;
      this.children.push(t);
    }
  },
});
DomNode.prototype.setAttribute = function (k, v) { this.attributes[k] = v; };
DomNode.prototype.getAttribute = function (k) { return this.attributes[k]; };
DomNode.prototype.appendChild = function (child) {
  child.parentNode = this;
  this.children.push(child);
  return child;
};
DomNode.prototype.removeChild = function (child) {
  var i = this.children.indexOf(child);
  if (i >= 0) this.children.splice(i, 1);
  child.parentNode = null;
  return child;
};
DomNode.prototype.insertBefore = function (newNode, refNode) {
  var i = this.children.indexOf(refNode);
  newNode.parentNode = this;
  if (i < 0) this.children.push(newNode);
  else this.children.splice(i, 0, newNode);
  return newNode;
};
Object.defineProperty(DomNode.prototype, "firstChild", {
  get: function () { return this.children[0] || null; },
});
DomNode.prototype.addEventListener = function (type, fn) {
  (this._listeners[type] = this._listeners[type] || []).push(fn);
};
DomNode.prototype.dispatch = function (type, ev) {
  (this._listeners[type] || []).forEach(function (fn) { fn(ev || {}); });
};
Object.defineProperty(DomNode.prototype, "classList", {
  get: function () {
    var self = this;
    return {
      add: function (c) {
        var parts = self._className.split(/\s+/).filter(Boolean);
        if (parts.indexOf(c) < 0) parts.push(c);
        self._className = parts.join(" ");
      },
      remove: function (c) {
        self._className = self._className.split(/\s+/).filter(function (x) { return x && x !== c; }).join(" ");
      },
      contains: function (c) {
        return self._className.split(/\s+/).indexOf(c) !== -1;
      },
    };
  },
});

function createTextNode(text) {
  var t = new DomNode(null);
  t._nodeValue = String(text);
  return t;
}

// The three elements app.js captures at module-load time via
// document.getElementById (§27.4's index.html mounts the SPA into
// #app, with #banner and #auth-status alongside it).
var appEl = new DomNode("div");
var bannerEl = new DomNode("div");
var authStatusEl = new DomNode("span");
var byId = { app: appEl, banner: bannerEl, "auth-status": authStatusEl };

global.document = {
  getElementById: function (id) { return byId[id] || null; },
  createElement: function (tag) { return new DomNode(tag); },
  createTextNode: createTextNode,
};
global.window = {
  addEventListener: function () {},
  location: { protocol: "http:", host: "playground.example", href: "" },
};
global.sessionStorage = { getItem: function () { return null; }, setItem: function () {} };
global.navigator = { clipboard: { writeText: function () {} } };
global.alert = function () {};

const app = require(path.join(__dirname, "app.js"));

function flush() {
  return new Promise(function (resolve) { setTimeout(resolve, 0); });
}

function findAll(node, predicate, out) {
  out = out || [];
  if (predicate(node)) out.push(node);
  (node.children || []).forEach(function (c) { findAll(c, predicate, out); });
  return out;
}

function findButton(root, text) {
  return findAll(root, function (n) { return n.tagName === "BUTTON" && n.textContent === text; })[0];
}

test("the playground walks the runtime picker, session configuration, and chat screens, and the chat screen's Interrupt button and raw-frame inspector work as specified", async () => {
  const sessionsPosted = [];

  // ---- fetch stub: /v1/playground/token, GET /v1/runtimes, POST /v1/sessions ----
  global.fetch = function (url, opts) {
    if (url === "/v1/playground/token") {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: function () {
          return Promise.resolve({ bearerToken: "test-bearer", expiresInSeconds: 900, effectiveScope: "" });
        },
      });
    }
    if (url === "/v1/runtimes") {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: function () {
          return Promise.resolve([
            { id: "acme-runtime", description: "an example runtime" },
            { id: "other-runtime", description: "excluded by allowedRuntimes" },
          ]);
        },
      });
    }
    if (url === "/v1/sessions") {
      sessionsPosted.push(JSON.parse(opts.body));
      return Promise.resolve({
        ok: true,
        status: 200,
        json: function () { return Promise.resolve({ id: "sess-1" }); },
      });
    }
    return Promise.reject(new Error("unexpected fetch to " + url));
  };

  // ---- WebSocket stub: records every frame the SPA sends ----
  function FakeWebSocket(url, protocols) {
    this.url = url;
    this.protocols = protocols;
    this.readyState = FakeWebSocket.OPEN;
    this.sent = [];
    FakeWebSocket.instances.push(this);
    var self = this;
    // Defer onopen to a microtask so app.js finishes assigning its
    // onopen/onmessage/onclose handlers (in the same synchronous block
    // right after `new WebSocket(...)`) before it fires.
    Promise.resolve().then(function () {
      if (self.onopen) self.onopen();
    });
  }
  FakeWebSocket.OPEN = 1;
  FakeWebSocket.instances = [];
  FakeWebSocket.prototype.send = function (data) { this.sent.push(data); };
  FakeWebSocket.prototype.close = function () {};
  global.WebSocket = FakeWebSocket;

  // ---- screen 1: runtime picker (§27.4 item 1) ----
  app.state.config = { authMode: "dev", allowedRuntimes: ["acme-*"], wsPath: "/mcp/v1/ws" };
  app.renderRuntimePicker();
  await flush();

  const runtimeCards = findAll(appEl, function (n) { return n.className === "runtime-card"; });
  assert.equal(runtimeCards.length, 1, "the picker must render only the runtime allowedRuntimes permits");
  assert.match(runtimeCards[0].textContent, /acme-runtime/);
  assert.doesNotMatch(appEl.textContent, /other-runtime/, "a runtime excluded by allowedRuntimes must not render");

  const useButton = findButton(appEl, "use this runtime");
  assert.ok(useButton, '§27.4 item 1 requires a "use this runtime" button on each picker entry');
  useButton.dispatch("click");
  await flush();

  // ---- screen 2: session configuration (§27.4 item 2) ----
  assert.match(appEl.textContent, /Configure session: acme-runtime/, "selecting a runtime must open its session-configuration screen");
  const createButton = findButton(appEl, "create session");
  assert.ok(createButton, '§27.4 item 2 requires a "create session" control');
  createButton.dispatch("click");
  await flush();

  assert.equal(sessionsPosted.length, 1, "creating the session must POST /v1/sessions");
  assert.equal(sessionsPosted[0].runtimeRef, "acme-runtime");

  // ---- screen 3: chat (§27.4 item 3) ----
  assert.match(appEl.textContent, /Session sess-1/, "a successful create must open the chat screen for the new session");
  const interruptButton = findButton(appEl, "interrupt");
  const cancelButton = findButton(appEl, "cancel");
  assert.ok(interruptButton, "§27.4 item 3 requires an Interrupt button");
  assert.ok(cancelButton, "§27.4 item 3 requires a Cancel button");

  const frameInspector = findAll(appEl, function (n) { return n.className === "frame-inspector"; })[0];
  assert.ok(frameInspector, "§27.4 item 3 requires a raw-frame inspector");
  const summary = findAll(frameInspector, function (n) { return n.tagName === "SUMMARY"; })[0];
  assert.equal(summary.textContent, "raw MCP frame inspector");
  const framePanel = findAll(frameInspector, function (n) { return n.tagName === "PRE"; })[0];

  // Rendering the chat screen synchronously opens the MCP WebSocket
  // (mintBearer's promise chain and the WebSocket's onopen both settle
  // as microtasks the `createButton` flush above already drained), so
  // the attach_session frame has already been sent and recorded.
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  assert.ok(ws, "the chat screen must open an MCP WebSocket");
  assert.equal(ws.sent.length, 1, "opening the WebSocket must send the attach_session frame");
  assert.match(framePanel.textContent, /lenny\/attach_session/, "the raw-frame inspector must render the attach_session frame it just sent");

  // Send a chat message: it must go out over the WebSocket and appear
  // in the raw-frame inspector.
  const input = findAll(appEl, function (n) { return n.tagName === "INPUT" && n.attributes.type === "text"; })[0];
  input.value = "hello agent";
  const sendButton = findButton(appEl, "send");
  assert.ok(input && sendButton, "the chat screen must render a message input and a send control");
  sendButton.dispatch("click");

  assert.equal(ws.sent.length, 2, "sending a message must send a frame over the MCP WebSocket");
  const sentMessageFrame = JSON.parse(ws.sent[1]);
  assert.equal(sentMessageFrame.params.name, "lenny/send_message");
  assert.equal(sentMessageFrame.params.arguments.message, "hello agent");
  assert.match(framePanel.textContent, /lenny\/send_message/, "the raw-frame inspector must render the outbound chat frame");

  // The Interrupt button must issue lenny/interrupt_session over the
  // same MCP WebSocket the chat pane uses.
  interruptButton.dispatch("click");
  assert.equal(ws.sent.length, 3);
  const interruptFrame = JSON.parse(ws.sent[2]);
  assert.equal(interruptFrame.params.name, "lenny/interrupt_session", "the Interrupt button must issue lenny/interrupt_session");
  assert.equal(interruptFrame.params.arguments.sessionId, "sess-1");

  // The Cancel button must issue lenny/cancel_session.
  cancelButton.dispatch("click");
  assert.equal(ws.sent.length, 4);
  const cancelFrame = JSON.parse(ws.sent[3]);
  assert.equal(cancelFrame.params.name, "lenny/cancel_session", "the Cancel button must issue lenny/cancel_session");
});
