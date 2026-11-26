package types

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// MaxTagLen caps one tag, in bytes.
	MaxTagLen = 32
	// MaxTags caps the tags on one channel or thread.
	MaxTags = 20
)

// tagPattern is what a tag may hold once lowercased: letters, digits and
// - _ . /, starting with a letter or digit. No spaces or colons, so a search
// like "tag:x tag:y text" splits cleanly.
var tagPattern = regexp.MustCompile(`^[\p{Ll}\p{N}][\p{Ll}\p{N}_./-]*$`)

// NormalizeTag trims and lowercases a tag, dropping a leading '#', and checks
// it's one the search can match.
func NormalizeTag(raw string) (string, error) {
	tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
	if tag == "" {
		return "", fmt.Errorf("a tag can't be empty")
	}
	if len(tag) > MaxTagLen {
		return "", fmt.Errorf("tag %q is longer than %d characters", tag, MaxTagLen)
	}
	if !tagPattern.MatchString(tag) {
		return "", fmt.Errorf("tag %q may hold only letters, digits and - _ . /, starting with a letter or digit", tag)
	}
	return tag, nil
}

// NormalizeTags normalizes each tag, then sorts them and drops duplicates.
func NormalizeTags(raw []string) ([]string, error) {
	tags := make([]string, 0, len(raw))
	for _, r := range raw {
		tag, err := NormalizeTag(r)
		if err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	return slices.Compact(tags), nil
}

// ErrTooManyTags is returned when an edit would leave a channel or thread
// with more than MaxTags.
var ErrTooManyTags = fmt.Errorf("a channel or thread can have at most %d tags", MaxTags)
