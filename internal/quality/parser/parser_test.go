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

	require.Equal(s.T(), []Function{
		{Name: "Hello", StartLine: 13, EndLine: 16},
		{Name: "MakeWidget", StartLine: 18, EndLine: 20},
	}, facts.Functions)
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

	require.Equal(s.T(), []Function{
		{Name: "hello", StartLine: 12, EndLine: 14},
		{Name: "makeWidget", StartLine: 17, EndLine: 19},
	}, facts.Functions)
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

	require.Equal(s.T(), []Function{
		{Name: "hello", StartLine: 5, EndLine: 7},
		{Name: "makeWidget", StartLine: 10, EndLine: 12},
	}, facts.Functions)
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

func (s *ParserSuite) TestCountLinesTrailingNewline() {
	require.Equal(s.T(), 0, countLines([]byte{}))
	require.Equal(s.T(), 1, countLines([]byte("a")))
	require.Equal(s.T(), 1, countLines([]byte("a\n")))
	require.Equal(s.T(), 2, countLines([]byte("a\nb")))
	require.Equal(s.T(), 2, countLines([]byte("a\nb\n")))
}
