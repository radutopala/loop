// Package chatcomponents builds the components an agent shows in the desktop
// chat: a template (built in, or defined in config) filled with the agent's
// own HTML, CSS and JS, composed into one self-contained HTML document and
// wrapped in a fenced block the chat renders in a sandboxed frame.
package chatcomponents

import (
	"embed"
	"fmt"
	"html"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/radutopala/loop/internal/config"
)

//go:embed builtin
var builtinFS embed.FS

// FenceTag is the info-string tag of a component block in a chat message.
const FenceTag = "loop-component"

// Template is a resolved chat component template.
type Template struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Guide       string `json:"guide"`
	// Shell is the HTML the content goes into, at its {{content}} slot.
	Shell string `json:"-"`
	CSS   string `json:"-"`
}

// Content is what the agent fills a template with.
type Content struct {
	Title string
	HTML  string
	CSS   string
	JS    string
}

var builtins = []config.ChatComponent{
	{Name: "math", Description: "A math notebook page, squared paper with every line written on the grid, for math worked step by step: fractions with a bar, crossed-out factors, highlights and a boxed result."},
	{Name: "canvas", Description: "A canvas drawn from JS: plots, geometry, diagrams, animations and simulations."},
	{Name: "react", Description: "An interactive React app on a dark page, written with htm instead of JSX: forms, calculators, tables, charts from a React library, anything with state."},
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Resolve returns the templates available to a channel: the built-ins, then
// the config's entries, each replacing a built-in or earlier entry of the
// same name. A template's files are read from the first of loopDirs that has
// them (project .loop before ~/.loop), then from the built-in it replaces; a
// missing file is simply empty. Entries with a name that can't sit in a fence
// info string are skipped.
func Resolve(entries []config.ChatComponent, loopDirs []string, readFile func(string) ([]byte, error)) []Template {
	var out []Template
	index := map[string]int{}
	for _, b := range builtins {
		index[b.Name] = len(out)
		out = append(out, Template{
			Name:        b.Name,
			Description: b.Description,
			Guide:       readBuiltin(b.Name, "guide.md"),
			Shell:       readBuiltin(b.Name, "shell.html"),
			CSS:         readBuiltin(b.Name, "style.css"),
		})
	}
	for _, e := range entries {
		if !validName.MatchString(e.Name) {
			continue
		}
		var t Template
		i, replaces := index[e.Name]
		if replaces {
			t = out[i]
		}
		t.Name = e.Name
		if e.Description != "" {
			t.Description = e.Description
		}
		read := func(file, fallback string) string {
			for _, dir := range loopDirs {
				if data, err := readFile(filepath.Join(e.Dir(dir), file)); err == nil {
					return string(data)
				}
			}
			return fallback
		}
		t.Guide = read("guide.md", t.Guide)
		t.Shell = read("shell.html", t.Shell)
		t.CSS = read("style.css", t.CSS)
		if replaces {
			out[i] = t
			continue
		}
		index[e.Name] = len(out)
		out = append(out, t)
	}
	return out
}

// Find returns the template with the given name.
func Find(templates []Template, name string) (Template, bool) {
	for _, t := range templates {
		if t.Name == name {
			return t, true
		}
	}
	return Template{}, false
}

func readBuiltin(name, file string) string {
	data, _ := builtinFS.ReadFile(path.Join("builtin", name, file))
	return string(data)
}

// Title makes a component title fit a fence info string: one line, trimmed
// and capped. It's plain text, but an agent writing HTML around it tends to
// escape it too ("x &gt; 2"), so entities are decoded first.
func Title(title string) string {
	title = strings.Join(strings.Fields(html.UnescapeString(title)), " ")
	if r := []rune(title); len(r) > 120 {
		title = string(r[:120])
	}
	return title
}

// baseCSS applies to every component, under the template's own styles.
const baseCSS = `html, body { margin: 0; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
.loop-step-title { font-size: 0.78rem; letter-spacing: 0.08em; text-transform: uppercase; font-weight: 700; margin-bottom: 14px; }
nav.loop-steps { display: flex; justify-content: center; align-items: center; gap: 12px; padding: 12px 0; }
nav.loop-steps button { background: #3a404a; color: #e8eaed; border: 1px solid #4a515d; border-radius: 8px; padding: 6px 16px; font-size: 0.9rem; cursor: pointer; }
nav.loop-steps button:disabled { opacity: 0.3; cursor: default; }
nav.loop-steps span { color: #9aa0a6; font-size: 0.82rem; min-width: 60px; text-align: center; }
`

// baseScript reports the document's height to the chat so the frame fits
// its content, and turns <section data-step="title"> elements into a stepper
// that shows one at a time. It runs on DOMContentLoaded, after the
// component's own module script, so sections that script builds count too.
const baseScript = `(() => {
  const report = () => parent.postMessage({ type: "loop-component-height", height: Math.ceil(document.body.getBoundingClientRect().height) }, "*");
  new ResizeObserver(report).observe(document.body);
  addEventListener("load", report);
  addEventListener("DOMContentLoaded", () => {
    const steps = [...document.querySelectorAll("section[data-step]")];
    for (const s of steps) {
      const title = document.createElement("div");
      title.className = "loop-step-title";
      title.textContent = s.dataset.step;
      s.prepend(title);
    }
    if (steps.length < 2) return;
    const nav = document.createElement("nav");
    nav.className = "loop-steps";
    nav.innerHTML = '<button type="button" data-dir="-1" aria-label="Previous step">◀</button><span></span><button type="button" data-dir="1" aria-label="Next step">▶</button>';
    steps[steps.length - 1].after(nav);
    const [prev, counter, next] = nav.children;
    let at = 0;
    const show = () => {
      steps.forEach((s, i) => { s.style.display = i === at ? "" : "none"; });
      counter.textContent = (at + 1) + " / " + steps.length;
      prev.disabled = at === 0;
      next.disabled = at === steps.length - 1;
    };
    const go = (dir) => { at = Math.min(steps.length - 1, Math.max(0, at + dir)); show(); };
    nav.addEventListener("click", (e) => { const b = e.target.closest("button"); if (b) go(Number(b.dataset.dir)); });
    addEventListener("keydown", (e) => { if (e.key === "ArrowLeft") go(-1); if (e.key === "ArrowRight") go(1); });
    show();
  });
})();`

// Compose builds the component's HTML document: the base styles, the
// template's and the agent's CSS, the agent's HTML in the template's
// {{content}} slot (after the shell when it has none), the base script, and
// the agent's JS as a module so it can import from a CDN.
func Compose(t Template, c Content) string {
	body := t.Shell
	if strings.Contains(body, "{{content}}") {
		body = strings.Replace(body, "{{content}}", c.HTML, 1)
	} else {
		body += "\n" + c.HTML
	}
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", htmlEscaper.Replace(c.Title))
	fmt.Fprintf(&b, "<style>\n%s</style>\n", baseCSS)
	for _, css := range []string{t.CSS, c.CSS} {
		if strings.TrimSpace(css) != "" {
			fmt.Fprintf(&b, "<style>\n%s\n</style>\n", closeTagEscaper("style").Replace(css))
		}
	}
	b.WriteString("</head>\n<body>\n")
	b.WriteString(body)
	fmt.Fprintf(&b, "\n<script>\n%s\n</script>\n", baseScript)
	if strings.TrimSpace(c.JS) != "" {
		fmt.Fprintf(&b, "<script type=\"module\">\n%s\n</script>\n", closeTagEscaper("script").Replace(c.JS))
	}
	b.WriteString("</body>\n</html>\n")
	return b.String()
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// closeTagEscaper keeps an agent's CSS or JS from closing the element it's
// in early: "</script" inside a script ends it, whatever the JS meant.
func closeTagEscaper(tag string) *strings.Replacer {
	return strings.NewReplacer("</"+tag, `<\/`+tag, "</"+strings.ToUpper(tag), `<\/`+strings.ToUpper(tag))
}

// Fence wraps a composed document in the fenced block the chat renders:
// ```loop-component <template> <title>. The fence is one backtick longer
// than any run of backticks that starts a line in the document, so the
// document can't close it.
func Fence(template, title, doc string) string {
	longest := 0
	for line := range strings.SplitSeq(doc, "\n") {
		n := len(line) - len(strings.TrimLeft(line, "`"))
		longest = max(longest, n)
	}
	fence := strings.Repeat("`", max(3, longest+1))
	info := strings.TrimSpace(FenceTag + " " + template + " " + title)
	return fence + info + "\n" + strings.TrimSuffix(doc, "\n") + "\n" + fence
}

// Guide is the text the chat_component tool gives an agent: how to show a
// component, then each template with its own guide.
func Guide(templates []Template) string {
	var b strings.Builder
	b.WriteString(`Show a component in the desktop chat with chat_component action "show": pick a template and fill it with html (and optionally css and js).

- The component renders in a sandboxed frame in the chat, where the call was made; your text reply follows it. It sizes itself to its content.
- html is a fragment, not a full page. css is scoped to the component. js runs as a module after the html, so it can import from a CDN (https://esm.sh/...).
- Steps: one <section data-step="Step title"> per step shows them one at a time with ◀ n/N ▶ buttons — no JS needed.
- It only renders in the Loop desktop app; on Slack or Discord answer in text.

Templates:
`)
	for _, t := range templates {
		fmt.Fprintf(&b, "\n## %s\n%s\n", t.Name, t.Description)
		if g := strings.TrimSpace(t.Guide); g != "" {
			fmt.Fprintf(&b, "\n%s\n", g)
		}
	}
	return b.String()
}
