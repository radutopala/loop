import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { css } from "../../test/shared-colors";
import { PillDeck, StatusPill } from "./StatusPill";

const pills = [
  { key: "gate", label: "gate", color: "red" },
  { key: "ci", label: "ci", color: "blue" },
  { key: "cmt", label: "cmt", color: "green" },
];

describe("StatusPill", () => {
  it("shows its label in its color with a title and margin", () => {
    const { container } = render(<StatusPill label="gate" color="red" title="tip" marginLeft={4} />);
    const pill = container.querySelector<HTMLElement>("[data-pill='gate']")!;
    expect(pill.textContent).toBe("gate");
    expect(pill.title).toBe("tip");
    expect(pill.style.color).toBe(css("red"));
    expect(pill.style.marginLeft).toBe("4px");
    expect(pill.style.transform).toBe("");
  });

  it("hides the label of a pill under the top of a deck and offsets it", () => {
    const { container } = render(<StatusPill label="ci" color="red" depth={2} background="black" />);
    const pill = container.querySelector<HTMLElement>("[data-pill='ci']")!;
    expect(pill.style.color).toBe("transparent");
    expect(pill.style.background).toBe(css("black"));
    expect(pill.style.transform).toBe("translate(6px, -2px)");
  });
});

describe("PillDeck", () => {
  it("renders nothing without pills", () => {
    const { container } = render(<PillDeck pills={[]} open={false} background="black" />);
    expect(container.innerHTML).toBe("");
  });

  it("lays a lone pill out on its own", () => {
    const { container } = render(<PillDeck pills={pills.slice(0, 1)} open={false} background="black" marginLeft={3} />);
    expect(container.querySelector("[data-pill-deck]")).toBeNull();
    expect(container.querySelector<HTMLElement>("[data-pill='gate']")!.style.marginLeft).toBe("3px");
  });

  it("lays an open deck's pills side by side, spaced after the first", () => {
    const { container } = render(<PillDeck pills={pills} open background="black" marginLeft={3} spacing={5} />);
    expect(container.querySelector("[data-pill-deck]")).toBeNull();
    const shown = [...container.querySelectorAll<HTMLElement>("[data-pill]")];
    expect(shown.map((p) => p.dataset.pill)).toEqual(["gate", "ci", "cmt"]);
    expect(shown.map((p) => p.style.marginLeft)).toEqual(["3px", "5px", "5px"]);
  });

  it("stacks a closed deck with the first pill painted last, on top", () => {
    const { container } = render(<PillDeck pills={pills} open={false} background="black" />);
    const deck = container.querySelector<HTMLElement>("[data-pill-deck]")!;
    expect(deck.dataset.pillDeck).toBe("3");
    expect(deck.style.paddingRight).toBe("6px");
    const shown = [...deck.querySelectorAll<HTMLElement>("[data-pill]")];
    expect(shown.map((p) => p.dataset.pill)).toEqual(["cmt", "ci", "gate"]);
    expect(shown[2]!.style.color).toBe(css("red"));
    expect(shown[0]!.style.color).toBe("transparent");
  });
});
