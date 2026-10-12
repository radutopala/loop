import { describe, expect, it, vi } from "vitest";
import { emitPluginState, onPluginState } from "./pluginStateBus";

describe("pluginStateBus", () => {
  it("tells the other frames of the key, not the one that saved", () => {
    const a = vi.fn();
    const b = vi.fn();
    const other = vi.fn();
    const offA = onPluginState("k", a);
    const offB = onPluginState("k", b);
    const offOther = onPluginState("other", other);
    emitPluginState("k", { tab: 2 }, a);
    expect(a).not.toHaveBeenCalled();
    expect(b).toHaveBeenCalledWith({ tab: 2 });
    expect(other).not.toHaveBeenCalled();
    offB();
    emitPluginState("k", 3, vi.fn());
    expect(a).toHaveBeenCalledWith(3);
    expect(b).toHaveBeenCalledTimes(1);
    offA();
    offOther();
    emitPluginState("k", 4, vi.fn());
    expect(a).toHaveBeenCalledTimes(1);
  });
});
