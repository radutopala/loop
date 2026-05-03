package quality

import (
	"os"

	"github.com/odvcencio/gotreesitter/grammars"
)

// activeGrammarSet is the comma-separated list of tree-sitter grammars the
// engine is allowed to load. Restricting the set at process start keeps the
// embedded-grammar memory footprint bounded and matches the day-1 language
// scope (Go, TypeScript, JavaScript). Follow-on language PRs append entries
// here.
const activeGrammarSet = "go,typescript,javascript"

// embeddedLanguageCacheLimit caps the number of grammars resident in the
// gotreesitter LRU cache. Eight is enough to keep the active set hot without
// retaining grammars the engine has finished using.
const embeddedLanguageCacheLimit = 8

func init() {
	os.Setenv("GOTREESITTER_GRAMMAR_SET", activeGrammarSet)
	grammars.SetEmbeddedLanguageCacheLimit(embeddedLanguageCacheLimit)
}
