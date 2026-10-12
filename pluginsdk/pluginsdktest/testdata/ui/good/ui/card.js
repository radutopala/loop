import { connect } from "./bridge.js";

const bridge = await connect();
const ctx = await bridge.call("context.get");
document.getElementById("text").textContent = `${ctx.card}: ${ctx.data.total}`;
await bridge.call("ui.resize", { height: 120 });
