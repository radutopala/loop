import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Vitest runs without globals, so Testing Library can't register its own
// afterEach cleanup; unmount every rendered tree here instead.
afterEach(cleanup);

// jsdom has no layout, so it leaves out scrollIntoView; components call it
// to reveal what they render.
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}
