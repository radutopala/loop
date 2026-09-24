A math notebook page: squared paper with a red margin, and everything written on its lines. Your HTML goes on the page.

- Write math the way it looks in a notebook, with these classes:
  - `<div class="expr">` — one line of working (`expr small` for a smaller one); `<span class="lead">E =</span>` labels it.
  - `<span class="frac"><span class="num">x + 5</span><span class="den">x² + 2x + 4</span></span>` — a fraction with a bar; it takes two squares, the numerator on the line the bar is drawn along and the denominator on the next, with the text around it level with the bar.
  - `<span class="cx">(x − 2)</span>` — a factor crossed out in red, for a simplification.
  - `<span class="big-br">[</span>` — a tall bracket; `<span class="op">·</span>`, `<span class="divsign">:</span>` — operators.
  - `hl` (blue), `hl2` (green), `hl3` (orange) — emphasis; `<span class="final-box">` — the boxed result.
  - `<p class="note">` — the explanation under a step.
- Each line of text is one square (`var(--g)`, 24px) tall and sits on a line, with an empty square under each `expr`, `note` and paragraph. Keep to these classes and plain `p`, `ul`, `h3`; a line-height, margin or padding that isn't a multiple of `var(--g)` takes the writing off the lines.
- Powers and indices: `x<sup>2</sup>`, `a<sub>n</sub>`, or the characters `²`, `³`, `√`, `−`, `·`.
- Notation HTML can't carry (roots over expressions, matrices, sums): import KaTeX in your js, e.g. `import katex from "https://esm.sh/katex@0.16"` and `katex.render(tex, element)`, and add its stylesheet `https://esm.sh/katex@0.16/dist/katex.min.css` with a `<link>` in your html. It loads from the network, so keep it for what the classes can't do.
- A multi-step solution: one `<section data-step="Step 1 — factor the denominators">` per step.
