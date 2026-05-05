package parser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ParserSuite struct {
	suite.Suite
	parser *TreeSitterParser
}

func TestParserSuite(t *testing.T) {
	suite.Run(t, new(ParserSuite))
}

func (s *ParserSuite) SetupTest() {
	p, err := New(DefaultSpecs())
	require.NoError(s.T(), err)
	s.parser = p
}

func (s *ParserSuite) loadTestdata(name string) []byte {
	src, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(s.T(), err)
	return src
}

// --- New / DefaultSpecs ---

func (s *ParserSuite) TestNewWithDefaultSpecsSucceeds() {
	p, err := New(DefaultSpecs())
	require.NoError(s.T(), err)
	require.NotNil(s.T(), p)
}

func (s *ParserSuite) TestNewReturnsErrorOnInvalidQuery() {
	_, err := New([]LanguageSpec{
		{
			Name:       "broken",
			Extensions: []string{".x"},
			Language:   grammars.GoLanguage(),
			Query:      "(this is not valid scheme syntax",
		},
	})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "compile broken query")
}

// --- Supports ---

func (s *ParserSuite) TestSupportsKnownExtensions() {
	known := []string{
		"foo.go", "Foo.GO",
		"bar.ts", "bar.tsx",
		"baz.js", "baz.jsx", "baz.mjs", "baz.cjs",
	}
	for _, p := range known {
		require.Truef(s.T(), s.parser.Supports(p), "expected Supports(%q) = true", p)
	}
}

func (s *ParserSuite) TestSupportsRejectsUnknownExtensions() {
	unknown := []string{"foo.py", "bar.rs", "no-extension", "README.md"}
	for _, p := range unknown {
		require.Falsef(s.T(), s.parser.Supports(p), "expected Supports(%q) = false", p)
	}
}

// --- Parse: per-language happy paths ---

func (s *ParserSuite) TestParseGo() {
	facts, err := s.parser.Parse("sample.go", s.loadTestdata("sample.go"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "go", facts.Language)
	require.False(s.T(), facts.ParseFailed)
	require.Equal(s.T(), 20, facts.LOC)

	require.Equal(s.T(), []sigOnly{
		{Name: "Hello", StartLine: 13, EndLine: 16},
		{Name: "MakeWidget", StartLine: 18, EndLine: 20},
	}, signatures(facts.Functions))
	require.Equal(s.T(), []TypeDecl{{Name: "Widget", StartLine: 9}}, facts.Types)
	require.Equal(s.T(), []Import{
		{Path: "fmt", StartLine: 4},
		{Path: "example.com/bar", StartLine: 6},
	}, facts.Imports)
	require.Equal(s.T(), []Call{
		{Name: "Println", StartLine: 14},
		{Name: "Greet", StartLine: 15},
	}, facts.Calls)
}

func (s *ParserSuite) TestParseTypeScript() {
	facts, err := s.parser.Parse("sample.ts", s.loadTestdata("sample.ts"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "typescript", facts.Language)
	require.False(s.T(), facts.ParseFailed)

	require.Equal(s.T(), []sigOnly{
		{Name: "hello", StartLine: 12, EndLine: 14},
		{Name: "makeWidget", StartLine: 17, EndLine: 19},
	}, signatures(facts.Functions))
	require.Equal(s.T(), []TypeDecl{
		{Name: "IFoo", StartLine: 5},
		{Name: "Bar", StartLine: 9},
		{Name: "Widget", StartLine: 11},
	}, facts.Types)
	require.Equal(s.T(), []Import{
		{Path: "./x", StartLine: 1},
		{Path: "./side-effect", StartLine: 2},
		{Path: "./y", StartLine: 3},
	}, facts.Imports)
	require.Equal(s.T(), []Call{{Name: "greet", StartLine: 13}}, facts.Calls)
}

func (s *ParserSuite) TestParseJavaScript() {
	facts, err := s.parser.Parse("sample.js", s.loadTestdata("sample.js"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "javascript", facts.Language)
	require.False(s.T(), facts.ParseFailed)

	require.Equal(s.T(), []sigOnly{
		{Name: "hello", StartLine: 5, EndLine: 7},
		{Name: "makeWidget", StartLine: 10, EndLine: 12},
	}, signatures(facts.Functions))
	require.Equal(s.T(), []TypeDecl{{Name: "Widget", StartLine: 4}}, facts.Types)
	require.Equal(s.T(), []Import{
		{Path: "./x", StartLine: 1},
		{Path: "./y", StartLine: 2},
	}, facts.Imports)
	require.Equal(s.T(), []Call{{Name: "greet", StartLine: 6}}, facts.Calls)
}

// --- Parse: edge cases ---

func (s *ParserSuite) TestParseRejectsUnsupportedExtension() {
	_, err := s.parser.Parse("foo.py", []byte("print('hi')"))
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unsupported file")
}

func (s *ParserSuite) TestParseEmptySource() {
	facts, err := s.parser.Parse("empty.go", []byte{})
	require.NoError(s.T(), err)
	require.False(s.T(), facts.ParseFailed)
	require.Equal(s.T(), 0, facts.LOC)
	require.Empty(s.T(), facts.Functions)
	require.Empty(s.T(), facts.Imports)
}

func (s *ParserSuite) TestParseSingleLineWithoutNewline() {
	facts, err := s.parser.Parse("oneline.go", []byte("package x"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, facts.LOC)
}

// --- Parse: failure mode (parse hook returns error) ---

func (s *ParserSuite) TestParseFailedWhenUnderlyingParseErrors() {
	s.parser.parse = func(_ *gotreesitter.Language, _ []byte) (*gotreesitter.Tree, error) {
		return nil, errors.New("synthetic")
	}
	facts, err := s.parser.Parse("foo.go", []byte("package foo"))
	require.NoError(s.T(), err)
	require.True(s.T(), facts.ParseFailed)
	require.Equal(s.T(), "go", facts.Language)
}

func (s *ParserSuite) TestParseFailedWhenUnderlyingParseReturnsNilTree() {
	s.parser.parse = func(_ *gotreesitter.Language, _ []byte) (*gotreesitter.Tree, error) {
		return nil, nil
	}
	facts, err := s.parser.Parse("foo.go", []byte("package foo"))
	require.NoError(s.T(), err)
	require.True(s.T(), facts.ParseFailed)
}

// --- Helpers (covered indirectly via Parse, but small direct cases for clarity) ---

func (s *ParserSuite) TestTrimQuotesHandlesAllWrappers() {
	require.Equal(s.T(), "abc", trimQuotes(`"abc"`))
	require.Equal(s.T(), "abc", trimQuotes(`'abc'`))
	require.Equal(s.T(), "abc", trimQuotes("`abc`"))
	require.Equal(s.T(), "abc", trimQuotes("abc"))
}

// sigOnly is a helper view of a Function that drops Body, so signature
// assertions stay readable even when the body walk produces large shingle
// arrays. Body is verified separately in the body-walk tests.
type sigOnly struct {
	Name      string
	StartLine int
	EndLine   int
}

func signatures(fns []Function) []sigOnly {
	out := make([]sigOnly, len(fns))
	for i, f := range fns {
		out[i] = sigOnly{Name: f.Name, StartLine: f.StartLine, EndLine: f.EndLine}
	}
	return out
}

// --- Body walk: per-language complexity & params ---

func (s *ParserSuite) TestParseBodyGo() {
	facts, err := s.parser.Parse("complex.go", s.loadTestdata("complex.go"))
	require.NoError(s.T(), err)
	bodies := bodiesByName(facts.Functions)

	branchy := bodies["Branchy"]
	require.NotNil(s.T(), branchy)
	require.Equal(s.T(), 20, branchy.LOC)
	require.Equal(s.T(), 3, branchy.ParamCount)
	require.Equal(s.T(), 3, branchy.MaxNesting)
	require.Equal(s.T(), 8, branchy.DecisionPoints)
	require.Equal(s.T(), 18, branchy.CognitiveLoad)
	require.NotEmpty(s.T(), branchy.Shingles)

	trivial := bodies["Trivial"]
	require.NotNil(s.T(), trivial)
	require.Equal(s.T(), 1, trivial.ParamCount)
	require.Equal(s.T(), 0, trivial.MaxNesting)
	require.Equal(s.T(), 1, trivial.DecisionPoints)
	require.Equal(s.T(), 0, trivial.CognitiveLoad)

	manyparams := bodies["Manyparams"]
	require.NotNil(s.T(), manyparams)
	require.Equal(s.T(), 5, manyparams.ParamCount)
}

func (s *ParserSuite) TestParseBodyTypeScript() {
	facts, err := s.parser.Parse("complex.ts", s.loadTestdata("complex.ts"))
	require.NoError(s.T(), err)
	bodies := bodiesByName(facts.Functions)

	branchy := bodies["branchy"]
	require.NotNil(s.T(), branchy)
	require.Equal(s.T(), 22, branchy.LOC)
	require.Equal(s.T(), 3, branchy.ParamCount)
	require.Equal(s.T(), 3, branchy.MaxNesting)
	require.Equal(s.T(), 9, branchy.DecisionPoints)
	require.Equal(s.T(), 19, branchy.CognitiveLoad)

	trivial := bodies["trivial"]
	require.NotNil(s.T(), trivial)
	require.Equal(s.T(), 1, trivial.DecisionPoints)
	require.Equal(s.T(), 0, trivial.CognitiveLoad)
}

func (s *ParserSuite) TestParseBodyJavaScript() {
	facts, err := s.parser.Parse("complex.js", s.loadTestdata("complex.js"))
	require.NoError(s.T(), err)
	bodies := bodiesByName(facts.Functions)

	branchy := bodies["branchy"]
	require.NotNil(s.T(), branchy)
	require.Equal(s.T(), 9, branchy.DecisionPoints)
	require.Equal(s.T(), 19, branchy.CognitiveLoad)
	require.Equal(s.T(), 3, branchy.MaxNesting)
}

func (s *ParserSuite) TestShingleStability() {
	src := s.loadTestdata("complex.go")
	a, err := s.parser.Parse("complex.go", src)
	require.NoError(s.T(), err)
	b, err := s.parser.Parse("complex.go", src)
	require.NoError(s.T(), err)
	require.Equal(s.T(), bodiesByName(a.Functions)["Branchy"].Shingles, bodiesByName(b.Functions)["Branchy"].Shingles)
}

func bodiesByName(fns []Function) map[string]*FunctionBody {
	out := make(map[string]*FunctionBody, len(fns))
	for _, f := range fns {
		out[f.Name] = f.Body
	}
	return out
}

// --- Body walk: defensive branches reached via direct calls ---

func (s *ParserSuite) TestWalkFunctionBodyNilNode() {
	require.Nil(s.T(), walkFunctionBody(nil, "go", nil, nil))
}

func (s *ParserSuite) TestWalkFunctionBodyUnknownLanguage() {
	// Build a real Go fnDef so the nil-node branch isn't the path under test.
	src := []byte("package p\nfunc F() {}\n")
	lang := grammars.GoLanguage()
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	require.NoError(s.T(), err)
	fnDef := findFirstNamedDescendant(tree.RootNode(), lang, "function_declaration")
	require.NotNil(s.T(), fnDef)
	require.Nil(s.T(), walkFunctionBody(lang, "fortran", fnDef, src))
}

func (s *ParserSuite) TestVisitNilNodeReturns() {
	state := walkState{profile: goProfile, lang: grammars.GoLanguage(), source: nil}
	var cyc, cog, nest int
	var tokens []string
	state.visit(nil, 0, &cyc, &cog, &nest, &tokens)
	require.Equal(s.T(), 0, cyc)
}

func (s *ParserSuite) TestIsShortCircuitOnNonBinaryNode() {
	src := []byte("package p\nfunc F() { x := 1; _ = x }\n")
	lang := grammars.GoLanguage()
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	require.NoError(s.T(), err)
	fn := findFirstNamedDescendant(tree.RootNode(), lang, "function_declaration")
	require.NotNil(s.T(), fn)
	// A function_declaration has no `operator` field, so isShortCircuit
	// must take the `op == nil` early return.
	require.False(s.T(), isShortCircuit(fn, lang, src))
}

func (s *ParserSuite) TestCountParamsNoParamListField() {
	require.Equal(s.T(), 0, countParams(nil, nil, langProfile{}))
}

func (s *ParserSuite) TestCountParamsMissingFieldOnNode() {
	src := []byte("package p\nfunc F() {}\n")
	lang := grammars.GoLanguage()
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	require.NoError(s.T(), err)
	fn := findFirstNamedDescendant(tree.RootNode(), lang, "function_declaration")
	require.NotNil(s.T(), fn)
	// Use a profile that points at a non-existent field — list lookup misses.
	require.Equal(s.T(), 0, countParams(lang, fn, langProfile{paramListField: "no_such_field"}))
}

func (s *ParserSuite) TestCountParamsCountsNamedChildrenWhenChildTypeUnset() {
	src := []byte("function f(a, b, c) {}")
	lang := grammars.JavascriptLanguage()
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	require.NoError(s.T(), err)
	fn := findFirstNamedDescendant(tree.RootNode(), lang, "function_declaration")
	require.NotNil(s.T(), fn)
	require.Equal(s.T(), 3, countParams(lang, fn, jsProfile))
}

func (s *ParserSuite) TestCountParamsSkipsNonMatchingChildType() {
	// Force the type-mismatch branch by passing a paramListChildType that
	// doesn't match the actual parameter_declaration type.
	src := []byte("package p\nfunc F(a int) {}\n")
	lang := grammars.GoLanguage()
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	require.NoError(s.T(), err)
	fn := findFirstNamedDescendant(tree.RootNode(), lang, "function_declaration")
	require.NotNil(s.T(), fn)
	mismatched := langProfile{
		paramListField:     "parameters",
		paramListChildType: "no_such_type",
		paramNameLeafType:  "identifier",
	}
	require.Equal(s.T(), 0, countParams(lang, fn, mismatched))
}

func (s *ParserSuite) TestCountLeavesEmptySubtreeCountsOne() {
	src := []byte("package p\nfunc F() {}\n")
	lang := grammars.GoLanguage()
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	require.NoError(s.T(), err)
	fn := findFirstNamedDescendant(tree.RootNode(), lang, "function_declaration")
	require.NotNil(s.T(), fn)
	// "no_such_leaf" never appears, so countLeaves's count==0 floor kicks in.
	require.Equal(s.T(), 1, countLeaves(lang, fn, "no_such_leaf"))
}

func (s *ParserSuite) TestNormaliseTokenAllBranches() {
	require.Equal(s.T(), "IDENT", normaliseToken("identifier"))
	require.Equal(s.T(), "LIT_STR", normaliseToken("string_literal"))
	require.Equal(s.T(), "LIT_NUM", normaliseToken("int_literal"))
	require.Equal(s.T(), "LIT_BOOL", normaliseToken("true"))
	require.Equal(s.T(), "LIT_BOOL", normaliseToken("false"))
	require.Equal(s.T(), "LIT_BOOL", normaliseToken("nil"))
	require.Equal(s.T(), "LIT_BOOL", normaliseToken("null"))
	require.Equal(s.T(), "LIT_BOOL", normaliseToken("undefined"))
	require.Equal(s.T(), "block", normaliseToken("block"))
}

func (s *ParserSuite) TestShingleTokensBelowK() {
	require.Nil(s.T(), shingleTokens([]string{"a", "b"}, 5))
}

func findFirstNamedDescendant(n *gotreesitter.Node, lang *gotreesitter.Language, nodeType string) *gotreesitter.Node {
	if n == nil {
		return nil
	}
	if n.IsNamed() && n.Type(lang) == nodeType {
		return n
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		if found := findFirstNamedDescendant(n.NamedChild(i), lang, nodeType); found != nil {
			return found
		}
	}
	return nil
}

func (s *ParserSuite) TestCountLinesTrailingNewline() {
	require.Equal(s.T(), 0, countLines([]byte{}))
	require.Equal(s.T(), 1, countLines([]byte("a")))
	require.Equal(s.T(), 1, countLines([]byte("a\n")))
	require.Equal(s.T(), 2, countLines([]byte("a\nb")))
	require.Equal(s.T(), 2, countLines([]byte("a\nb\n")))
}
