// Builds the srcdoc for the editor's HTML preview iframe (CodeEditor.tsx).

// In a srcdoc document with a <base>, "#section" links resolve against the
// base URL and would navigate the frame away. This handler keeps them as
// in-page jumps. It runs inside the sandboxed frame, never in the app.
const HASH_LINK_SCRIPT = `<script>document.addEventListener("click",function(e){var a=e.target instanceof Element&&e.target.closest('a[href^="#"]');if(!a)return;e.preventDefault();var id=decodeURIComponent(a.getAttribute("href").slice(1));var t=id?document.getElementById(id)||document.getElementsByName(id)[0]:document.body;if(t)t.scrollIntoView();});</script>`;

function escapeAttr(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
}

// withBaseHref injects a <base href> (unless the page declares its own) plus
// the hash-link handler at the top of <head>, falling back to just after
// <html>, or the very start for fragments.
export function withBaseHref(html: string, baseHref: string): string {
  const hasBase = /<base[\s>]/i.test(html);
  const inject = (hasBase ? "" : `<base href="${escapeAttr(baseHref)}">`) + HASH_LINK_SCRIPT;
  for (const tag of [/<head(\s[^>]*)?>/i, /<html(\s[^>]*)?>/i]) {
    const m = tag.exec(html);
    if (m) {
      const at = m.index + m[0].length;
      return html.slice(0, at) + inject + html.slice(at);
    }
  }
  return inject + html;
}
