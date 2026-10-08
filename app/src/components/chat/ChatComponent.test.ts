import { describe, expect, it } from "vitest";
import { clickedInFrame } from "./ChatComponent";

describe("clickedInFrame", () => {
  // Tests run without a DOM; identity is all clickedInFrame compares.
  const frame = {} as HTMLIFrameElement;
  const other = {} as Element;
  const cases = [
    { name: "a live click in the frame", active: frame, activation: { isActive: true }, want: true },
    { name: "a live click elsewhere in the app", active: other, activation: { isActive: true }, want: false },
    { name: "focus in the frame, but no live click", active: frame, activation: { isActive: false }, want: false },
    { name: "no userActivation API", active: frame, activation: undefined, want: false },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(clickedInFrame(frame, { activeElement: c.active }, c.activation)).toBe(c.want);
    });
  }
});
