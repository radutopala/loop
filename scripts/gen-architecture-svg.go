//go:build ignore

// gen-architecture-svg.go draws the architecture diagram the README shows,
// docs/static/images/architecture.svg. No dependencies beyond the Go standard
// library.
//
// The layout is hand-placed: every box and every line has fixed coordinates on
// a 1200×900 canvas, in three bands — clients on top, the daemon on the host in
// the middle, Docker at the bottom. Lines are drawn before boxes, so a box sits
// on top of any line that runs under it. To move something, change its
// coordinates and those of the lines that end on it, then regenerate and look
// at the result (open the SVG in a browser).
//
// Usage: go run scripts/gen-architecture-svg.go docs/static/images/architecture.svg
package main

import (
	"fmt"
	"html"
	"os"
	"strconv"
	"strings"
)

const (
	width  = 1200
	height = 900

	font = "-apple-system,BlinkMacSystemFont,'Segoe UI',Inter,Helvetica,Arial,sans-serif"
	mono = "ui-monospace,SFMono-Regular,Menlo,Consolas,monospace"

	edgeColor  = "#7d8db3"
	gateStroke = "#b84a55"
)

// theme colors a box.
type theme struct {
	fill, stroke, title, sub string
}

var (
	daemonTheme = theme{fill: "#172038", stroke: "#3f5c9e", title: "#e8eeff", sub: "#8d9cc0"}
	dockerTheme = theme{fill: "#122a2a", stroke: "#2f9171", title: "#e3fff3", sub: "#86b9a5"}
	clientTheme = theme{fill: "#2b2213", stroke: "#c48d2f", title: "#ffe6b3", sub: "#c7a97a"}
	gateTheme   = theme{fill: "#2c1519", stroke: gateStroke, title: "#ffd0d5", sub: "#d89aa1"}
)

// point is a point on the canvas.
type point struct{ x, y float64 }

// svg collects the diagram's elements.
type svg struct {
	b strings.Builder
}

func (s *svg) add(format string, args ...any) {
	fmt.Fprintf(&s.b, format, args...)
	s.b.WriteByte('\n')
}

// num formats a coordinate without trailing zeros.
func num(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func esc(text string) string {
	return html.EscapeString(text)
}

// node draws a box with a title and, when sub isn't empty, a subtitle.
func (s *svg) node(x, y, w, h float64, title, sub string, t theme, monoTitle bool) {
	s.add(`<rect x="%s" y="%s" width="%s" height="%s" rx="10" fill="%s" stroke="%s" stroke-width="1.4"/>`, num(x), num(y), num(w), num(h), t.fill, t.stroke)
	s.add(`<rect x="%s" y="%s" width="3" height="%s" rx="1.5" fill="%s"/>`, num(x), num(y+10), num(h-20), t.stroke)
	family := font
	if monoTitle {
		family = mono
	}
	cx := x + w/2
	if sub == "" {
		s.add(`<text x="%s" y="%s" text-anchor="middle" font-family="%s" font-size="15" font-weight="600" fill="%s">%s</text>`, num(cx), num(y+h/2+5), family, t.title, esc(title))
		return
	}
	s.add(`<text x="%s" y="%s" text-anchor="middle" font-family="%s" font-size="15" font-weight="600" fill="%s">%s</text>`, num(cx), num(y+h/2-3), family, t.title, esc(title))
	s.add(`<text x="%s" y="%s" text-anchor="middle" font-family="%s" font-size="11.5" fill="%s">%s</text>`, num(cx), num(y+h/2+15), font, t.sub, esc(sub))
}

// cylinder draws a database.
func (s *svg) cylinder(x, y, w, h float64, title, sub string) {
	t := daemonTheme
	const ry = 9
	s.add(`<path d="M%s %s a%s %s 0 0 1 %s 0 v%s a%s %s 0 0 1 %s 0 Z" fill="%s" stroke="%s" stroke-width="1.4"/>`,
		num(x), num(y+ry), num(w/2), num(ry), num(w), num(h-2*ry), num(w/2), num(ry), num(-w), t.fill, t.stroke)
	s.add(`<path d="M%s %s a%s %s 0 0 0 %s 0" fill="none" stroke="%s" stroke-width="1.4"/>`, num(x), num(y+ry), num(w/2), num(ry), num(w), t.stroke)
	cx := x + w/2
	s.add(`<text x="%s" y="%s" text-anchor="middle" font-family="%s" font-size="15" font-weight="600" fill="%s">%s</text>`, num(cx), num(y+h/2+4), font, t.title, esc(title))
	s.add(`<text x="%s" y="%s" text-anchor="middle" font-family="%s" font-size="11" fill="%s">%s</text>`, num(cx), num(y+h/2+21), font, t.sub, esc(sub))
}

// panel draws a labeled region holding boxes.
func (s *svg) panel(x, y, w, h float64, label, fill, stroke, color string, dashed bool) {
	dash := ""
	if dashed {
		dash = ` stroke-dasharray="6 5"`
	}
	s.add(`<rect x="%s" y="%s" width="%s" height="%s" rx="16" fill="%s" stroke="%s" stroke-width="1.4"%s/>`, num(x), num(y), num(w), num(h), fill, stroke, dash)
	s.add(`<text x="%s" y="%s" font-family="%s" font-size="12" font-weight="700" letter-spacing="1.6" fill="%s">%s</text>`, num(x+18), num(y+24), font, color, esc(label))
}

func path(points []point) string {
	parts := make([]string, len(points))
	for i, p := range points {
		parts[i] = num(p.x) + " " + num(p.y)
	}
	return "M" + strings.Join(parts, " L")
}

// edge draws an arrow along points, with a head at both ends when both.
func (s *svg) edge(points []point, both, dashed bool) {
	extra := ""
	if dashed {
		extra += ` stroke-dasharray="5 4"`
	}
	if both {
		extra += ` marker-start="url(#arrow)"`
	}
	s.add(`<path d="%s" fill="none" stroke="%s" stroke-width="1.6" stroke-linejoin="round"%s marker-end="url(#arrow)"/>`, path(points), edgeColor, extra)
}

// line draws a connection without arrow heads.
func (s *svg) line(points []point, color string, dashed bool) {
	extra := ""
	if dashed {
		extra = ` stroke-dasharray="5 4"`
	}
	s.add(`<path d="%s" fill="none" stroke="%s" stroke-width="1.6"%s/>`, path(points), color, extra)
}

// label writes text on a line, outlined in the background color so it stays
// readable where it crosses one.
func (s *svg) label(x, y float64, text, anchor string) {
	s.add(`<text x="%s" y="%s" text-anchor="%s" font-family="%s" font-size="11.5" font-weight="500" fill="#b4c0dc" stroke="#0f1628" stroke-width="5" stroke-linejoin="round" paint-order="stroke">%s</text>`, num(x), num(y), anchor, font, esc(text))
}

func pts(xy ...float64) []point {
	points := make([]point, 0, len(xy)/2)
	for i := 0; i+1 < len(xy); i += 2 {
		points = append(points, point{xy[i], xy[i+1]})
	}
	return points
}

func draw() string {
	s := &svg{}
	s.add(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label="Loop architecture">`, width, height, width, height)
	s.add(`<defs>`)
	s.add(`<linearGradient id="bg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#0c1222"/><stop offset="1" stop-color="#121a30"/></linearGradient>`)
	s.add(`<marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 z" fill="%s"/></marker>`, edgeColor)
	s.add(`</defs>`)
	s.add(`<rect width="%d" height="%d" rx="22" fill="url(#bg)"/>`, width, height)
	s.add(`<text x="40" y="44" font-family="%s" font-size="13" font-weight="700" letter-spacing="2.4" fill="#6f7fa6">LOOP · ARCHITECTURE</text>`, font)

	// Panels.
	s.panel(40, 190, 1120, 350, "LOOP DAEMON · HOST", "#0f1830", "#2b3e6b", "#8ea6dc", false)
	s.panel(40, 580, 1120, 290, "DOCKER", "#0d1d20", "#1f5b48", "#6fd0a6", false)
	s.panel(630, 615, 510, 235, "AGENT CONTAINER · PER CHAT", "#0f2427", "#2f9171", "#6fd0a6", true)

	// Clients into the daemon.
	s.edge(pts(450, 142, 450, 233), true, false)
	s.label(456, 170, "REST · WebSocket", "start")
	s.edge(pts(1044, 142, 1044, 233), true, false)
	s.label(1050, 170, "messages", "start")
	s.edge(pts(720, 265, 942, 265), false, false)
	s.label(831, 258, "local chat", "middle")
	s.edge(pts(216, 295, 216, 328), false, false)
	s.edge(pts(378, 295, 378, 328), false, false)
	s.edge(pts(1044, 295, 1044, 328), false, false)

	// Turns and runs into the runner.
	s.edge(pts(1044, 390, 1044, 438), false, false)
	s.label(1052, 420, "AgentRequest", "start")
	s.edge(pts(822, 390, 822, 410, 960, 410, 960, 438), false, false)
	s.label(870, 404, "nodes", "middle")
	s.edge(pts(600, 390, 600, 425, 930, 425, 930, 438), false, false)
	s.label(700, 440, "due task", "middle")

	// The runner creates the agent container, whose tools call back.
	s.edge(pts(1010, 500, 1010, 648), false, false)
	s.label(1002, 565, "create · mount dir_path", "end")
	s.edge(pts(755, 648, 755, 515, 489, 515, 489, 297), true, false)
	s.label(622, 509, "tool calls", "middle")

	// Plugins.
	s.edge(pts(130, 390, 130, 648), false, false)
	s.label(136, 470, "start · stop", "start")
	s.edge(pts(267, 648, 267, 297), true, false)
	s.label(273, 528, "Plugin API · events", "start")

	// Memory and embeddings.
	s.line(pts(370, 390, 370, 440), edgeColor, false)
	s.edge(pts(462, 390, 462, 648), false, false)
	s.label(468, 560, "embed", "start")

	// Inside Docker.
	s.edge(pts(648, 680, 612, 680), false, false)
	s.label(630, 672, "CDP", "middle")
	s.line(pts(860, 680, 900, 680), edgeColor, false)
	s.label(880, 672, "stdio", "middle")
	s.line(pts(1010, 710, 1010, 760), gateStroke, true)
	s.label(1018, 739, "guards syscalls", "start")

	// Clients.
	s.node(330, 72, 240, 70, "Desktop app", "Electron · chats, editor, terminal", clientTheme, false)
	s.node(944, 72, 200, 70, "Slack / Discord", "@mention · reply · !loop · DM", clientTheme, false)

	// Daemon.
	s.node(180, 235, 540, 60, "API server", "REST · /api/ws events · Plugin API · MCP calls", daemonTheme, false)
	s.node(944, 235, 200, 60, "Bots", "local · Slack · Discord", daemonTheme, false)
	s.node(60, 330, 192, 60, "Plugin runtime", "lazy start · idle stop", daemonTheme, false)
	s.node(282, 330, 192, 60, "Memory indexer", "semantic search", daemonTheme, false)
	s.node(504, 330, 192, 60, "Scheduler", "cron · interval · once", daemonTheme, false)
	s.node(726, 330, 192, 60, "Workflow engine", "prompt & bash DAGs", daemonTheme, false)
	s.node(948, 330, 192, 60, "Orchestrator", "queue · sessions · live", daemonTheme, false)
	s.cylinder(285, 440, 170, 66, "SQLite", "channels · messages · tasks")
	s.node(900, 440, 220, 60, "DockerRunner", "live session or --print", daemonTheme, false)

	// Docker.
	s.node(60, 650, 230, 60, "Plugin containers", "per channel", dockerTheme, false)
	s.node(320, 650, 150, 60, "Ollama", "embeddings", dockerTheme, false)
	s.node(490, 650, 120, 60, "Chrome", "headless", dockerTheme, false)
	s.node(650, 650, 210, 60, "MCP servers", "loop · loop-browser", dockerTheme, false)
	s.node(900, 650, 220, 60, "claude", "Claude Code, your dir mounted", dockerTheme, true)
	s.node(650, 760, 470, 62, "Security gate", "seccomp notify · Docker API proxy · approvals in chat", gateTheme, false)

	s.add(`</svg>`)
	return s.b.String()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/gen-architecture-svg.go <out.svg>")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], []byte(draw()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
