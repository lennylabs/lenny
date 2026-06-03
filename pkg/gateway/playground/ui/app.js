// Lenny Playground single-page app.
//
// The playground is a client of the public MCP and REST surface
// (§27.5): runtime discovery uses GET /v1/runtimes, session creation
// uses POST /v1/sessions, the chat stream uses the MCP WebSocket, and
// the session-capability JWT is minted by POST /v1/playground/token.
// The only playground-specific endpoints this app calls are the auth
// gatekeepers under /playground/auth/.
//
// This bundle is plain ES2017 with no build step. The §27.4 spec
// describes a React SPA; the playground screens and the §27.5
// protocol usage are implemented here directly so the bundle is
// embeddable in the gateway binary without an npm toolchain.

(function () {
  "use strict";

  // ---- in-memory state ----
  // The minted bearer is held in memory only, never in localStorage,
  // sessionStorage, or a cookie (§27.3.1 "Caching").
  var state = {
    config: null,
    bearer: null,
    bearerExpiresAt: 0,
    apiKeyToken: null, // apiKey mode: held in sessionStorage by the form
    runtime: null,
    sessionId: null,
    ws: null,
    frames: [],
  };

  var app = document.getElementById("app");
  var bannerEl = document.getElementById("banner");
  var authStatusEl = document.getElementById("auth-status");

  // ---- small DOM helpers ----
  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    attrs = attrs || {};
    Object.keys(attrs).forEach(function (k) {
      if (k === "class") node.className = attrs[k];
      else if (k === "text") node.textContent = attrs[k];
      else if (k.indexOf("on") === 0) node.addEventListener(k.slice(2), attrs[k]);
      else if (attrs[k] === true) node.setAttribute(k, "");
      else if (attrs[k] !== false && attrs[k] != null) node.setAttribute(k, attrs[k]);
    });
    (children || []).forEach(function (c) {
      if (c == null) return;
      node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
    });
    return node;
  }
  function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  // ---- bootstrap ----
  function start() {
    fetch("/playground/config.json", { credentials: "same-origin" })
      .then(function (r) {
        if (!r.ok) throw new Error("config fetch failed: " + r.status);
        return r.json();
      })
      .then(function (cfg) {
        state.config = cfg;
        renderBanner(cfg);
        renderRuntimePicker();
      })
      .catch(function (e) {
        renderError("Could not load the playground configuration. " + e.message);
      });
  }

  // §27.9: the dev-mode and apiKey-mode banner text is server-sourced.
  function renderBanner(cfg) {
    if (!cfg.banner) {
      bannerEl.hidden = true;
      return;
    }
    bannerEl.hidden = false;
    bannerEl.className = "banner " + (cfg.bannerSeverity || "warning");
    bannerEl.textContent = cfg.banner;
  }

  function renderError(message) {
    clear(app);
    app.appendChild(el("div", { class: "runtime-card" }, [
      el("h1", { text: "Playground error" }),
      el("p", { class: "err", text: message }),
    ]));
  }

  // ---- bearer acquisition (§27.3.1) ----
  // mintBearer obtains a session-capability JWT from the single
  // mode-polymorphic endpoint POST /v1/playground/token. The cached
  // bearer is reused until 60 s before expiry, then re-exchanged.
  function mintBearer() {
    var now = Date.now();
    if (state.bearer && now < state.bearerExpiresAt - 60000) {
      return Promise.resolve(state.bearer);
    }
    var headers = { "Content-Type": "application/json" };
    var opts = { method: "POST", body: "{}", credentials: "same-origin" };
    // apiKey mode sends the pasted token as Authorization: Bearer.
    if (state.config.authMode === "apiKey") {
      if (!state.apiKeyToken) {
        return Promise.reject(new Error("paste an operator-issued token first"));
      }
      headers.Authorization = "Bearer " + state.apiKeyToken;
    }
    opts.headers = headers;
    return fetch("/v1/playground/token", opts).then(function (r) {
      return r.json().then(function (body) {
        if (!r.ok) {
          var reason = body.error && body.error.details && body.error.details.reason;
          if (r.status === 401 && (reason === "playground_session_expired" || !reason)) {
            // Cookie expired: re-run the OIDC login.
            if (state.config.authMode === "oidc") {
              window.location.href = "/playground/auth/login";
            }
          }
          throw new Error((body.error && body.error.message) || ("token mint failed: " + r.status));
        }
        state.bearer = body.bearerToken;
        state.bearerExpiresAt = Date.now() + body.expiresInSeconds * 1000;
        authStatusEl.textContent = "session token active";
        return state.bearer;
      });
    });
  }

  // §27.5 line 190: runtime discovery is filtered by
  // playground.allowedRuntimes. The gateway is the authoritative filter
  // (it applies the same globs to GET /v1/runtimes for an
  // origin=playground bearer), and the picker honors the server-sourced
  // list a second time so a runtime the operator excluded never renders.
  // `*` is the only metacharacter and matches any sequence; an empty or
  // ["*"] list makes every runtime visible.
  function runtimeAllowed(name) {
    var patterns = (state.config && state.config.allowedRuntimes) || [];
    if (!patterns.length) return true;
    for (var i = 0; i < patterns.length; i++) {
      if (globMatch(patterns[i], name)) return true;
    }
    return false;
  }

  function globMatch(pattern, s) {
    if (pattern.indexOf("*") < 0) return pattern === s;
    var parts = pattern.split("*");
    var n = parts.length;
    if (s.indexOf(parts[0]) !== 0) return false;
    if (s.lastIndexOf(parts[n - 1]) !== s.length - parts[n - 1].length) return false;
    var rest = s.slice(parts[0].length);
    if (rest.length < parts[n - 1].length) return false;
    rest = rest.slice(0, rest.length - parts[n - 1].length);
    for (var i = 1; i < n - 1; i++) {
      var idx = rest.indexOf(parts[i]);
      if (idx < 0) return false;
      rest = rest.slice(idx + parts[i].length);
    }
    return true;
  }

  // ---- screen 1: runtime picker (§27.4) ----
  function renderRuntimePicker() {
    clear(app);
    app.appendChild(el("h1", { text: "Choose a runtime" }));
    var list = el("div", {}, []);
    app.appendChild(list);
    list.appendChild(el("p", { class: "notice", text: "Loading runtimes." }));

    if (state.config.authMode === "apiKey") {
      app.insertBefore(renderApiKeyForm(), list);
    }

    fetchRuntimes()
      .then(function (runtimes) {
        clear(list);
        runtimes = runtimes.filter(function (rt) {
          return runtimeAllowed(rt.id || rt.name || "");
        });
        if (!runtimes.length) {
          list.appendChild(el("p", { class: "notice", text: "No runtimes are visible to this caller." }));
          return;
        }
        runtimes.forEach(function (rt) {
          // §27.4 item 1: each entry shows the runtime id, version, and
          // description. v1's only §5.1 version-bearing field is
          // minPlatformVersion, so the version line reports it when present
          // and is omitted otherwise (no "version unknown" filler).
          var card = [
            el("div", { class: "runtime-id", text: rt.id || rt.name || "(unnamed)" }),
          ];
          if (rt.minPlatformVersion) {
            card.push(el("div", { class: "meta", text: "requires platform ≥ " + rt.minPlatformVersion }));
          }
          card.push(el("p", { text: rt.description || "" }));
          card.push(el("button", {
            text: "use this runtime",
            onclick: function () {
              state.runtime = rt;
              renderSessionConfig();
            },
          }));
          list.appendChild(el("div", { class: "runtime-card" }, card));
        });
      })
      .catch(function (e) {
        clear(list);
        list.appendChild(el("p", { class: "err", text: "Runtime discovery failed: " + e.message }));
      });
  }

  // §27.5: runtime discovery is GET /v1/runtimes, the public surface.
  function fetchRuntimes() {
    return mintBearer().then(function (bearer) {
      return fetch("/v1/runtimes", {
        headers: { Authorization: "Bearer " + bearer },
        credentials: "same-origin",
      }).then(function (r) {
        if (!r.ok) throw new Error("GET /v1/runtimes returned " + r.status);
        return r.json();
      }).then(function (body) {
        // The discovery surface returns either a bare array or an
        // object with a runtimes field.
        if (Array.isArray(body)) return body;
        return body.runtimes || body.items || [];
      });
    });
  }

  // The apiKey-mode paste form. The token is kept in sessionStorage
  // only (§27.3), never localStorage or a cookie.
  function renderApiKeyForm() {
    var existing = sessionStorage.getItem("lenny_playground_apikey");
    if (existing) state.apiKeyToken = existing;
    var input = el("input", {
      type: "password",
      placeholder: "operator-issued bearer token",
      value: existing || "",
    });
    return el("div", { class: "config-card" }, [
      el("h2", { text: "API key" }),
      el("p", { class: "notice", text: "Paste an operator-issued user bearer token. It is stored in this tab only." }),
      input,
      el("div", { class: "row" }, [
        el("button", {
          text: "save token",
          onclick: function () {
            state.apiKeyToken = input.value.trim();
            sessionStorage.setItem("lenny_playground_apikey", state.apiKeyToken);
            state.bearer = null;
            renderRuntimePicker();
          },
        }),
      ]),
    ]);
  }

  // ---- screen 2: session configuration (§27.4) ----
  function renderSessionConfig() {
    clear(app);
    app.appendChild(el("h1", { text: "Configure session: " + (state.runtime.id || state.runtime.name) }));

    // §27.4 item 2: the runtime-options editor is a form generated from
    // the runtime's runtimeOptionsSchema (§5.1 / §14). When the runtime
    // declares no schema the SPA falls back to a free-form JSON editor; in
    // both cases the gateway validates the runtimeOptions against the
    // schema at session create.
    var schema = parseSchema(state.runtime.runtimeOptionsSchema);
    var optionsEditor = schema ? renderSchemaForm(schema) : renderRawOptions();

    var labelsField = el("input", {
      type: "text",
      placeholder: "key=value,key2=value2",
    });
    var planField = el("input", { type: "file", accept: ".tar,.tar.gz,.tgz" });
    var delegationField = el("input", { type: "text", placeholder: "delegation policy id (optional)" });
    var errLine = el("p", { class: "err" }, []);

    var card = el("div", { class: "config-card" }, [
      el("h2", { text: "Runtime options" }),
      el("p", { class: "notice", text: schema
        ? "Fields generated from the runtime's runtimeOptionsSchema; the gateway validates them at session create."
        : "This runtime declares no runtimeOptionsSchema. Enter JSON; the gateway validates it at session create." }),
      optionsEditor.node,
      el("label", { text: "Workspace plan tarball (optional)" }),
      planField,
      el("label", { text: "Delegation policy (optional, requires scope)" }),
      delegationField,
      el("label", { text: "Session labels" }),
      labelsField,
      errLine,
      el("div", { class: "row" }, [
        el("button", { text: "back", class: "secondary", onclick: renderRuntimePicker }),
        el("button", {
          text: "create session",
          onclick: function () {
            errLine.textContent = "";
            var options;
            try {
              options = optionsEditor.collect();
            } catch (e) {
              errLine.textContent = e.message;
              return;
            }
            createSession({
              runtimeRef: state.runtime.id || state.runtime.name,
              runtimeOptions: options,
              labels: parseLabels(labelsField.value),
              delegationPolicyId: delegationField.value.trim() || undefined,
            }, errLine);
          },
        }),
      ]),
    ]);
    app.appendChild(card);
  }

  // parseSchema returns the runtimeOptionsSchema as a parsed object when it
  // describes a JSON-Schema object with a properties map, or null when the
  // runtime declares no usable schema (the caller then falls back to the
  // free-form JSON editor). The discovery response may carry the schema as
  // a JSON string or as an already-parsed object.
  function parseSchema(raw) {
    if (!raw) return null;
    var schema = raw;
    if (typeof raw === "string") {
      try {
        schema = JSON.parse(raw);
      } catch (e) {
        return null;
      }
    }
    if (!schema || typeof schema !== "object") return null;
    if (!schema.properties || typeof schema.properties !== "object") return null;
    return schema;
  }

  // renderRawOptions is the no-schema fallback: a JSON textarea whose
  // collect() parses the entered text. It preserves the pre-form behavior
  // for runtimes that declare no runtimeOptionsSchema.
  function renderRawOptions() {
    var field = el("textarea", { placeholder: "{ }", text: "{}" });
    return {
      node: field,
      collect: function () {
        try {
          return JSON.parse(field.value || "{}");
        } catch (e) {
          throw new Error("runtime options is not valid JSON");
        }
      },
    };
  }

  // renderSchemaForm builds the §27.4 item 2 schema-driven form from a
  // JSON-Schema object. It renders one control per declared property:
  // an enum becomes a <select>, a boolean a checkbox, a number/integer a
  // numeric input, and everything else a text input. Property title and
  // description annotate the control; a property listed in the schema's
  // `required` array is marked and validated. collect() coerces each
  // control back to its declared type and rejects a missing required value.
  function renderSchemaForm(schema) {
    var required = {};
    (Array.isArray(schema.required) ? schema.required : []).forEach(function (k) {
      required[k] = true;
    });
    var fields = [];
    var container = el("div", { class: "schema-form" }, []);

    Object.keys(schema.properties).forEach(function (name) {
      var prop = schema.properties[name] || {};
      var type = prop.type || (prop.enum ? "string" : "string");
      var isReq = !!required[name];
      var labelText = (prop.title || name) + (isReq ? " *" : "");
      var control;

      if (Array.isArray(prop.enum)) {
        control = el("select", {}, prop.enum.map(function (opt) {
          return el("option", { value: String(opt), text: String(opt) });
        }));
        if (prop["default"] != null) control.value = String(prop["default"]);
      } else if (type === "boolean") {
        control = el("input", { type: "checkbox" });
        if (prop["default"] === true) control.checked = true;
      } else if (type === "number" || type === "integer") {
        control = el("input", { type: "number" });
        if (prop["default"] != null) control.value = String(prop["default"]);
      } else {
        control = el("input", { type: "text" });
        if (prop["default"] != null) control.value = String(prop["default"]);
      }

      container.appendChild(el("label", { text: labelText }));
      if (prop.description) {
        container.appendChild(el("p", { class: "notice", text: prop.description }));
      }
      container.appendChild(control);
      fields.push({ name: name, type: type, isEnum: Array.isArray(prop.enum), required: isReq, control: control });
    });

    return {
      node: container,
      collect: function () {
        var out = {};
        fields.forEach(function (f) {
          if (f.type === "boolean") {
            out[f.name] = f.control.checked;
            return;
          }
          var raw = f.control.value;
          if (raw === "" || raw == null) {
            if (f.required) throw new Error("the required option \"" + f.name + "\" is empty");
            return;
          }
          if (!f.isEnum && (f.type === "number" || f.type === "integer")) {
            var num = Number(raw);
            if (isNaN(num)) throw new Error("the option \"" + f.name + "\" must be a number");
            out[f.name] = f.type === "integer" ? Math.trunc(num) : num;
            return;
          }
          out[f.name] = raw;
        });
        return out;
      },
    };
  }

  function parseLabels(raw) {
    var labels = {};
    (raw || "").split(",").forEach(function (pair) {
      pair = pair.trim();
      if (!pair) return;
      var i = pair.indexOf("=");
      if (i > 0) labels[pair.slice(0, i).trim()] = pair.slice(i + 1).trim();
    });
    return labels;
  }

  // §27.5: session creation is POST /v1/sessions, the public surface.
  function createSession(payload, errLine) {
    mintBearer()
      .then(function (bearer) {
        return fetch("/v1/sessions", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: "Bearer " + bearer,
          },
          credentials: "same-origin",
          body: JSON.stringify(payload),
        });
      })
      .then(function (r) {
        return r.json().then(function (body) {
          if (!r.ok) {
            throw new Error((body.error && body.error.message) || ("POST /v1/sessions returned " + r.status));
          }
          state.sessionId = body.id || body.sessionId;
          renderChat();
        });
      })
      .catch(function (e) {
        errLine.textContent = e.message;
      });
  }

  // ---- screen 3: chat (§27.4) ----
  function renderChat() {
    clear(app);
    state.frames = [];
    app.appendChild(el("h1", { text: "Session " + state.sessionId }));

    var log = el("div", { class: "chat-log" }, []);
    var input = el("input", { type: "text", placeholder: "message the agent" });
    var framePanel = el("pre", { text: "(no frames yet)" });
    var frameWrap = el("details", { class: "frame-inspector" }, [
      el("summary", { text: "raw MCP frame inspector" }),
      framePanel,
    ]);

    function appendMsg(kind, who, text) {
      var m = el("div", { class: "msg " + kind }, [
        el("span", { class: "who", text: who }),
        document.createTextNode(text),
      ]);
      log.appendChild(m);
      log.scrollTop = log.scrollHeight;
    }
    function recordFrame(direction, frame) {
      // §27.9: the gateway redacts frames before sending them; this
      // panel displays whatever the gateway delivered.
      state.frames.push(direction + " " + frame);
      framePanel.textContent = state.frames.slice(-40).join("\n");
    }

    // §27.4 item 3: the "Copy as client SDK snippet" control emits
    // equivalent code in Go, Python, or TypeScript. The language picker
    // selects which template copySnippet renders.
    var snippetLang = el("select", {}, [
      el("option", { value: "go", text: "Go" }),
      el("option", { value: "python", text: "Python" }),
      el("option", { value: "typescript", text: "TypeScript" }),
    ]);

    var card = el("div", { class: "chat-card" }, [
      el("div", { class: "row" }, [
        el("button", { text: "interrupt", class: "secondary", onclick: function () { sendControl("interrupt"); } }),
        el("button", { text: "cancel", class: "secondary", onclick: function () { sendControl("cancel"); } }),
        snippetLang,
        el("button", { text: "copy SDK snippet", class: "secondary", onclick: function () { copySnippet(snippetLang.value); } }),
        el("button", { text: "new session", class: "secondary", onclick: renderRuntimePicker }),
      ]),
      log,
      el("div", { class: "row" }, [
        input,
        el("button", {
          text: "send",
          onclick: function () {
            var text = input.value.trim();
            if (!text) return;
            input.value = "";
            appendMsg("user", "you", text);
            sendMessage(text, recordFrame, appendMsg);
          },
        }),
      ]),
      frameWrap,
    ]);
    app.appendChild(card);

    openWebSocket(appendMsg, recordFrame);
  }

  // §27.3.1 step 3: the MCP WebSocket upgrade carries the bearer. A
  // browser cannot set an Authorization header on a WebSocket
  // upgrade, so the bearer rides the Sec-WebSocket-Protocol carrier
  // lenny.bearer.<token> defined for this purpose.
  function openWebSocket(appendMsg, recordFrame) {
    mintBearer()
      .then(function (bearer) {
        var scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
        var url = scheme + "//" + window.location.host + (state.config.wsPath || "/mcp/v1/ws");
        var ws = new WebSocket(url, ["lenny.mcp.v1", "lenny.bearer." + bearer]);
        state.ws = ws;
        ws.onopen = function () {
          appendMsg("event", "connection", "MCP WebSocket open");
        };
        ws.onerror = function () {
          appendMsg("error", "connection", "MCP WebSocket error");
        };
        ws.onclose = function (ev) {
          if (ev.code === 4401) {
            appendMsg("error", "connection", "bearer revoked; re-authenticate");
          } else {
            appendMsg("event", "connection", "MCP WebSocket closed (" + ev.code + ")");
          }
        };
        ws.onmessage = function (ev) {
          recordFrame("<=", ev.data);
          dispatchFrame(ev.data, appendMsg);
        };
      })
      .catch(function (e) {
        appendMsg("error", "connection", "could not open the chat stream: " + e.message);
      });
  }

  // dispatchFrame renders an inbound MCP frame: assistant messages,
  // tool-call events, delegation events, and errors (§27.4).
  function dispatchFrame(raw, appendMsg) {
    var frame;
    try {
      frame = JSON.parse(raw);
    } catch (e) {
      appendMsg("event", "frame", raw);
      return;
    }
    if (frame.error) {
      appendMsg("error", "error", JSON.stringify(frame.error));
      return;
    }
    var result = frame.result || frame.params || {};
    if (result.type === "tool_call" || result.method === "tools/call") {
      appendMsg("event", "tool call", JSON.stringify(result));
    } else if (result.type === "delegation" || (result.method || "").indexOf("delegat") >= 0) {
      appendMsg("event", "delegation", JSON.stringify(result));
    } else if (result.message || result.text || result.content) {
      appendMsg("agent", "agent", result.message || result.text || JSON.stringify(result.content));
    } else {
      appendMsg("event", "frame", raw);
    }
  }

  // sendMessage sends a chat message over the MCP WebSocket as a
  // JSON-RPC tools/call for the session-message tool.
  function sendMessage(text, recordFrame, appendMsg) {
    if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
      appendMsg("error", "connection", "the chat stream is not open");
      return;
    }
    // §27.5: send_message is the registered §15.2 tool; its schema names
    // the target as `to` and the body as `message` (§8.5 line 537).
    var frame = JSON.stringify({
      jsonrpc: "2.0",
      id: "msg-" + Date.now(),
      method: "tools/call",
      params: { name: "lenny/send_message", arguments: { to: state.sessionId, message: text } },
    });
    state.ws.send(frame);
    recordFrame("=>", frame);
  }

  function sendControl(kind) {
    if (!state.ws || state.ws.readyState !== WebSocket.OPEN) return;
    // §15.2 lines 1295/1303: the registered control tools are
    // interrupt_session and cancel_session.
    var tool = kind === "interrupt" ? "lenny/interrupt_session" : "lenny/cancel_session";
    state.ws.send(JSON.stringify({
      jsonrpc: "2.0",
      id: kind + "-" + Date.now(),
      method: "tools/call",
      params: { name: tool, arguments: { sessionId: state.sessionId } },
    }));
  }

  // §27.6: on browser close the client sends session.cancel with the
  // playground_client_closed reason as a best-effort hint.
  window.addEventListener("beforeunload", function () {
    if (state.ws && state.ws.readyState === WebSocket.OPEN) {
      try {
        state.ws.send(JSON.stringify({
          jsonrpc: "2.0",
          method: "tools/call",
          params: {
            name: "lenny/cancel_session",
            arguments: { sessionId: state.sessionId, reason: "playground_client_closed" },
          },
        }));
      } catch (e) {
        // Best-effort: a dropped socket falls back to the idle path.
      }
    }
  });

  // §27.4 item 3 / §27.9: the "Copy as client SDK snippet" feature emits
  // equivalent code in Go, Python, or TypeScript. Every template
  // references the gateway URL and bearer through environment variables
  // and never embeds a credential (§27.9 line 256).
  function copySnippet(lang) {
    var rt = state.runtime.id || state.runtime.name;
    var snippet = sdkSnippet(lang || "go", rt);
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(snippet);
    }
    alert("SDK snippet copied to the clipboard (" + snippetLangLabel(lang) + ").");
  }

  function snippetLangLabel(lang) {
    if (lang === "python") return "Python";
    if (lang === "typescript") return "TypeScript";
    return "Go";
  }

  function sdkSnippet(lang, rt) {
    var q = JSON.stringify(rt);
    if (lang === "python") {
      return (
        "# Python — lenny client SDK\n" +
        "import os\n" +
        "from lenny import Client\n" +
        "\n" +
        "client = Client(\n" +
        "    base_url=os.environ['LENNY_GATEWAY_URL'],\n" +
        "    token=os.environ['LENNY_BEARER_TOKEN'],  # from your OIDC flow\n" +
        ")\n" +
        "session = client.create_session(runtime=" + q + ")\n" +
        "for event in session.send('hello'):\n" +
        "    print(event)\n"
      );
    }
    if (lang === "typescript") {
      return (
        "// TypeScript — lenny client SDK\n" +
        "import { Client } from \"@lennylabs/client\";\n" +
        "\n" +
        "const client = new Client({\n" +
        "  baseUrl: process.env.LENNY_GATEWAY_URL!,\n" +
        "  token: process.env.LENNY_BEARER_TOKEN!, // from your OIDC flow\n" +
        "});\n" +
        "const session = await client.createSession({ runtime: " + q + " });\n" +
        "for await (const event of session.send(\"hello\")) {\n" +
        "  console.log(event);\n" +
        "}\n"
      );
    }
    return (
      "// Go — lenny client SDK\n" +
      "package main\n" +
      "\n" +
      "import (\n" +
      "\t\"context\"\n" +
      "\t\"fmt\"\n" +
      "\t\"os\"\n" +
      "\n" +
      "\tlenny \"github.com/lennylabs/lenny/sdks/client/go\"\n" +
      ")\n" +
      "\n" +
      "func main() {\n" +
      "\tclient := lenny.New(os.Getenv(\"LENNY_GATEWAY_URL\"), os.Getenv(\"LENNY_BEARER_TOKEN\")) // token from your OIDC flow\n" +
      "\tsession, err := client.CreateSession(context.Background(), lenny.CreateSessionRequest{Runtime: " + q + "})\n" +
      "\tif err != nil {\n" +
      "\t\tpanic(err)\n" +
      "\t}\n" +
      "\tfor event := range session.Send(context.Background(), \"hello\") {\n" +
      "\t\tfmt.Println(event)\n" +
      "\t}\n" +
      "}\n"
    );
  }

  start();
})();
