// A small client of Loop's bridge: waits for the port and calls methods.
export function connect() {
  return new Promise((resolve) => {
    const onMessage = (ev) => {
      if (!ev.data || ev.data.type !== "loop-plugin:init" || !ev.ports[0]) return;
      removeEventListener("message", onMessage);
      const port = ev.ports[0];
      const pending = new Map();
      let next = 1;
      port.onmessage = (e) => {
        const p = pending.get(e.data.id);
        if (!p) return;
        pending.delete(e.data.id);
        if (e.data.error) p.reject(new Error(e.data.error.message));
        else p.resolve(e.data.result);
      };
      resolve({
        call: (method, params = {}) =>
          new Promise((res, rej) => {
            const id = next++;
            pending.set(id, { resolve: res, reject: rej });
            port.postMessage({ jsonrpc: "2.0", id, method, params });
          }),
      });
    };
    addEventListener("message", onMessage);
  });
}
