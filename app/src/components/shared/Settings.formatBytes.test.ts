import { describe, expect, it } from "vitest";
import { formatBytes } from "./Settings";

describe("formatBytes", () => {
  it("clamps zero, negatives, and non-finite values to 0 B", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(-1)).toBe("0 B");
    expect(formatBytes(Number.NaN)).toBe("0 B");
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe("0 B");
  });

  it("reports whole bytes without decimals", () => {
    expect(formatBytes(1)).toBe("1 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1023)).toBe("1023 B");
  });

  it("scales into KB/MB/GB/TB", () => {
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1024 * 1024)).toBe("1.0 MB");
    expect(formatBytes(1024 * 1024 * 1024)).toBe("1.0 GB");
    expect(formatBytes(1024 ** 4)).toBe("1.0 TB");
  });

  it("rounds to whole units once the value is 10 or larger", () => {
    expect(formatBytes(15 * 1024)).toBe("15 KB");
    expect(formatBytes(Math.round(12.7 * 1024 * 1024))).toBe("13 MB");
  });

  it("caps the unit at TB for very large values", () => {
    expect(formatBytes(1024 ** 5)).toBe("1024 TB");
  });
});
