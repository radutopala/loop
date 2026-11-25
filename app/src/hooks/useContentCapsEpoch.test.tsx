import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { clearContentCaps } from "../api/loopApi";
import { useContentCapsEpoch } from "./useContentCapsEpoch";

describe("useContentCapsEpoch", () => {
  it("changes each time every content link is forgotten", () => {
    const { result } = renderHook(() => useContentCapsEpoch());
    const first = result.current;
    act(() => clearContentCaps());
    expect(result.current).toBe(first + 1);
    act(() => clearContentCaps());
    expect(result.current).toBe(first + 2);
  });
});
