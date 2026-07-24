import { describe, expect, it } from "vitest";
import { formatCountdown } from "./DelayCountdown";

describe("formatCountdown", () => {
  it("renders sub-minute delays as m:ss", () => {
    expect(formatCountdown(5)).toBe("0:05");
    expect(formatCountdown(45)).toBe("0:45");
  });

  it("renders minutes as m:ss with zero-padded seconds", () => {
    expect(formatCountdown(60)).toBe("1:00");
    expect(formatCountdown(90)).toBe("1:30");
    expect(formatCountdown(599)).toBe("9:59");
  });

  it("switches to h:mm:ss once an hour or more remains", () => {
    expect(formatCountdown(3600)).toBe("1:00:00");
    expect(formatCountdown(3661)).toBe("1:01:01");
    expect(formatCountdown(7325)).toBe("2:02:05");
  });

  it("floors fractional seconds", () => {
    expect(formatCountdown(30.9)).toBe("0:30");
  });

  it("clamps negatives to 0:00", () => {
    expect(formatCountdown(-1)).toBe("0:00");
    expect(formatCountdown(-500)).toBe("0:00");
  });
});
