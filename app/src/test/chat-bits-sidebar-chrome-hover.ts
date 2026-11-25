import { fireEvent } from "@testing-library/react";
import { expect } from "vitest";

/** css normalizes a color the way jsdom stores it in element.style. */
export function css(color: string): string {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
}

type Look = { backgroundColor?: string; color?: string };

function expectLook(el: HTMLElement, look: Look) {
  if (look.backgroundColor !== undefined) expect(el.style.backgroundColor).toBe(css(look.backgroundColor));
  if (look.color !== undefined) expect(el.style.color).toBe(css(look.color));
}

/** Hovers el and leaves it again, checking how it looks after each. */
export function expectHover(el: HTMLElement, over: Look, out: Look) {
  fireEvent.mouseEnter(el);
  expectLook(el, over);
  fireEvent.mouseLeave(el);
  expectLook(el, out);
}
