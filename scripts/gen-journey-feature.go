//go:build ignore

// gen-journey-feature.go generates a single-take journey.feature from the
// per-section docs_capture.feature. No dependencies beyond the Go standard
// library.
//
// `make docs-capture` runs every @docs section as its OWN scenario (each a fresh
// browser + its own start/stop recording) — useful for screenshots and iterating
// on one section, but every section is a separate clip with no continuous video.
//
// This program flattens all those sections into ONE scenario that shares a single
// browser session and brackets the WHOLE walkthrough with one `start recording` /
// `stop recording "journey"`, producing an unbroken single-take recording with a
// continuous soundtrack — no stitching, no seams.
//
// Transformation:
//   - The Background is copied verbatim (runs once for the single scenario).
//   - Every section's steps are concatenated in file order.
//   - Per-section `I start recording` / `I stop recording "<n>"` are dropped and
//     replaced with exactly one `When I start recording` (after any pre-roll
//     setup, e.g. the intro card) and one trailing `Then I stop recording
//     "journey"`.
//   - `I capture screenshot "<n>"` steps are dropped — screenshots are the job of
//     `make docs-capture`; the journey target only produces the video.
//   - Agent-completion waits (the chat "Stop" button disappearing) become
//     best-effort, so one slow reply can't abort the whole continuous take.
//   - The feature is tagged @journey (NOT @docs) so normal BDD runs and
//     docs-capture both skip it; run it via `make docs-journey`.
//
// Usage: go run scripts/gen-journey-feature.go <docs_capture.feature> <journey.feature>
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var stepRe = regexp.MustCompile(`^\s*(Given|When|Then|And|But)\s+(.*\S)\s*$`)

type item struct {
	comment  bool
	kw, text string // text holds the comment line when comment==true
}

func isStart(it item) bool { return !it.comment && strings.TrimSpace(it.text) == "I start recording" }
func isStop(it item) bool {
	return !it.comment && strings.HasPrefix(strings.TrimSpace(it.text), "I stop recording")
}
func isShot(it item) bool {
	return !it.comment && strings.HasPrefix(strings.TrimSpace(it.text), "I capture screenshot")
}
func keep(it item) bool { return !(isStart(it) || isStop(it) || isShot(it)) }

// soften turns an agent-completion wait into a best-effort wait.
func soften(text string) string {
	if strings.HasPrefix(text, "I wait up to ") &&
		strings.Contains(text, `button[title='Stop']" to disappear`) &&
		!strings.HasSuffix(text, ", best effort") {
		return text + ", best effort"
	}
	return text
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: gen-journey-feature.go <src.feature> <dst.feature>")
		os.Exit(2)
	}
	src, dst := os.Args[1], os.Args[2]
	raw, err := os.ReadFile(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	// 1) Copy the Background block verbatim (stops at the first scenario tag /
	//    Scenario: heading).
	i := 0
	for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "Background:") {
		i++
	}
	var bg []string
	for i < len(lines) {
		s := strings.TrimSpace(lines[i])
		if (strings.HasPrefix(s, "@") || strings.HasPrefix(s, "Scenario:")) && !strings.HasPrefix(s, "Background:") {
			break
		}
		bg = append(bg, lines[i])
		i++
	}
	for len(bg) > 0 && strings.TrimSpace(bg[len(bg)-1]) == "" {
		bg = bg[:len(bg)-1]
	}

	// 2) Flatten every scenario's steps + inline comments, in order.
	var items []item
	for ; i < len(lines); i++ {
		line := lines[i]
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "@") || strings.HasPrefix(s, "Scenario:") || strings.HasPrefix(s, "Feature:") {
			continue
		}
		if strings.HasPrefix(s, "#") {
			items = append(items, item{comment: true, text: s})
			continue
		}
		if m := stepRe.FindStringSubmatch(line); m != nil {
			items = append(items, item{kw: m[1], text: m[2]})
		}
	}

	// 3) Split at the first `start recording`: everything before it is pre-roll
	//    that must run before the single recording starts (the intro card).
	first := -1
	for idx, it := range items {
		if isStart(it) {
			first = idx
			break
		}
	}
	if first < 0 {
		fmt.Fprintln(os.Stderr, "no 'I start recording' step found in source feature")
		os.Exit(1)
	}
	var preroll, body []item
	for _, it := range items[:first] {
		if keep(it) {
			preroll = append(preroll, it)
		}
	}
	for _, it := range items[first+1:] {
		if keep(it) {
			body = append(body, it)
		}
	}

	// 4) Emit.
	out := []string{
		"@frontend @journey",
		"Feature: End-to-end journey (single-take)",
		"  # GENERATED from docs_capture.feature by scripts/gen-journey-feature.go —",
		"  # DO NOT EDIT BY HAND. Concatenates every @docs section into ONE scenario",
		"  # so the whole walkthrough records as a single continuous take (one browser",
		"  # session, one start/stop recording -> docs/videos/journey.mp4): an unbroken",
		"  # video with a continuous soundtrack, no stitching. Per-section start/stop",
		"  # recording and screenshot captures are stripped (screenshots come from",
		"  # `make docs-capture`); agent-completion waits are made best-effort. Tagged",
		"  # @journey (not @docs) so normal runs and docs-capture both skip it.",
		"  # Regenerate + run via `make docs-journey`.",
	}
	out = append(out, bg...)
	out = append(out, "", "  Scenario: End-to-end product walkthrough")

	const indent = "    "
	firstStep := true
	for _, it := range preroll {
		if it.comment {
			out = append(out, indent+it.text)
			continue
		}
		kw := "And"
		if firstStep {
			kw = "Given"
			firstStep = false
		}
		out = append(out, fmt.Sprintf("%s%s %s", indent, kw, soften(it.text)))
	}
	out = append(out, indent+"When I start recording")
	for _, it := range body {
		if it.comment {
			out = append(out, indent+it.text)
			continue
		}
		out = append(out, fmt.Sprintf("%sAnd %s", indent, soften(it.text)))
	}
	out = append(out, indent+`Then I stop recording "journey"`)

	if err := os.WriteFile(dst, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s: %d pre-roll + %d body steps\n", dst, len(preroll), len(body))
}
