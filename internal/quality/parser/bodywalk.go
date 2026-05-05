package parser

import (
	"hash/fnv"

	"github.com/odvcencio/gotreesitter"
)

// langProfile pairs a language name with its body-walk vocabulary. Each entry
// names tree-sitter node types; the walker uses these to count decision
// points, classify nesting parents, and find the parameter list.
type langProfile struct {
	// branching is the set of node types that contribute one to cyclomatic
	// complexity and one (plus nesting penalty) to cognitive load. Excludes
	// short-circuit operators — those are handled by binaryOpField below.
	branching map[string]struct{}

	// nestingParents is the set of structural constructs that increment the
	// cognitive-nesting depth for descendants. Most overlap with branching;
	// nestingParents excludes short-circuit operators (`&&`/`||`) which add
	// to complexity but should not increase nesting depth.
	nestingParents map[string]struct{}

	// shortCircuit is the node type whose `operator` field is `&&` or `||`.
	// Tree-sitter exposes these as binary expressions; we look at the operator
	// text to disambiguate.
	shortCircuit string

	// paramListField is the field name on a function-definition node whose
	// child is the parameter list; the walker counts that list's named
	// children. Empty means no parameter counting for this language.
	paramListField string

	// paramListChildType further constrains parameter-counting to direct
	// children of paramListField with this node type. Empty means count all
	// named direct children.
	paramListChildType string

	// paramNameLeafType, when set, switches parameter counting from "count
	// paramListChildType containers" to "count identifier leaves within
	// each container, with a floor of 1". Go needs this because
	// `(a, b, c int)` is a single parameter_declaration with three names —
	// the smell is the parameter count, not the declaration count.
	paramNameLeafType string
}

// goProfile, tsProfile, jsProfile cover the production grammars. New
// languages append a profile entry and register it in profiles.
var (
	goProfile = langProfile{
		branching: setOf(
			"if_statement",
			"for_statement",
			"expression_case",
			"type_case",
			"communication_case",
			"select_statement",
		),
		nestingParents: setOf(
			"if_statement",
			"for_statement",
			"expression_switch_statement",
			"type_switch_statement",
			"select_statement",
		),
		shortCircuit:       "binary_expression",
		paramListField:     "parameters",
		paramListChildType: "parameter_declaration",
		paramNameLeafType:  "identifier",
	}

	tsProfile = langProfile{
		branching: setOf(
			"if_statement",
			"for_statement",
			"for_in_statement",
			"while_statement",
			"do_statement",
			"switch_case",
			"ternary_expression",
			"catch_clause",
		),
		nestingParents: setOf(
			"if_statement",
			"for_statement",
			"for_in_statement",
			"while_statement",
			"do_statement",
			"switch_statement",
			"ternary_expression",
			"catch_clause",
		),
		shortCircuit:       "binary_expression",
		paramListField:     "parameters",
		paramListChildType: "",
	}

	jsProfile = tsProfile
)

var profiles = map[string]langProfile{
	"go":         goProfile,
	"typescript": tsProfile,
	"javascript": jsProfile,
}

func setOf(names ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// walkFunctionBody walks a function-definition subtree once and produces a
// FunctionBody. Returns nil when the language has no profile registered.
func walkFunctionBody(lang *gotreesitter.Language, langName string, fnDef *gotreesitter.Node, source []byte) *FunctionBody {
	if fnDef == nil {
		return nil
	}
	p, ok := profiles[langName]
	if !ok {
		return nil
	}

	body := &FunctionBody{
		LOC:        lineOf(fnDef.EndPoint()) - lineOf(fnDef.StartPoint()) + 1,
		ParamCount: countParams(lang, fnDef, p),
	}

	var tokens []string
	state := walkState{profile: p, lang: lang, source: source}
	state.visit(fnDef, 0, &body.DecisionPoints, &body.CognitiveLoad, &body.MaxNesting, &tokens)

	// First decision point is the function entry itself: cyclomatic = 1 + branches.
	body.DecisionPoints++

	body.Shingles = shingleTokens(tokens, 5)
	return body
}

type walkState struct {
	profile langProfile
	lang    *gotreesitter.Language
	source  []byte
}

func (s *walkState) visit(n *gotreesitter.Node, depth int, cyclomatic, cognitive, maxNesting *int, tokens *[]string) {
	if n == nil {
		return
	}
	nodeType := n.Type(s.lang)

	branches := false
	if _, ok := s.profile.branching[nodeType]; ok {
		branches = true
	} else if nodeType == s.profile.shortCircuit && isShortCircuit(n, s.lang, s.source) {
		branches = true
	}
	if branches {
		*cyclomatic++
		*cognitive += 1 + depth
	}

	nests := false
	if _, ok := s.profile.nestingParents[nodeType]; ok {
		nests = true
	}
	childDepth := depth
	if nests {
		childDepth = depth + 1
		if childDepth > *maxNesting {
			*maxNesting = childDepth
		}
	}

	*tokens = append(*tokens, normaliseToken(nodeType))

	for i := 0; i < n.NamedChildCount(); i++ {
		s.visit(n.NamedChild(i), childDepth, cyclomatic, cognitive, maxNesting, tokens)
	}
}

// isShortCircuit returns true when n is a binary expression whose operator
// text is `&&` or `||`. Tree-sitter grammars expose the operator either as a
// child node or as field-named "operator"; we read the text directly from the
// source bytes between the left/right operand boundaries.
func isShortCircuit(n *gotreesitter.Node, lang *gotreesitter.Language, source []byte) bool {
	op := n.ChildByFieldName("operator", lang)
	if op == nil {
		return false
	}
	t := op.Text(source)
	return t == "&&" || t == "||"
}

// countParams returns the parameter count of fnDef, looking up the field
// name in the profile. Receiver and type-parameter lists are excluded by
// design — only the field named in p.paramListField counts.
func countParams(lang *gotreesitter.Language, fnDef *gotreesitter.Node, p langProfile) int {
	if p.paramListField == "" {
		return 0
	}
	list := fnDef.ChildByFieldName(p.paramListField, lang)
	if list == nil {
		return 0
	}
	if p.paramListChildType == "" {
		return list.NamedChildCount()
	}
	count := 0
	for i := 0; i < list.NamedChildCount(); i++ {
		c := list.NamedChild(i)
		if c.Type(lang) != p.paramListChildType {
			continue
		}
		count += countLeaves(lang, c, p.paramNameLeafType)
	}
	return count
}

// countLeaves descends n's named subtree counting nodes whose Type matches
// leafType. Returns at least 1 — Go interface-style declarations like
// `func F(int)` have no identifier leaf but are still one parameter.
func countLeaves(lang *gotreesitter.Language, n *gotreesitter.Node, leafType string) int {
	count := 0
	var walk func(*gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node.Type(lang) == leafType {
			count++
			return
		}
		for i := 0; i < node.NamedChildCount(); i++ {
			walk(node.NamedChild(i))
		}
	}
	walk(n)
	if count == 0 {
		return 1
	}
	return count
}

// normaliseToken collapses identifier and literal node types onto stable
// generic tokens so two clones whose only difference is variable names or
// literal values still produce identical shingles.
func normaliseToken(nodeType string) string {
	switch nodeType {
	case "identifier", "type_identifier", "field_identifier", "package_identifier", "property_identifier":
		return "IDENT"
	case "interpreted_string_literal", "raw_string_literal", "string_literal", "string", "string_fragment", "template_string":
		return "LIT_STR"
	case "int_literal", "float_literal", "imaginary_literal", "rune_literal", "number":
		return "LIT_NUM"
	case "true", "false", "nil", "null", "undefined":
		return "LIT_BOOL"
	}
	return nodeType
}

// shingleTokens hashes every k-token window into a uint64 with FNV-1a.
// Returns an empty slice when the token stream is shorter than k. The
// hashes are deterministic across runs, which is required by the clone
// metric.
func shingleTokens(tokens []string, k int) []uint64 {
	if len(tokens) < k {
		return nil
	}
	out := make([]uint64, 0, len(tokens)-k+1)
	for i := 0; i+k <= len(tokens); i++ {
		h := fnv.New64a()
		for j := range k {
			_, _ = h.Write([]byte(tokens[i+j]))
			_, _ = h.Write([]byte{0})
		}
		out = append(out, h.Sum64())
	}
	return out
}
