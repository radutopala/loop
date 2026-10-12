// Draws each checked page in a sandboxed frame, as Loop does, answers its
// bridge with fakes and reports what happens to CheckUI.
(async () => {
  const METHOD_NOT_FOUND = -32601;
  const INVALID_PARAMS = -32602;
  const CALL_FAILED = -32000;
  const MAX_STATE_BYTES = 64 << 10;
  const THEME = {
    mode: "dark",
    vars: {
      "--bg": "#1e1e1e",
      "--surface": "#252526",
      "--fg": "#d4d4d4",
      "--muted": "#8b8b8b",
      "--border": "#3c3c3c",
      "--accent": "#0e639c",
      "--success": "#4ec9b0",
      "--danger": "#f14c4c",
      "--font": "system-ui, sans-serif",
      "--mono": "ui-monospace, monospace",
    },
  };

  const post = (path, body) => fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const report = (page, kind, message) => post("/report", { page, kind, message });

  class BridgeError extends Error {
    constructor(code, message) {
      super(message);
      this.code = code;
    }
  }
  const str = (params, key, required = true) => {
    const v = params[key];
    if (typeof v === "string" && (v !== "" || !required)) return v;
    if (v === undefined && !required) return "";
    throw new BridgeError(INVALID_PARAMS, `${key} must be a ${required ? "non-empty " : ""}string`);
  };

  const cfg = await (await fetch("/config")).json();
  const needs = (capability) => {
    if (!(cfg.capabilities || []).includes(capability)) throw new BridgeError(CALL_FAILED, `the plugin wasn't approved for ${capability}`);
  };

  const call = async (index, page, state, method, params) => {
    switch (method) {
      case "api.request": {
        const res = await post("/api", { method: str(params, "method"), path: str(params, "path"), body: params.body ?? null });
        if (!res.ok) throw new BridgeError(CALL_FAILED, await res.text());
        return res.json();
      }
      case "context.get": {
        const ctx = { pluginId: cfg.pluginId, panel: page.kind === "panel" ? page.id : "", channelId: "check", channelName: "check", dirPath: "/work" };
        return page.kind === "card" ? { ...ctx, card: page.id, data: page.data ?? null } : ctx;
      }
      case "state.get":
        return state.value;
      case "state.set": {
        const value = params.value ?? null;
        if (new TextEncoder().encode(JSON.stringify(value)).length > MAX_STATE_BYTES) throw new BridgeError(INVALID_PARAMS, "state is over 64 KB");
        state.value = value;
        return null;
      }
      case "theme.get":
        return THEME;
      case "settings.get":
        return cfg.settings;
      case "ui.notify":
        needs("ui.notify");
        str(params, "title");
        str(params, "body", false);
        return null;
      case "ui.open_file": {
        needs("ui.open_file");
        const line = params.line;
        if (line !== undefined && (typeof line !== "number" || !Number.isInteger(line) || line < 1)) throw new BridgeError(INVALID_PARAMS, "line must be a positive integer");
        str(params, "path");
        return null;
      }
      case "ui.resize": {
        if (page.kind !== "card") throw new BridgeError(CALL_FAILED, "only a card sizes itself; a panel fills its pane");
        const height = params.height;
        if (typeof height !== "number" || !Number.isFinite(height) || height <= 0) throw new BridgeError(INVALID_PARAMS, "height must be a positive number");
        return null;
      }
      default:
        throw new BridgeError(METHOD_NOT_FOUND, `no method ${method}`);
    }
  };

  const frames = cfg.pages.map((page, index) => {
    const state = { value: null };
    const frame = document.createElement("iframe");
    frame.setAttribute("sandbox", "allow-scripts allow-forms");
    frame.title = `${page.kind} ${page.id}`;
    frame.addEventListener("load", () => {
      const channel = new MessageChannel();
      const port = channel.port1;
      port.onmessage = async (ev) => {
        const msg = ev.data;
        if (!msg || typeof msg !== "object" || typeof msg.method !== "string") return;
        report(index, "connected", "");
        const params = msg.params && typeof msg.params === "object" ? msg.params : {};
        try {
          const result = await call(index, page, state, msg.method, params);
          if (msg.id !== undefined && msg.id !== null) port.postMessage({ jsonrpc: "2.0", id: msg.id, result: result ?? null });
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          report(index, "bridge", `${msg.method}: ${message}`);
          if (msg.id === undefined || msg.id === null) return;
          port.postMessage({ jsonrpc: "2.0", id: msg.id, error: { code: err instanceof BridgeError ? err.code : CALL_FAILED, message } });
        }
      };
      frame.contentWindow.postMessage({ type: "loop-plugin:init" }, "*", [channel.port2]);
      report(index, "loaded", "");
    });
    frame.src = page.url;
    document.body.appendChild(frame);
    return frame;
  });

  addEventListener("message", (ev) => {
    const d = ev.data;
    if (!d || d.type !== "loop-check:probe") return;
    const index = frames.findIndex((f) => f.contentWindow === ev.source);
    if (index >= 0) report(index, d.kind, d.message);
  });
})();
