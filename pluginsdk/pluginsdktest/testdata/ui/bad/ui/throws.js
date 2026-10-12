addEventListener("message", (ev) => {
  if (!ev.data || ev.data.type !== "loop-plugin:init") return;
  const port = ev.ports[0];
  port.onmessage = () => {};
  port.postMessage({ jsonrpc: "2.0", id: 1, method: "ui.open_file", params: { path: "a.go" } });
  throw new Error("boom");
});
