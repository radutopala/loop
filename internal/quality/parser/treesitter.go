package parser

import (
	"bytes"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

//go:embed queries/go.scm
var goQuery string

//go:embed queries/typescript.scm
var typescriptQuery string

//go:embed queries/javascript.scm
var javascriptQuery string

// LanguageSpec pairs a language name with its grammar and tags query. The
// constructor takes a slice of these so tests can inject broken inputs to
// exercise the compile-error path.
type LanguageSpec struct {
	Name       string
	Extensions []string
	Language   *gotreesitter.Language
	Query      string
}

// DefaultSpecs returns the production language specs: Go, TypeScript,
// JavaScript. Follow-on language PRs append here and to the
// GOTREESITTER_GRAMMAR_SET env var in internal/quality/grammars.go.
func DefaultSpecs() []LanguageSpec {
	return []LanguageSpec{
		{
			Name:       "go",
			Extensions: []string{".go"},
			Language:   grammars.GoLanguage(),
			Query:      goQuery,
		},
		{
			Name:       "typescript",
			Extensions: []string{".ts", ".tsx"},
			Language:   grammars.TypescriptLanguage(),
			Query:      typescriptQuery,
		},
		{
			Name:       "javascript",
			Extensions: []string{".js", ".jsx", ".mjs", ".cjs"},
			Language:   grammars.JavascriptLanguage(),
			Query:      javascriptQuery,
		},
	}
}

type languageHandle struct {
	name     string
	language *gotreesitter.Language
	query    *gotreesitter.Query
}

// parseFunc parses a source byte slice with a given language. Held as a
// struct field on TreeSitterParser so tests can inject failure modes.
type parseFunc func(lang *gotreesitter.Language, source []byte) (*gotreesitter.Tree, error)

// TreeSitterParser is the production Parser backed by gotreesitter. It is
// safe for concurrent use; gotreesitter parsers themselves are single-
// goroutine, so a fresh one is built per Parse call.
type TreeSitterParser struct {
	byExt   map[string]*languageHandle
	handles []*languageHandle
	parse   parseFunc
}

// New compiles each language's query against its grammar. Returns an error
// if any query fails to compile.
func New(specs []LanguageSpec) (*TreeSitterParser, error) {
	p := &TreeSitterParser{
		byExt:   make(map[string]*languageHandle),
		handles: make([]*languageHandle, 0, len(specs)),
		parse:   defaultParse,
	}
	for _, spec := range specs {
		q, err := gotreesitter.NewQuery(spec.Query, spec.Language)
		if err != nil {
			return nil, fmt.Errorf("compile %s query: %w", spec.Name, err)
		}
		h := &languageHandle{name: spec.Name, language: spec.Language, query: q}
		p.handles = append(p.handles, h)
		for _, ext := range spec.Extensions {
			p.byExt[strings.ToLower(ext)] = h
		}
	}
	return p, nil
}

func defaultParse(lang *gotreesitter.Language, source []byte) (*gotreesitter.Tree, error) {
	return gotreesitter.NewParser(lang).Parse(source)
}

// Supports implements Parser.
func (p *TreeSitterParser) Supports(path string) bool {
	return p.handleFor(path) != nil
}

func (p *TreeSitterParser) handleFor(path string) *languageHandle {
	return p.byExt[strings.ToLower(filepath.Ext(path))]
}

// Parse implements Parser. Files whose parse hits a hard error are returned
// with FileFacts.ParseFailed = true rather than as a Go error — the engine
// treats them as "skip + count" so a single bad file never crashes a scan.
func (p *TreeSitterParser) Parse(path string, source []byte) (*FileFacts, error) {
	handle := p.handleFor(path)
	if handle == nil {
		return nil, fmt.Errorf("unsupported file: %s", path)
	}

	facts := &FileFacts{
		Path:     path,
		Language: handle.name,
		LOC:      countLines(source),
	}

	tree, err := p.parse(handle.language, source)
	if err != nil || tree == nil {
		facts.ParseFailed = true
		return facts, nil
	}
	defer tree.Release()

	cursor := handle.query.Exec(tree.RootNode(), handle.language, source)
	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}
		applyMatch(facts, match, source)
	}
	return facts, nil
}

func applyMatch(facts *FileFacts, match gotreesitter.QueryMatch, source []byte) {
	var (
		fnName, typeName, callName, importPath string
		fnDef, typeDef, callSite, importSite   *gotreesitter.Node
	)
	for _, c := range match.Captures {
		switch c.Name {
		case "function.name":
			fnName = c.Text(source)
		case "function.def":
			fnDef = c.Node
		case "type.name":
			typeName = c.Text(source)
		case "type.def":
			typeDef = c.Node
		case "import.path":
			importPath = trimQuotes(c.Text(source))
		case "import.site":
			importSite = c.Node
		case "call.name":
			callName = c.Text(source)
		case "call.site":
			callSite = c.Node
		}
	}

	switch {
	case fnDef != nil && fnName != "":
		facts.Functions = append(facts.Functions, Function{
			Name:      fnName,
			StartLine: lineOf(fnDef.StartPoint()),
			EndLine:   lineOf(fnDef.EndPoint()),
		})
	case typeDef != nil && typeName != "":
		facts.Types = append(facts.Types, TypeDecl{
			Name:      typeName,
			StartLine: lineOf(typeDef.StartPoint()),
		})
	case importSite != nil && importPath != "":
		facts.Imports = append(facts.Imports, Import{
			Path:      importPath,
			StartLine: lineOf(importSite.StartPoint()),
		})
	case callSite != nil && callName != "":
		facts.Calls = append(facts.Calls, Call{
			Name:      callName,
			StartLine: lineOf(callSite.StartPoint()),
		})
	}
}

func lineOf(p gotreesitter.Point) int {
	return int(p.Row) + 1
}

func trimQuotes(s string) string {
	return strings.Trim(s, `"'`+"`")
}

func countLines(source []byte) int {
	if len(source) == 0 {
		return 0
	}
	n := bytes.Count(source, []byte{'\n'})
	if source[len(source)-1] != '\n' {
		n++
	}
	return n
}
