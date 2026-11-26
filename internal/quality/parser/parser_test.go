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
		"qux.py", "lib.rs",
	}
	for _, p := range known {
		require.Truef(s.T(), s.parser.Supports(p), "expected Supports(%q) = true", p)
	}
}

func (s *ParserSuite) TestSupportsRejectsUnknownExtensions() {
	unknown := []string{"foo.rb", "bar.java", "stub.pyi", "no-extension", "README.md"}
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

func (s *ParserSuite) TestParsePython() {
	facts, err := s.parser.Parse("sample.py", s.loadTestdata("sample.py"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "python", facts.Language)
	require.False(s.T(), facts.ParseFailed)
	require.Equal(s.T(), 15, facts.LOC)

	require.Equal(s.T(), []sigOnly{
		{Name: "greet", StartLine: 9, EndLine: 10},
		{Name: "make_widget", StartLine: 14, EndLine: 15},
	}, signatures(facts.Functions))
	require.Equal(s.T(), []TypeDecl{{Name: "Widget", StartLine: 8}}, facts.Types)
	require.Equal(s.T(), []Import{
		{Path: "os", StartLine: 1},
		{Path: "os/path", StartLine: 2},
		{Path: "./__init__", StartLine: 3},
		{Path: "./sibling", StartLine: 3},
		{Path: "../pkg/mod", StartLine: 4},
		{Path: "../pkg/mod/thing", StartLine: 4},
		{Path: "../pkg/mod/other", StartLine: 4},
		{Path: "pkg", StartLine: 5},
		{Path: "pkg/x", StartLine: 5},
	}, facts.Imports)
	require.Equal(s.T(), []Call{
		{Name: "helper", StartLine: 10},
		{Name: "build", StartLine: 15},
	}, facts.Calls)
}

func (s *ParserSuite) TestParseRust() {
	facts, err := s.parser.Parse("sample.rs", s.loadTestdata("sample.rs"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "rust", facts.Language)
	require.False(s.T(), facts.ParseFailed)

	require.Equal(s.T(), []sigOnly{
		{Name: "area", StartLine: 9, EndLine: 9},
		{Name: "new", StartLine: 13, EndLine: 18},
		{Name: "it_works", StartLine: 22, EndLine: 22},
	}, signatures(facts.Functions))
	entries := map[string]bool{}
	for _, f := range facts.Functions {
		entries[f.Name] = f.Entry
	}
	require.Equal(s.T(), map[string]bool{"area": false, "new": false, "it_works": true}, entries)
	require.Equal(s.T(), []TypeDecl{
		{Name: "Widget", StartLine: 7},
		{Name: "Shape", StartLine: 8},
		{Name: "Area", StartLine: 9},
		{Name: "Alias", StartLine: 10},
	}, facts.Types)
	// The inline `mod inline {}` names no file, so it isn't an import.
	require.Equal(s.T(), []Import{
		{Path: "std::collections::HashMap", StartLine: 1},
		{Path: "crate::graph::Node", StartLine: 2},
		{Path: "crate::graph::edge", StartLine: 2},
		{Path: "crate::graph::edge::Edge", StartLine: 2},
		{Path: "super", StartLine: 3},
		{Path: "self::inner", StartLine: 4},
	}, facts.Imports)
	require.Equal(s.T(), []Call{
		{Name: "helper", StartLine: 14},
		{Name: "new", StartLine: 15},
		{Name: "clone", StartLine: 16},
	}, facts.Calls)
}

func (s *ParserSuite) TestParseDropsEmptyImportPath() {
	facts, err := s.parser.Parse("empty.go", []byte("package p\n\nimport \"\"\n"))
	require.NoError(s.T(), err)
	require.Empty(s.T(), facts.Imports)
}

func (s *ParserSuite) TestPythonImportPaths() {
	cases := []struct {
		raw  string
		want []string
	}{
		{raw: "os", want: []string{"os"}},
		{raw: "a.b.c", want: []string{"a/b/c"}},
		{raw: "from . import sib", want: []string{"./__init__", "./sib"}},
		{raw: "from .. import x", want: []string{"../__init__", "../x"}},
		{raw: "from .sib import A, B as C", want: []string{"./sib", "./sib/A", "./sib/B"}},
		{raw: "from ..pkg.mod import thing", want: []string{"../pkg/mod", "../pkg/mod/thing"}},
		{raw: "from ...up import *", want: []string{"../../up"}},
		{raw: "from a.b import (\n    x,  # why\n    y,\n)", want: []string{"a/b", "a/b/x", "a/b/y"}},
		{raw: "from a import x, \\\n    y", want: []string{"a", "a/x", "a/y"}},
		{raw: "from . sib import z", want: []string{"./sib", "./sib/z"}},
	}
	for _, tc := range cases {
		require.Equalf(s.T(), tc.want, pythonImportPaths(tc.raw), "raw %q", tc.raw)
	}
}

func (s *ParserSuite) TestRustImportPaths() {
	cases := []struct {
		raw  string
		want []string
	}{
		{raw: "std::fmt", want: []string{"std::fmt"}},
		{raw: "::std::fmt", want: []string{"std::fmt"}},
		{raw: "crate::a::*", want: []string{"crate::a"}},
		{raw: "super::*", want: []string{"super"}},
		{raw: "crate::a as b", want: []string{"crate::a"}},
		{raw: "crate::{a::X, b::{self, Y as Z}}", want: []string{"crate::a::X", "crate::b", "crate::b::Y"}},
		{raw: "crate::{\n    a,\n    b,\n}", want: []string{"crate::a", "crate::b"}},
		{raw: "{a, b}", want: []string{"a", "b"}},
		{raw: "crate::{a, b", want: []string{"crate::a", "crate::b"}},
		{raw: "*", want: nil},
	}
	for _, tc := range cases {
		require.Equalf(s.T(), tc.want, rustImportPaths(tc.raw), "raw %q", tc.raw)
	}
}

func (s *ParserSuite) TestIsTestAttributed() {
	cases := []struct {
		src  string
		want bool
	}{
		{src: "#[test]\nfn f() {}", want: true},
		{src: "#[tokio::test]\nfn f() {}", want: true},
		{src: "#[test]\n// why\n#[ignore]\nfn f() {}", want: true},
		{src: "#[test_case(1)]\nfn f() {}", want: false},
		{src: "#[inline]\nfn f() {}", want: false},
		{src: "struct S;\nfn f() {}", want: false},
		{src: "fn f() {}", want: false},
	}
	lang := grammars.RustLanguage()
	for _, tc := range cases {
		tree, err := gotreesitter.NewParser(lang).Parse([]byte(tc.src))
		require.NoError(s.T(), err)
		fn := findFirstNamedDescendant(tree.RootNode(), lang, "function_item")
		require.NotNil(s.T(), fn, tc.src)
		require.Equalf(s.T(), tc.want, isTestAttributed(lang, fn, []byte(tc.src)), "src %q", tc.src)
	}
}

// --- Parse: edge cases ---

func (s *ParserSuite) TestParseRejectsUnsupportedExtension() {
	_, err := s.parser.Parse("foo.rb", []byte("puts 'hi'"))
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

func (s *ParserSuite) TestParseBodyPython() {
	facts, err := s.parser.Parse("complex.py", s.loadTestdata("complex.py"))
	require.NoError(s.T(), err)
	bodies := bodiesByName(facts.Functions)

	branchy := bodies["branchy"]
	require.NotNil(s.T(), branchy)
	require.Equal(s.T(), 19, branchy.LOC)
	// self and the bare `*` separator aren't parameters.
	require.Equal(s.T(), 3, branchy.ParamCount)
	require.Equal(s.T(), 3, branchy.MaxNesting)
	require.Equal(s.T(), 14, branchy.DecisionPoints)
	require.Equal(s.T(), 24, branchy.CognitiveLoad)

	trivial := bodies["trivial"]
	require.NotNil(s.T(), trivial)
	require.Equal(s.T(), 1, trivial.ParamCount)
	require.Equal(s.T(), 1, trivial.DecisionPoints)
	require.Equal(s.T(), 0, trivial.CognitiveLoad)
}

func (s *ParserSuite) TestParseBodyRust() {
	facts, err := s.parser.Parse("complex.rs", s.loadTestdata("complex.rs"))
	require.NoError(s.T(), err)
	bodies := bodiesByName(facts.Functions)

	branchy := bodies["branchy"]
	require.NotNil(s.T(), branchy)
	require.Equal(s.T(), 22, branchy.LOC)
	// &self is a receiver, not a parameter.
	require.Equal(s.T(), 3, branchy.ParamCount)
	require.Equal(s.T(), 3, branchy.MaxNesting)
	require.Equal(s.T(), 9, branchy.DecisionPoints)
	require.Equal(s.T(), 18, branchy.CognitiveLoad)

	trivial := bodies["trivial"]
	require.NotNil(s.T(), trivial)
	require.Equal(s.T(), 1, trivial.ParamCount)
	require.Equal(s.T(), 1, trivial.DecisionPoints)
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
	require.Equal(s.T(), 0, countParams(nil, nil, langProfile{}, nil))
}

func (s *ParserSuite) TestCountParamsMissingFieldOnNode() {
	src := []byte("package p\nfunc F() {}\n")
	lang := grammars.GoLanguage()
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	require.NoError(s.T(), err)
	fn := findFirstNamedDescendant(tree.RootNode(), lang, "function_declaration")
	require.NotNil(s.T(), fn)
	// Use a profile that points at a non-existent field — list lookup misses.
	require.Equal(s.T(), 0, countParams(lang, fn, langProfile{paramListField: "no_such_field"}, nil))
}

func (s *ParserSuite) TestCountParamsCountsNamedChildrenWhenChildTypeUnset() {
	src := []byte("function f(a, b, c) {}")
	lang := grammars.JavascriptLanguage()
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	require.NoError(s.T(), err)
	fn := findFirstNamedDescendant(tree.RootNode(), lang, "function_declaration")
	require.NotNil(s.T(), fn)
	require.Equal(s.T(), 3, countParams(lang, fn, jsProfile, src))
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
	require.Equal(s.T(), 0, countParams(lang, fn, mismatched, nil))
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
	for _, tc := range []struct{ node, want string }{
		{node: "shorthand_field_identifier", want: "IDENT"},
		{node: "string_content", want: "LIT_STR"},
		{node: "integer", want: "LIT_NUM"},
		{node: "float", want: "LIT_NUM"},
		{node: "integer_literal", want: "LIT_NUM"},
		{node: "char_literal", want: "LIT_NUM"},
		{node: "none", want: "LIT_BOOL"},
		{node: "boolean_literal", want: "LIT_BOOL"},
	} {
		require.Equalf(s.T(), tc.want, normaliseToken(tc.node), "node %q", tc.node)
	}
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
