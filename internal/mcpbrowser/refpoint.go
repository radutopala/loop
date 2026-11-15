package mcpbrowser

import (
	"context"
	"encoding/json"
	"fmt"
)

// refPoint brings ref n (1-based, already range-checked) on screen and
// returns its centre.
//
// A ref's rect is where find_elements saw it, relative to the viewport, and
// mouse input or elementFromPoint reaches only what is on screen. So a ref
// outside the viewport is scrolled to the middle of it first, and every ref
// moves by however far the page scrolled, which keeps the others usable.
func (s *Server) refPoint(ctx context.Context, n int) (x, y float64, err error) {
	ref := s.refs[n-1]
	x, y = ref.X+ref.Width/2, ref.Y+ref.Height/2
	resp, err := s.callAction(ctx, "evaluate_js", map[string]any{"expression": bringIntoViewJS(x, y)})
	if err != nil {
		return 0, 0, fmt.Errorf("scrolling ref_%d into view failed: %w", n, err)
	}
	var moved [2]float64
	if err := json.Unmarshal([]byte(resp.Result), &moved); err != nil {
		return 0, 0, fmt.Errorf("scrolling ref_%d into view failed: unexpected answer %q", n, resp.Result)
	}
	for i := range s.refs {
		s.refs[i].X -= moved[0]
		s.refs[i].Y -= moved[1]
	}
	return x - moved[0], y - moved[1], nil
}

// bringIntoViewJS scrolls the page so the point (x, y) is in the viewport,
// along each axis only when it is outside, and answers how far the page
// moved as [dx, dy]. The scroll is instant even where the page asks for
// smooth scrolling, so the distance is known when the script returns.
func bringIntoViewJS(x, y float64) string {
	return fmt.Sprintf(`(() => {
  const x = %f, y = %f, sx = scrollX, sy = scrollY;
  const off = (v, size) => v < 0 || v >= size ? v - size / 2 : 0;
  const dx = off(x, innerWidth), dy = off(y, innerHeight);
  if (dx || dy) scrollBy({left: dx, top: dy, behavior: "instant"});
  return JSON.stringify([scrollX - sx, scrollY - sy]);
})()`, x, y)
}

// selectOptionJS picks value in the <select> at (x, y), looking through
// same-origin frames, and fires the events a user's pick would. It answers
// "selected", "" when the point holds no select (a field to type into, or
// a cross-origin frame script cannot see into), or why it could not pick.
//
// Typing into a select does not work: the click opens its list, and the
// typed letters only highlight an option until the list is closed.
func selectOptionJS(x, y float64, value string) string {
	v, _ := json.Marshal(value) // a string: never fails
	return fmt.Sprintf(`(() => {
  let x = %f, y = %f, el = document.elementFromPoint(x, y);
  while (el && (el.tagName === "IFRAME" || el.tagName === "FRAME")) {
    let doc = null;
    try { doc = el.contentDocument; } catch (e) {}
    if (!doc) return "";
    const r = el.getBoundingClientRect();
    x -= r.left + el.clientLeft;
    y -= r.top + el.clientTop;
    el = doc.elementFromPoint(x, y);
  }
  if (!el || el.tagName !== "SELECT") return "";
  const want = %s, opts = [...el.options];
  const opt = opts.find(o => o.value === want || o.text.trim() === want) ||
    opts.find(o => o.text.trim().toLowerCase() === want.toLowerCase());
  if (!opt) return "no option " + JSON.stringify(want) + "; the options are: " + opts.map(o => o.text.trim()).join(", ");
  if (opt.disabled) return "option " + JSON.stringify(want) + " is disabled";
  el.focus();
  el.value = opt.value;
  el.dispatchEvent(new Event("input", {bubbles: true}));
  el.dispatchEvent(new Event("change", {bubbles: true}));
  return "selected";
})()`, x, y, v)
}
