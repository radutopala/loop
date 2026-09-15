package review

import (
	"strings"
	"unicode"
)

// duplicateThreshold is the word-bigram Dice coefficient at which two
// findings on the same line are treated as the same finding.
//
// Observed re-reports of one finding land around 0.85 — a review re-run
// re-derives the issue and writes it out in its own words, so most of the
// prose survives and only the opening sentence is rephrased. Two genuinely
// different claims about the same line share little beyond the identifiers
// they both mention and sit far below this. 0.7 leaves room on both sides of
// that gap rather than splitting it.
const duplicateThreshold = 0.7

// nearDuplicate reports whether two finding bodies say the same thing in
// different words.
//
// The id dedup in AddComment hashes the body, so it only catches a finding
// reported twice verbatim. That covers agent retries and nothing else: the
// "do NOT re-emit" list handed to the review subagents is prose, and prose
// cannot stop a subagent that re-derived an issue independently from
// reporting it in its own wording. This is the backstop for that, and it is
// deliberately mechanical — no model call, no network, same answer every
// time, so a wrong merge can be reproduced from the two bodies alone.
func nearDuplicate(a, b string) bool {
	return bodySimilarity(a, b) >= duplicateThreshold
}

// bodySimilarity is the Dice coefficient over the two bodies' word bigrams,
// in [0, 1].
//
// Bigrams rather than bare words because word order carries most of the
// meaning here: "cache write fails" and "write fails, cache" share every word
// and describe different things. Dice rather than Jaccard because it weights
// the overlap twice, which keeps a rewritten opening sentence from dragging
// an otherwise identical finding under the threshold.
func bodySimilarity(a, b string) float64 {
	ba, bb := bigrams(normalizeWords(a)), bigrams(normalizeWords(b))
	if len(ba) == 0 || len(bb) == 0 {
		// One of them is a single word or empty, so there are no bigrams to
		// compare. Fall back to the words themselves: a one-word finding is
		// either the same word or not related at all.
		wa, wb := normalizeWords(a), normalizeWords(b)
		if len(wa) == 0 || len(wb) == 0 {
			return 0
		}
		if strings.Join(wa, " ") == strings.Join(wb, " ") {
			return 1
		}
		return 0
	}
	shared := 0
	for g := range ba {
		if _, ok := bb[g]; ok {
			shared++
		}
	}
	return 2 * float64(shared) / float64(len(ba)+len(bb))
}

// normalizeWords lowercases and splits on everything that is not a letter or
// digit, so the punctuation a rewrite shuffles — backticks around an
// identifier, a comma that became a semicolon, "->" spelled as "→" — does not
// register as a difference.
func normalizeWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// bigrams is the set of adjacent word pairs. A set, not a list: a phrase
// repeated within one finding should not count for more than its presence.
func bigrams(words []string) map[[2]string]struct{} {
	out := make(map[[2]string]struct{}, len(words))
	for i := 0; i+1 < len(words); i++ {
		out[[2]string{words[i], words[i+1]}] = struct{}{}
	}
	return out
}
