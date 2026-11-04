#!/usr/bin/env node
// Records what the dev app does while you click around, without touching it:
// each API request, how long each "Loading" stayed up, and periodic
// screenshots. Needs `make app-dev` running (see LOOP_DEBUG_PORT in the README).
//
//   node scripts/record-ui.mjs [--seconds 60] [--out DIR] [--no-screenshots]
//
// Ctrl-C ends the recording early. Sampling costs the page a little: the
// numbers are good to about 50 ms and screenshots are taken every 500 ms.
import { mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parseArgs } from "node:util";

const { values: args } = parseArgs({
  options: {
    seconds: { type: "string", default: "60" },
    out: { type: "string", default: join(tmpdir(), `loop-ui-recording-${Date.now()}`) },
    "no-screenshots": { type: "boolean", default: false },
  },
});
const debugPort = process.env.LOOP_DEBUG_PORT || "9223";
const seconds = Number(args.seconds);
if (!(seconds > 0)) {
  console.error(`--seconds must be a positive number, got "${args.seconds}"`);
  process.exit(1);
}

let targets;
try {
  targets = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`)).json();
} catch {
  console.error(`Nothing is listening on debug port ${debugPort}. Start the app with \`make app-dev\` first.`);
  process.exit(1);
}
const pages = targets.filter((t) => t.type === "page");
const page = pages.find((t) => t.url.startsWith("http://localhost:")) ?? pages[0];
if (!page) {
  console.error("The app has no window open.");
  process.exit(1);
}

const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  ws.onopen = resolve;
  ws.onerror = () => reject(new Error(`Can't attach to ${page.url}`));
});

const samples = [];
const requests = new Map();
const pending = new Map();
let nextId = 0;

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.id) {
    pending.get(msg.id)?.(msg.result);
    pending.delete(msg.id);
    return;
  }
  const p = msg.params;
  switch (msg.method) {
    case "Runtime.bindingCalled":
      if (p.name === "__loopRecordSample") samples.push(JSON.parse(p.payload));
      break;
    case "Network.requestWillBeSent":
      if (p.request.method !== "OPTIONS" && !requests.has(p.requestId) && new URL(p.request.url).pathname.startsWith("/api/")) {
        requests.set(p.requestId, { url: p.request.url, method: p.request.method, at: Date.now() });
      }
      break;
    case "Network.responseReceived": {
      const r = requests.get(p.requestId);
      if (r) r.status = p.response.status;
      break;
    }
    case "Network.loadingFinished":
    case "Network.loadingFailed": {
      const r = requests.get(p.requestId);
      if (r) {
        r.done = Date.now();
        if (msg.method === "Network.loadingFailed") r.failed = p.canceled ? "canceled" : p.errorText;
      }
      break;
    }
  }
};

const send = (method, params = {}) =>
  new Promise((resolve) => {
    const id = ++nextId;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params }));
  });

// Runs in the page: reports the open chat and every visible "Loading" when
// either changes. Installed for reloads too, and it ends when the binding goes.
const sampler = `(() => {
  if (window !== window.top) return; // Also injected into embedded frames.
  clearInterval(window.__loopRecordTimer);
  let last = "";
  window.__loopRecordTimer = setInterval(() => {
    const labels = new Set();
    for (const el of document.querySelectorAll("body *")) {
      if (el.children.length === 0 && el.offsetParent && /^loading/i.test((el.textContent || "").trim())) {
        labels.add(el.closest("[data-testid]")?.dataset.testid || (el.textContent || "").trim());
      }
    }
    const sample = { chat: location.hash.slice(1), loading: [...labels] };
    const key = JSON.stringify(sample);
    if (key === last) return;
    last = key;
    window.__loopRecordSample?.(JSON.stringify({ ...sample, at: Date.now() }));
  }, 50);
})();`;

await send("Network.enable");
await send("Page.enable");
await send("Runtime.enable");
await send("Runtime.addBinding", { name: "__loopRecordSample" });
await send("Page.addScriptToEvaluateOnNewDocument", { source: sampler });
await send("Runtime.evaluate", { expression: sampler });

mkdirSync(args.out, { recursive: true });
const start = Date.now();
let stopped = false;
process.on("SIGINT", () => {
  stopped = true;
});
console.log(`Recording ${page.url} for ${seconds}s. Use the app now; Ctrl-C ends it early.`);

while (!stopped && Date.now() - start < seconds * 1000) {
  if (!args["no-screenshots"]) {
    const shot = await send("Page.captureScreenshot", { format: "jpeg", quality: 45, optimizeForSpeed: true });
    if (shot?.data) {
      const name = `${String(Date.now() - start).padStart(7, "0")}.jpg`;
      writeFileSync(join(args.out, name), Buffer.from(shot.data, "base64"));
    }
  }
  await new Promise((resolve) => setTimeout(resolve, 500));
}
await send("Runtime.evaluate", { expression: "clearInterval(window.__loopRecordTimer); delete window.__loopRecordTimer" });
const end = Date.now();
ws.close();

const rel = (t) => String(t - start).padStart(7);
const shortId = (id) => id.slice(0, 6);
const describe = (url) => {
  const u = new URL(url);
  for (const key of [...u.searchParams.keys()]) {
    if (/tok|sig|auth/i.test(key)) u.searchParams.set(key, "redacted");
  }
  return u.pathname.replace(/^\/api/, "").replace(/\/channels\/([0-9a-f]{6})[0-9a-f]*/, "/ch/$1") + decodeURIComponent(u.search);
};

// How long each label stayed up, per chat.
const spans = [];
const open = new Map();
for (const { at, chat, loading } of samples) {
  const now = new Set(loading.map((label) => `${chat} ${label}`));
  for (const [key, since] of open) {
    if (!now.has(key)) {
      spans.push({ key, since, until: at });
      open.delete(key);
    }
  }
  for (const key of now) if (!open.has(key)) open.set(key, at);
}
for (const [key, since] of open) spans.push({ key, since, until: end, unfinished: true });

const timeline = [
  ...samples.map((s) => ({ at: s.at, line: `chat ${shortId(s.chat)} loading=[${s.loading.join(", ")}]` })),
  ...[...requests.values()].map((r) => ({
    at: r.at,
    line: `${(r.done ? `${r.done - r.at}ms` : "open").padStart(8)} ${r.method} ${describe(r.url)} ${r.status ?? ""} ${r.failed ?? ""}`.trimEnd(),
  })),
].sort((a, b) => a.at - b.at);
writeFileSync(join(args.out, "timeline.txt"), `${timeline.map((row) => `${rel(row.at)} ${row.line}`).join("\n")}\n`);

const byDuration = (dur) => (a, b) => dur(b) - dur(a);
console.log(`\nLoading shown ${spans.length} times, longest first:`);
for (const s of [...spans].sort(byDuration((x) => x.until - x.since)).slice(0, 10)) {
  const [chat, ...label] = s.key.split(" ");
  console.log(`${String(s.until - s.since).padStart(7)}ms at ${rel(s.since).trim()}ms  chat ${shortId(chat)}  ${label.join(" ")}${s.unfinished ? " (still up)" : ""}`);
}
const finished = [...requests.values()].filter((r) => r.done);
console.log(`\n${requests.size} API requests, slowest first:`);
for (const r of finished.sort(byDuration((x) => x.done - x.at)).slice(0, 10)) {
  console.log(`${String(r.done - r.at).padStart(7)}ms at ${rel(r.at).trim()}ms  ${r.method} ${describe(r.url)}`);
}
console.log(`\nFull timeline: ${join(args.out, "timeline.txt")}`);
if (!args["no-screenshots"]) console.log(`Screenshots (named by ms since the start): ${args.out}`);
process.exit(0);
