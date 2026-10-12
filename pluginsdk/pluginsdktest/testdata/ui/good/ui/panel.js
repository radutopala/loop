import { connect } from "./bridge.js";

const bridge = await connect();
const ctx = await bridge.call("context.get");
const theme = await bridge.call("theme.get");
const settings = await bridge.call("settings.get");
for (const [k, v] of Object.entries(theme.vars)) document.documentElement.style.setProperty(k, v);
document.getElementById("title").textContent = `${settings.greeting} from ${ctx.panel}`;
const res = await bridge.call("api.request", { method: "POST", path: "/stats?n=1", body: { from: ctx.panel } });
document.getElementById("stats").textContent = JSON.stringify(res.body);
await bridge.call("state.set", { value: { seen: res.status } });
await bridge.call("ui.notify", { title: "Loaded" });
await bridge.call("ui.copy", { text: "copied" });
const data = await (await fetch("data.json")).json();
document.body.dataset.items = String(data.items.length);
