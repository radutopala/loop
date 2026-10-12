// Runs first in a checked page and tells the host page what goes wrong in
// it, and when it takes the bridge's port.
(() => {
  const send = (kind, message) => parent.postMessage({ type: "loop-check:probe", kind, message: String(message) }, "*");

  addEventListener("securitypolicyviolation", (e) => {
    const where = e.sourceFile ? ` at ${e.sourceFile}:${e.lineNumber}` : "";
    send("csp", `${e.effectiveDirective} blocked ${e.blockedURI || "a load"}${where}`);
  });
  addEventListener("error", (e) => {
    const err = e.error;
    send("error", err && err.stack ? err.stack : `${e.message} at ${e.filename}:${e.lineno}`);
  });
  addEventListener("unhandledrejection", (e) => {
    const r = e.reason;
    send("error", `unhandled rejection: ${r && r.stack ? r.stack : r}`);
  });

  // The bridge's port, from Loop's init message; the page has taken it once
  // it starts it, by start() or by setting onmessage.
  const ports = new WeakSet();
  addEventListener("message", (e) => {
    if (e.data && e.data.type === "loop-plugin:init" && e.ports[0]) ports.add(e.ports[0]);
  });
  let connected = false;
  const taken = (port) => {
    if (!connected && ports.has(port)) {
      connected = true;
      send("connected", "");
    }
  };
  const start = MessagePort.prototype.start;
  MessagePort.prototype.start = function () {
    taken(this);
    return start.call(this);
  };
  const onmessage = Object.getOwnPropertyDescriptor(MessagePort.prototype, "onmessage");
  Object.defineProperty(MessagePort.prototype, "onmessage", {
    ...onmessage,
    set(v) {
      taken(this);
      onmessage.set.call(this, v);
    },
  });
})();
