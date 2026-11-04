// @vitest-environment jsdom
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchPR } from "../api/git";
import { useChannelPR } from "./useChannelPR";

vi.mock("../api/git", () => ({ fetchPR: vi.fn() }));
vi.mock("../api/pr", () => ({ getPRStatus: vi.fn() }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function Probe() {
  useChannelPR("open", "feature");
  return null;
}

describe("useChannelPR", () => {
  afterEach(() => {
    vi.mocked(fetchPR).mockReset();
  });

  it("looks up at once without a delay, for the open chat's header", async () => {
    vi.mocked(fetchPR).mockResolvedValue({ present: false });
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(createElement(Probe)));
    expect(fetchPR).toHaveBeenCalledTimes(1);
    expect(fetchPR).toHaveBeenCalledWith("open");
    act(() => root.unmount());
  });
});
