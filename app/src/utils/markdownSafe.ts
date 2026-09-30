import DOMPurify, { type DOMPurify as Purifier } from "dompurify";
import { marked } from "marked";
import { inAppHref } from "./messageLinks";

/** URL attributes (href, src, …) keep only these schemes; relative and other URLs are dropped. */
const ALLOWED_URI = /^(?:https?|mailto|loop):/i;

let purifier: Purifier | null = null;

// A purifier of our own, created on first use: mermaid adds and removes hooks
// on the shared default instance, and nothing here may touch window at import
// time.
function getPurifier(): Purifier {
  if (purifier) return purifier;
  const p = DOMPurify(window);
  p.addHook("afterSanitizeAttributes", (node) => {
    if (node.tagName !== "A" || !node.hasAttribute("href")) return;
    const href = node.getAttribute("href") ?? "";
    // A loop://channel/ link moves the page's hash, as it does in the chat.
    const inApp = inAppHref(href);
    if (inApp) {
      node.setAttribute("href", inApp);
      node.removeAttribute("target");
      node.removeAttribute("rel");
      return;
    }
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  });
  purifier = p;
  return p;
}

/**
 * Renders markdown to HTML that is safe for dangerouslySetInnerHTML: no
 * scripts or event handlers, links only to http(s), mailto and loop URLs,
 * each opening outside the page (a loop://channel/ link opens in the app).
 */
export function renderMarkdownSafe(md: string): string {
  const html = marked.parse(md, { async: false }) as string;
  return getPurifier().sanitize(html, { ALLOWED_URI_REGEXP: ALLOWED_URI });
}
