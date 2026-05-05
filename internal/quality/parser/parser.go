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

	// Body is per-function structural data extracted during the AST walk:
	// decision-point counts (cyclomatic, cognitive), nesting depth, parameter
	// count, and shape shingles for clone detection. Nil when the language
	// has no body-walker registered or when the parse failed before the
	// walker was invoked. Metrics packages tolerate nil and skip the function.
	Body *FunctionBody
}

// FunctionBody is the body-walk extract for one function. Populated once per
// function in the same DFS that produces FileFacts so both complexity and
// clone detection share the cost.
type FunctionBody struct {
	// LOC is the function-body line span (EndLine - StartLine + 1).
	LOC int

	// ParamCount is the number of declared parameters; receiver and
	// type-parameter clauses are excluded.
	ParamCount int

	// MaxNesting is the deepest nesting of structured constructs (if/for/
	// switch/while/do/case) seen anywhere in the body.
	MaxNesting int

	// DecisionPoints is the cyclomatic counter: 1 + count of branching nodes
	// (if, for, while, do, switch case, ternary, &&, ||).
	DecisionPoints int

	// CognitiveLoad is the Sonar-style cognitive complexity: each branching
	// construct contributes 1 + nesting penalty when nested inside another
	// branching construct.
	CognitiveLoad int

	// Shingles are 5-grams of normalised AST node-type tokens, hashed to
	// uint64 with FNV-1a. Identical token sequences produce identical
	// shingles, so two clones share a high overlap. Empty for trivial
	// functions (LOC < 2).
	Shingles []uint64
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
