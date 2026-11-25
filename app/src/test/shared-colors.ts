/** css normalizes a color the way jsdom stores it in element.style, so a
 *  test can compare a rendered style against a theme color. */
export function css(color: string): string {
  const el = document.createElement("div");
  el.style.color = color;
  return el.style.color;
}
