// Package parser extracts normalised structural facts (function and type
// definitions, import edges, call sites) from a single source file.
//
// The Parser interface lets downstream packages (graph, metrics) substitute
// a fake in unit tests instead of invoking gotreesitter on every case — cold
// parses are sub-second but cumulative across a 100% coverage suite.
package parser

// Parser turns a single file's source bytes into FileFacts.
type Parser interface {
	// Parse extracts structural facts. The path is used for language
	// detection (extension match) and as the FileFacts.Path field; the
	// file is never read from disk.
	Parse(path string, source []byte) (*FileFacts, error)

	// Supports reports whether the parser handles a file path.
	Supports(path string) bool
}

// FileFacts is the normalised extract from one source file.
type FileFacts struct {
	Path     string
	Language string
	LOC      int

	Functions []Function
	Types     []TypeDecl
	Imports   []Import
	Calls     []Call

	// ParseFailed signals the underlying parse hit an error or the GLR
	// safety cap. The engine treats parse-failed files as "skip + count" —
	// they do not contribute to the graph and do not crash the scan.
	ParseFailed bool
}

// Function is a named function or method definition.
type Function struct {
	Name      string
	StartLine int
	EndLine   int
}

// TypeDecl is a named type, class, interface, or struct definition.
type TypeDecl struct {
	Name      string
	StartLine int
}

// Import is a package or module import edge.
type Import struct {
	Path      string
	StartLine int
}

// Call is a function or method invocation site.
type Call struct {
	Name      string
	StartLine int
}
