An interactive React app on a dark page: React 19, ReactDOM and htm are ready by name, and an empty `<div id="root">` is where it mounts.

- Write the app in `js`. There's no build step, so no JSX: write the markup with [htm](https://github.com/developit/htm), which reads the same in a tagged template:
  ```js
  import React, { useState } from "react";
  import { createRoot } from "react-dom/client";
  import htm from "htm";
  const html = htm.bind(React.createElement);

  function App() {
    const [n, setN] = useState(0);
    return html`<div class="row"><button onClick=${() => setN(n + 1)}>Clicked ${n} times</button></div>`;
  }
  createRoot(document.getElementById("root")).render(html`<${App} />`);
  ```
  In htm, components are `<${Name} prop=${value} />`, and a closing tag can be `<//>`; attributes are `class`, `style=${{ ... }}` and `onClick=${fn}`.
- Import `react`, `react-dom/client` and `htm` by those names. Import a React library from esm.sh with `?external=react,react-dom`, e.g. `import { LineChart, Line } from "https://esm.sh/recharts@3?external=react,react-dom"`, so it shares the page's React; a second copy of React breaks hooks.
- `html` is usually empty: the app renders into `#root`. HTML you do give goes under it.
- Plain elements are styled for the dark page: buttons (`class="secondary"` for a grey one), inputs, selects, tables, `code`. Also `card` (a panel), `row` (items side by side) and `muted` (grey text).
- Steps: keep the current step in state and render it with your own buttons; `<section data-step>` isn't picked up from React.
- React and every library load from esm.sh, so a component needs the network; with no network it stays empty.
