addEventListener("message", (ev) => {
  if (ev.data && ev.data.type === "loop-plugin:init") ev.ports[0].start();
});
Promise.reject(new Error("nobody waits"));
await fetch("https://example.com/data.json");
