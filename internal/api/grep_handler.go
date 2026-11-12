package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/radutopala/loop/internal/config"
)

const (
	// grepMaxFiles and grepMaxMatches cap a content search's results;
	// grepFileMatches caps one file's.
	grepMaxFiles    = 200
	grepMaxMatches  = 2000
	grepFileMatches = 50
	// grepMaxLine is how much of a matching line comes back.
	grepMaxLine = 300
	// grepMinQuery is the shortest query searched: one character matches
	// most of every file.
	grepMinQuery = 2
)

// errGrepFull stops a search once it has all the results it returns.
var errGrepFull = errors.New("grep results full")

// grepMatch is one matching line: 1-based line and column, the match's
// length (both in characters), and the line's text, cut to grepMaxLine.
type grepMatch struct {
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Length int    `json:"length"`
	Text   string `json:"text"`
}

// grepFile is a file's matches.
type grepFile struct {
	RootIndex int         `json:"root_index"`
	RelPath   string      `json:"rel_path"`
	Matches   []grepMatch `json:"matches"`
}

type grepResponse struct {
	Files []grepFile `json:"files"`
	// Truncated is set when the caps cut the results short.
	Truncated bool `json:"truncated"`
	// Engine is what searched: "ripgrep", or "builtin" without rg.
	Engine string `json:"engine"`
}

// grepQuery is a content search: q, as a literal or a regex, matched
// with or without case.
type grepQuery struct {
	q             string
	regex         bool
	caseSensitive bool
}

// pattern is q as a Go regexp, for the builtin search.
func (g grepQuery) pattern() (*regexp.Regexp, error) {
	expr := g.q
	if !g.regex {
		expr = regexp.QuoteMeta(expr)
	}
	if !g.caseSensitive {
		expr = "(?i)" + expr
	}
	return regexp.Compile(expr)
}

// grepCollector gathers matches across roots up to the caps.
type grepCollector struct {
	files     []grepFile
	matches   int
	truncated bool
}

// add records a match in root's file rel, and errGrepFull once the
// results are full. Matches of one file arrive together.
func (c *grepCollector) add(root int, rel string, m grepMatch) error {
	if c.matches >= grepMaxMatches {
		c.truncated = true
		return errGrepFull
	}
	n := len(c.files)
	if n == 0 || c.files[n-1].RootIndex != root || c.files[n-1].RelPath != rel {
		if n >= grepMaxFiles {
			c.truncated = true
			return errGrepFull
		}
		c.files = append(c.files, grepFile{RootIndex: root, RelPath: rel})
		n++
	}
	f := &c.files[n-1]
	if len(f.Matches) >= grepFileMatches {
		c.truncated = true
		return nil
	}
	f.Matches = append(f.Matches, m)
	c.matches++
	return nil
}

// grepLine trims text's line ending and cuts it to grepMaxLine bytes, on a
// character boundary.
func grepLine(text string) string {
	text = strings.TrimRight(text, "\r\n")
	if len(text) <= grepMaxLine {
		return text
	}
	cut := grepMaxLine
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// column is the 1-based character column of byte offset off in line.
func column(line string, off int) int {
	off = min(max(off, 0), len(line))
	return utf8.RuneCountInString(line[:off]) + 1
}

// grepMatchAt is the match of line's bytes start to end, on line number n.
func grepMatchAt(n int, line string, start, end int) grepMatch {
	col := column(line, start)
	return grepMatch{Line: n, Column: col, Length: max(column(line, end)-col, 0), Text: grepLine(line)}
}

func (s *Server) handleGrepFiles(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	g := grepQuery{
		q:             r.URL.Query().Get("q"),
		regex:         r.URL.Query().Get("regex") == "1",
		caseSensitive: r.URL.Query().Get("case") == "1",
	}
	if utf8.RuneCountInString(g.q) < grepMinQuery {
		writeHTTPJSON(w, http.StatusOK, grepResponse{Files: []grepFile{}}, s.logger)
		return
	}
	re, err := g.pattern()
	if err != nil {
		http.Error(w, "invalid regex: "+err.Error(), http.StatusBadRequest)
		return
	}
	roots, err := s.allDirPaths(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ec := s.editorConfig(r.Context(), r.PathValue("id"))
	c := &grepCollector{}
	engine := "builtin"
	rg, lookErr := s.editorCmds.lookPathFn()("rg")
	if lookErr == nil {
		engine = "ripgrep"
	}
	for i, root := range roots {
		if root == "" {
			continue
		}
		var err error
		if lookErr == nil {
			err = s.ripgrep(r.Context(), rg, i, root, g, ec, c)
		} else {
			err = s.grepWalk(i, root, re, ec, c)
		}
		if errors.Is(err, errGrepFull) {
			break
		}
		if err != nil {
			s.logger.Warn("content search failed", "root", root, "engine", engine, "err", err)
		}
	}
	if c.files == nil {
		c.files = []grepFile{}
	}
	// rg searches files in parallel: order them as the file tree does.
	sort.SliceStable(c.files, func(i, j int) bool {
		a, b := c.files[i], c.files[j]
		if a.RootIndex != b.RootIndex {
			return a.RootIndex < b.RootIndex
		}
		return a.RelPath < b.RelPath
	})
	writeHTTPJSON(w, http.StatusOK, grepResponse{Files: c.files, Truncated: c.truncated, Engine: engine}, s.logger)
}

// rgEvent is the part of a line of rg --json the search reads.
type rgEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       struct{ Text string } `json:"path"`
		Lines      struct{ Text string } `json:"lines"`
		LineNumber int                   `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

// ripgrep searches root with rg, which skips what .gitignore and the
// hidden-file rules exclude, and binary files; and what ec's search
// excludes.
func (s *Server) ripgrep(ctx context.Context, rg string, rootIdx int, root string, g grepQuery, ec config.EditorConfig, c *grepCollector) error {
	args := []string{"--json", "--no-require-git", "--max-count", "51", "--max-filesize", "5M", "--max-columns", "2000"}
	if !g.regex {
		args = append(args, "--fixed-strings")
	}
	if g.caseSensitive {
		args = append(args, "--case-sensitive")
	} else {
		args = append(args, "--ignore-case")
	}
	for dir := range fileSearchSkipDirs {
		args = append(args, "--glob", "!"+dir+"/")
	}
	for _, glob := range ec.ExcludeGlobs() {
		args = append(args, "--glob", "!"+glob)
	}
	args = append(args, "--regexp", g.q, "--", ".")
	return s.editorCmds.streamFn()(ctx, root, rg, args, func(out io.Reader) error {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var ev rgEvent
			if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != "match" {
				continue
			}
			text := ev.Data.Lines.Text
			start, end := 0, 0
			if len(ev.Data.Submatches) > 0 {
				start, end = ev.Data.Submatches[0].Start, ev.Data.Submatches[0].End
			}
			rel := filepath.ToSlash(strings.TrimPrefix(ev.Data.Path.Text, "./"))
			m := grepMatchAt(ev.Data.LineNumber, text, start, end)
			if err := c.add(rootIdx, rel, m); err != nil {
				return err
			}
		}
		return sc.Err()
	})
}

// grepWalk searches root without rg: the file search's walk, which skips
// the same directories, .gitignore patterns and ec's search excludes, over
// the text files that aren't hidden, as rg does.
func (s *Server) grepWalk(rootIdx int, root string, re *regexp.Regexp, ec config.EditorConfig, c *grepCollector) error {
	patterns := loadGitignorePatterns(s.sys, root)
	return s.sys.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		excluded := gitignoreMatch(patterns, rel) || ec.Excluded(filepath.ToSlash(rel))
		if d.IsDir() {
			if fileSearchSkipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") || excluded {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") || excluded {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > maxFileSize {
			return nil
		}
		data, err := s.sys.ReadFile(path)
		if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
			return nil
		}
		rel = filepath.ToSlash(rel)
		for i, line := range strings.Split(string(data), "\n") {
			loc := re.FindStringIndex(line)
			if loc == nil {
				continue
			}
			if err := c.add(rootIdx, rel, grepMatchAt(i+1, line, loc[0], loc[1])); err != nil {
				return err
			}
		}
		return nil
	})
}
