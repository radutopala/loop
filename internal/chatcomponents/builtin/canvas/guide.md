A dark frame with a `<canvas id="c">` across its full width, 360px tall (change it with `css`: `#c { height: 480px; }`). Your HTML goes under the canvas: a legend, sliders, buttons.

- Draw from `js`: `window.canvas` and `window.ctx` (a 2D context) are ready, already scaled for the screen's pixel ratio, so draw in CSS pixels: `canvas.clientWidth` × `canvas.clientHeight`.
- The canvas is resized, and cleared, when its box changes — including once right after load, and when the user opens it across the whole pane. Draw a still picture in a function and run it on `canvas.addEventListener("resize", draw)` as well as at the start; an animation that redraws every frame needs nothing.
- Animate with `requestAnimationFrame`; read inputs with `addEventListener`.
- For a library, import it from a CDN: `import * as d3 from "https://esm.sh/d3@7";`.
