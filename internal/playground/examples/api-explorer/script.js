import React from "react";
import { createRoot } from "react-dom/client";

var h = React.createElement;
var { useState, useEffect, useRef, useCallback } = React;

var API_BASE = "http://localhost:8222";

var ENDPOINTS = [
  { group: "Health", items: [
    { method: "GET", path: "/api/health", desc: "Health check" },
    { method: "GET", path: "/api/readme", desc: "Get README" },
  ]},
  { group: "Channels", items: [
    { method: "GET", path: "/api/channels", desc: "Search channels", params: [{ name: "query", type: "query" }, { name: "platform", type: "query" }] },
    { method: "POST", path: "/api/channels", desc: "Ensure channel", body: { dir_path: "/path/to/project" } },
    { method: "POST", path: "/api/channels/create", desc: "Create channel", body: { name: "my-channel", author_id: "user1" } },
    { method: "POST", path: "/api/channels/ensure-all", desc: "Ensure all platforms", body: { dir_path: "/path/to/project" } },
    { method: "DELETE", path: "/api/channels/{id}", desc: "Delete channel", params: [{ name: "id", type: "path" }] },
  ]},
  { group: "Threads", items: [
    { method: "POST", path: "/api/threads", desc: "Create thread", body: { channel_id: "", name: "new-thread" } },
    { method: "DELETE", path: "/api/threads/{id}", desc: "Delete thread", params: [{ name: "id", type: "path" }] },
  ]},
  { group: "Messages", items: [
    { method: "POST", path: "/api/messages", desc: "Send message", body: { channel_id: "", content: "Hello!" } },
    { method: "GET", path: "/api/channels/{id}/messages", desc: "List messages", params: [{ name: "id", type: "path" }, { name: "limit", type: "query" }, { name: "cursor", type: "query" }] },
    { method: "GET", path: "/api/messages/search", desc: "Search messages", params: [{ name: "q", type: "query" }, { name: "limit", type: "query" }] },
  ]},
  { group: "Tasks", items: [
    { method: "GET", path: "/api/tasks", desc: "List tasks", params: [{ name: "channel_id", type: "query" }] },
    { method: "POST", path: "/api/tasks", desc: "Create task", body: { channel_id: "", schedule: "*/5 * * * *", type: "message", prompt: "do something" } },
    { method: "GET", path: "/api/tasks/{id}", desc: "Get task", params: [{ name: "id", type: "path" }] },
    { method: "PATCH", path: "/api/tasks/{id}", desc: "Update task", params: [{ name: "id", type: "path" }], body: { enabled: true } },
    { method: "DELETE", path: "/api/tasks/{id}", desc: "Delete task", params: [{ name: "id", type: "path" }] },
  ]},
  { group: "Agents", items: [
    { method: "GET", path: "/api/agents", desc: "List agents", params: [{ name: "channel_id", type: "query" }] },
    { method: "POST", path: "/api/agents", desc: "Register agent", body: { channel_id: "", name: "my-agent" } },
    { method: "PATCH", path: "/api/agents/{id}", desc: "Update agent", params: [{ name: "id", type: "path" }], body: { status: "working", work_summary: "doing stuff" } },
    { method: "DELETE", path: "/api/agents/{id}", desc: "Delete agent", params: [{ name: "id", type: "path" }] },
  ]},
  { group: "Config", items: [
    { method: "GET", path: "/api/config", desc: "Get global config" },
    { method: "PUT", path: "/api/config", desc: "Update global config", body: {} },
    { method: "GET", path: "/api/config/schema", desc: "Get config schema" },
    { method: "GET", path: "/api/config/project", desc: "Get project config", params: [{ name: "channel_id", type: "query" }] },
    { method: "PUT", path: "/api/config/project", desc: "Update project config", params: [{ name: "channel_id", type: "query" }], body: {} },
  ]},
  { group: "Sessions", items: [
    { method: "GET", path: "/api/channels/{id}/sessions", desc: "List sessions", params: [{ name: "id", type: "path" }] },
  ]},
  { group: "Files", items: [
    { method: "GET", path: "/api/channels/{id}/roots", desc: "List workspace roots", params: [{ name: "id", type: "path" }] },
    { method: "GET", path: "/api/channels/{id}/files", desc: "List files", params: [{ name: "id", type: "path" }, { name: "path", type: "query" }] },
    { method: "GET", path: "/api/channels/{id}/file", desc: "Read file", params: [{ name: "id", type: "path" }, { name: "path", type: "query" }] },
    { method: "PUT", path: "/api/channels/{id}/file", desc: "Write file", params: [{ name: "id", type: "path" }, { name: "path", type: "query" }], body: { content: "" } },
    { method: "DELETE", path: "/api/channels/{id}/file", desc: "Delete file", params: [{ name: "id", type: "path" }, { name: "path", type: "query" }] },
  ]},
  { group: "Git", items: [
    { method: "GET", path: "/api/channels/{id}/diff", desc: "Git diff", params: [{ name: "id", type: "path" }, { name: "source", type: "query" }, { name: "target", type: "query" }] },
    { method: "GET", path: "/api/channels/{id}/branches", desc: "List branches", params: [{ name: "id", type: "path" }] },
    { method: "POST", path: "/api/channels/{id}/branches/switch", desc: "Switch branch", params: [{ name: "id", type: "path" }], body: { branch: "main" } },
    { method: "POST", path: "/api/channels/{id}/branches/create", desc: "Create branch", params: [{ name: "id", type: "path" }], body: { name: "feature-x", from: "main" } },
  ]},
  { group: "Worktrees", items: [
    { method: "POST", path: "/api/worktrees", desc: "Create worktree", body: { channel_id: "", branch: "" } },
    { method: "POST", path: "/api/worktrees/import", desc: "Import worktree", body: { channel_id: "", path: "" } },
  ]},
  { group: "Image", items: [
    { method: "GET", path: "/api/image/status", desc: "Image status" },
    { method: "POST", path: "/api/image/rebuild", desc: "Rebuild image" },
    { method: "DELETE", path: "/api/image", desc: "Remove image" },
  ]},
  { group: "Memory", items: [
    { method: "POST", path: "/api/memory/search", desc: "Search memory", body: { query: "", channel_id: "", top_k: 5 } },
    { method: "POST", path: "/api/memory/index", desc: "Index memory", body: { channel_id: "" } },
    { method: "GET", path: "/api/memory/files", desc: "List memory files", params: [{ name: "channel_id", type: "query" }] },
    { method: "GET", path: "/api/memory/file", desc: "Read memory file", params: [{ name: "path", type: "query" }, { name: "channel_id", type: "query" }] },
    { method: "PUT", path: "/api/memory/file", desc: "Write memory file", params: [{ name: "path", type: "query" }, { name: "channel_id", type: "query" }], body: { content: "" } },
  ]},
  { group: "Playground", items: [
    { method: "GET", path: "/api/playground/items", desc: "List playgrounds" },
    { method: "GET", path: "/api/playground", desc: "Get playground", params: [{ name: "name", type: "query" }] },
    { method: "PUT", path: "/api/playground", desc: "Create/update playground", params: [{ name: "name", type: "query" }], body: { html: "<div>hello</div>", title: "My App", description: "A demo app" } },
    { method: "DELETE", path: "/api/playground", desc: "Delete playground", params: [{ name: "name", type: "query" }] },
    { method: "GET", path: "/api/playground/files", desc: "List playground files", params: [{ name: "name", type: "query" }] },
    { method: "GET", path: "/api/playground/file", desc: "Read playground file", params: [{ name: "name", type: "query" }, { name: "path", type: "query" }] },
    { method: "PUT", path: "/api/playground/file", desc: "Write playground file", params: [{ name: "name", type: "query" }, { name: "path", type: "query" }] },
    { method: "DELETE", path: "/api/playground/file", desc: "Delete playground file", params: [{ name: "name", type: "query" }, { name: "path", type: "query" }] },
  ]},
  { group: "Browser", items: [
    { method: "POST", path: "/api/browser/action", desc: "Browser action", body: { action: "navigate", url: "https://example.com" } },
    { method: "POST", path: "/api/browser/mode", desc: "Switch browser mode", body: { mode: "host" } },
  ]},
  { group: "Commands", items: [
    { method: "POST", path: "/api/commands", desc: "Execute command", body: { channel_id: "", command: "status" } },
  ]},
];

var METHOD_COLORS = { GET: "#06d6a0", POST: "#ffd166", PUT: "#00d4ff", PATCH: "#c77dff", DELETE: "#ff3860" };

function App() {
  var [selected, setSelected] = useState(null);
  var [paramValues, setParamValues] = useState({});
  var [bodyText, setBodyText] = useState("");
  var [response, setResponse] = useState(null);
  var [loading, setLoading] = useState(false);
  var [status, setStatus] = useState(null);
  var [elapsed, setElapsed] = useState(null);
  var [filter, setFilter] = useState("");
  var [expandedGroups, setExpandedGroups] = useState(
    ENDPOINTS.reduce(function(acc, g) { acc[g.group] = true; return acc; }, {})
  );

  function selectEndpoint(ep) {
    setSelected(ep);
    setResponse(null);
    setStatus(null);
    setElapsed(null);
    setParamValues({});
    setBodyText(ep.body ? JSON.stringify(ep.body, null, 2) : "");
  }

  function toggleGroup(name) {
    setExpandedGroups(function(prev) {
      var next = Object.assign({}, prev);
      next[name] = !next[name];
      return next;
    });
  }

  async function sendRequest() {
    if (!selected) return;
    setLoading(true);
    setResponse(null);
    var start = performance.now();

    var path = selected.path;
    // Replace path params
    if (selected.params) {
      selected.params.forEach(function(p) {
        if (p.type === "path" && paramValues[p.name]) {
          path = path.replace("{" + p.name + "}", encodeURIComponent(paramValues[p.name]));
        }
      });
    }
    // Add query params
    var queryParts = [];
    if (selected.params) {
      selected.params.forEach(function(p) {
        if (p.type === "query" && paramValues[p.name]) {
          queryParts.push(encodeURIComponent(p.name) + "=" + encodeURIComponent(paramValues[p.name]));
        }
      });
    }
    if (queryParts.length > 0) path += "?" + queryParts.join("&");

    try {
      var opts = { method: selected.method, headers: {} };
      if (bodyText && selected.method !== "GET") {
        opts.headers["Content-Type"] = "application/json";
        opts.body = bodyText;
      }
      var resp = await fetch(API_BASE + path, opts);
      var end = performance.now();
      setElapsed(Math.round(end - start));
      setStatus(resp.status + " " + resp.statusText);

      var contentType = resp.headers.get("content-type") || "";
      var text;
      if (contentType.includes("json")) {
        var json = await resp.json();
        text = JSON.stringify(json, null, 2);
      } else {
        text = await resp.text();
      }
      setResponse(text);
    } catch (e) {
      setResponse("Error: " + e.message);
      setStatus("Error");
      setElapsed(Math.round(performance.now() - start));
    }
    setLoading(false);
  }

  var filteredEndpoints = ENDPOINTS.map(function(g) {
    if (!filter) return g;
    var items = g.items.filter(function(ep) {
      var s = filter.toLowerCase();
      return ep.path.toLowerCase().includes(s) || ep.desc.toLowerCase().includes(s) || ep.method.toLowerCase().includes(s);
    });
    if (items.length === 0) return null;
    return { group: g.group, items: items };
  }).filter(Boolean);

  return h("div", { style: S.layout },
    // Sidebar
    h("div", { style: S.sidebar },
      h("div", { style: S.sideHeader },
        h("div", { style: S.logo }, "Loop API"),
        h("input", {
          style: S.search,
          placeholder: "Filter endpoints...",
          value: filter,
          onChange: function(e) { setFilter(e.target.value); },
        })
      ),
      h("div", { style: S.sideList },
        filteredEndpoints.map(function(g) {
          return h("div", { key: g.group },
            h("div", {
              style: S.groupHeader,
              onClick: function() { toggleGroup(g.group); },
            },
              h("span", null, expandedGroups[g.group] ? "\u25BE" : "\u25B8"),
              " ", g.group,
              h("span", { style: S.groupCount }, g.items.length)
            ),
            expandedGroups[g.group] && g.items.map(function(ep, i) {
              var isSelected = selected === ep;
              return h("div", {
                key: i,
                style: Object.assign({}, S.epItem, isSelected ? S.epItemActive : {}),
                onClick: function() { selectEndpoint(ep); },
              },
                h("span", { style: Object.assign({}, S.methodBadge, { color: METHOD_COLORS[ep.method] }) }, ep.method),
                h("span", { style: S.epPath }, ep.path.replace("/api", ""))
              );
            })
          );
        })
      )
    ),
    // Main area
    h("div", { style: S.main },
      selected ? h("div", { style: S.mainInner },
        // URL bar
        h("div", { style: S.urlBar },
          h("span", { style: Object.assign({}, S.urlMethod, { background: METHOD_COLORS[selected.method] + "22", color: METHOD_COLORS[selected.method] }) }, selected.method),
          h("span", { style: S.urlPath }, API_BASE + selected.path),
          h("button", { style: S.sendBtn, onClick: sendRequest, disabled: loading }, loading ? "Sending..." : "Send")
        ),
        h("div", { style: S.desc }, selected.desc),
        // Params
        selected.params && selected.params.length > 0 && h("div", { style: S.section },
          h("div", { style: S.sectionTitle }, "Parameters"),
          selected.params.map(function(p) {
            return h("div", { key: p.name, style: S.paramRow },
              h("label", { style: S.paramLabel },
                h("span", { style: Object.assign({}, S.paramType, { color: p.type === "path" ? "#ff6b35" : "#00d4ff" }) }, p.type),
                " ", p.name
              ),
              h("input", {
                style: S.paramInput,
                value: paramValues[p.name] || "",
                placeholder: p.name,
                onChange: function(e) {
                  var v = {}; v[p.name] = e.target.value;
                  setParamValues(function(prev) { return Object.assign({}, prev, v); });
                },
              })
            );
          })
        ),
        // Body
        selected.body !== undefined && h("div", { style: S.section },
          h("div", { style: S.sectionTitle }, "Request Body (JSON)"),
          h("textarea", {
            style: S.bodyEditor,
            value: bodyText,
            onChange: function(e) { setBodyText(e.target.value); },
            spellCheck: false,
          })
        ),
        // Response
        h("div", { style: Object.assign({}, S.section, { flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }) },
          h("div", { style: S.responseHeader },
            h("span", { style: S.sectionTitle }, "Response"),
            status && h("span", { style: Object.assign({}, S.statusBadge, {
              color: status.startsWith("2") ? "#06d6a0" : status.startsWith("4") ? "#ffd166" : status === "Error" ? "#ff3860" : "#ccc",
            }) }, status),
            elapsed !== null && h("span", { style: S.elapsed }, elapsed + "ms")
          ),
          h("pre", { style: S.responsePre },
            response !== null ? response : h("span", { style: { color: "#444" } }, "Click Send to make a request")
          )
        )
      ) : h("div", { style: S.empty },
        h("div", { style: S.emptyIcon }, "\u26A1"),
        h("div", { style: S.emptyTitle }, "Loop API Explorer"),
        h("div", { style: S.emptyDesc }, "Select an endpoint from the sidebar to get started"),
        h("div", { style: S.emptyHint }, ENDPOINTS.reduce(function(n, g) { return n + g.items.length; }, 0) + " endpoints across " + ENDPOINTS.length + " groups")
      )
    )
  );
}

var S = {
  layout: { display: "flex", height: "100vh", overflow: "hidden" },
  sidebar: { width: 280, background: "#0d0d2b", borderRight: "1px solid #1a1a3e", display: "flex", flexDirection: "column", flexShrink: 0 },
  sideHeader: { padding: 12, borderBottom: "1px solid #1a1a3e" },
  logo: { fontSize: 16, fontWeight: "bold", color: "#fff", marginBottom: 8 },
  search: { width: "100%", padding: "6px 10px", background: "#16213e", border: "1px solid #2a2a4a", borderRadius: 4, color: "#ccc", fontSize: 12, outline: "none" },
  sideList: { flex: 1, overflowY: "auto", padding: "4px 0" },
  groupHeader: { padding: "6px 12px", fontSize: 11, fontWeight: "bold", color: "#6666aa", cursor: "pointer", userSelect: "none", display: "flex", alignItems: "center", gap: 4 },
  groupCount: { marginLeft: "auto", background: "#16213e", borderRadius: 8, padding: "0 6px", fontSize: 10, color: "#555" },
  epItem: { padding: "5px 12px 5px 20px", cursor: "pointer", display: "flex", alignItems: "center", gap: 8, fontSize: 11, borderLeft: "2px solid transparent" },
  epItemActive: { background: "#16213e", borderLeftColor: "#00d4ff" },
  methodBadge: { fontSize: 9, fontWeight: "bold", width: 40, textAlign: "center", flexShrink: 0 },
  epPath: { color: "#888", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" },
  main: { flex: 1, display: "flex", flexDirection: "column", overflow: "hidden" },
  mainInner: { flex: 1, display: "flex", flexDirection: "column", padding: 16, gap: 12, overflow: "hidden" },
  urlBar: { display: "flex", alignItems: "center", gap: 8, background: "#16213e", borderRadius: 8, padding: 4 },
  urlMethod: { padding: "6px 10px", borderRadius: 6, fontSize: 11, fontWeight: "bold" },
  urlPath: { flex: 1, fontSize: 12, color: "#aaa", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", padding: "0 8px" },
  sendBtn: { padding: "6px 20px", background: "#0066ff", border: "none", borderRadius: 6, color: "#fff", fontSize: 12, fontWeight: "bold", cursor: "pointer" },
  desc: { fontSize: 12, color: "#666", padding: "0 4px" },
  section: { },
  sectionTitle: { fontSize: 11, fontWeight: "bold", color: "#6666aa", marginBottom: 6, textTransform: "uppercase", letterSpacing: 1 },
  paramRow: { display: "flex", alignItems: "center", gap: 8, marginBottom: 4 },
  paramLabel: { fontSize: 12, color: "#888", width: 120, display: "flex", alignItems: "center", gap: 4 },
  paramType: { fontSize: 9, fontWeight: "bold" },
  paramInput: { flex: 1, padding: "5px 8px", background: "#16213e", border: "1px solid #2a2a4a", borderRadius: 4, color: "#ccc", fontSize: 12, outline: "none" },
  bodyEditor: { width: "100%", height: 120, padding: 10, background: "#0d0d2b", border: "1px solid #2a2a4a", borderRadius: 6, color: "#ccc", fontSize: 12, resize: "vertical", outline: "none", lineHeight: 1.5 },
  responseHeader: { display: "flex", alignItems: "center", gap: 12 },
  statusBadge: { fontSize: 12, fontWeight: "bold" },
  elapsed: { fontSize: 11, color: "#555" },
  responsePre: { flex: 1, overflow: "auto", padding: 12, background: "#0d0d2b", border: "1px solid #1a1a3e", borderRadius: 6, fontSize: 12, lineHeight: 1.5, whiteSpace: "pre-wrap", wordBreak: "break-word", color: "#aaa", margin: 0 },
  empty: { flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 8 },
  emptyIcon: { fontSize: 48 },
  emptyTitle: { fontSize: 20, fontWeight: "bold", color: "#fff" },
  emptyDesc: { fontSize: 13, color: "#666" },
  emptyHint: { fontSize: 11, color: "#444", marginTop: 8 },
};

createRoot(document.getElementById("root")).render(h(App));
console.log("API Explorer loaded");