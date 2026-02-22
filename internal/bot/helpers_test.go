package bot

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// HelpersSuite consolidates tests for the shared bot helper functions.
type HelpersSuite struct {
	suite.Suite
}

func TestHelpersSuite(t *testing.T) {
	suite.Run(t, new(HelpersSuite))
}

// --- HasCommandPrefix ---

func (s *HelpersSuite) TestHasCommandPrefix() {
	tests := []struct {
		content  string
		expected bool
	}{
		{"!loop hello", true},
		{"!LOOP hello", true},
		{"!Loop", true},
		{"!loopextra", true},
		{"!loop status", true},
		{"not a command", false},
		{"", false},
		{"!loo", false},
		{"hello !loop", false},
	}
	for _, tc := range tests {
		s.Run(tc.content, func() {
			require.Equal(s.T(), tc.expected, HasCommandPrefix(tc.content))
		})
	}
}

// --- StripPrefix ---

func (s *HelpersSuite) TestStripPrefix() {
	tests := []struct {
		content string
		want    string
	}{
		{"!loop hello", "hello"},
		{"!loop  multiple spaces", "multiple spaces"},
		{"!loop", ""},
		{"!loo", ""},
		{"!loop status", "status"},
	}
	for _, tc := range tests {
		s.Run(tc.content, func() {
			require.Equal(s.T(), tc.want, StripPrefix(tc.content))
		})
	}
}

// --- StripMention ---

func (s *HelpersSuite) TestStripMention() {
	tests := []struct {
		name    string
		content string
		botID   string
		want    string
	}{
		{"standard mention", "<@bot-1> hello", "bot-1", "hello"},
		{"nick mention", "<@!bot-1> hello", "bot-1", "hello"},
		{"both mentions", "<@bot-1> <@!bot-1> hello", "bot-1", "hello"},
		{"mention in middle", "hey <@bot-1> hello", "bot-1", "hey  hello"},
		{"no mention", "hello", "bot-1", "hello"},
		{"slack standard", "<@U123BOT> hello world", "U123BOT", "hello world"},
		{"slack no mention", "hello world", "U123BOT", "hello world"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, StripMention(tc.content, tc.botID))
		})
	}
}

// --- ReplaceTextMention ---

func (s *HelpersSuite) TestReplaceTextMention() {
	tests := []struct {
		name     string
		content  string
		username string
		mention  string
		want     string
	}{
		{"exact case", "@LoopBot check this", "LoopBot", "<@bot-1>", "<@bot-1> check this"},
		{"lowercase", "@loopbot check this", "LoopBot", "<@bot-1>", "<@bot-1> check this"},
		{"uppercase", "@LOOPBOT check this", "LoopBot", "<@bot-1>", "<@bot-1> check this"},
		{"mid sentence", "hey @LoopBot check this", "LoopBot", "<@bot-1>", "hey <@bot-1> check this"},
		{"no mention", "just a message", "LoopBot", "<@bot-1>", "just a message"},
		{"only mention", "@LoopBot", "LoopBot", "<@bot-1>", "<@bot-1>"},
		{"slack style", "hey @loopbot do this", "loopbot", "<@U123>", "hey <@U123> do this"},
		{"slack no mention", "no mention here", "loopbot", "<@U123>", "no mention here"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, ReplaceTextMention(tc.content, tc.username, tc.mention))
		})
	}
}

// --- SplitMessage ---

func (s *HelpersSuite) TestSplitMessage() {
	tests := []struct {
		name     string
		content  string
		maxLen   int
		expected []string
	}{
		{
			name:     "short message",
			content:  "hello",
			maxLen:   2000,
			expected: []string{"hello"},
		},
		{
			name:     "short message small limit",
			content:  "short",
			maxLen:   100,
			expected: []string{"short"},
		},
		{
			name:     "exact limit",
			content:  strings.Repeat("a", 2000),
			maxLen:   2000,
			expected: []string{strings.Repeat("a", 2000)},
		},
		{
			name:    "split on newline",
			content: strings.Repeat("a", 1500) + "\n" + strings.Repeat("b", 600),
			maxLen:  2000,
			expected: []string{
				strings.Repeat("a", 1500) + "\n",
				strings.Repeat("b", 600),
			},
		},
		{
			name:    "split on newline small",
			content: "line1\nline2",
			maxLen:  8,
			expected: []string{
				"line1\n",
				"line2",
			},
		},
		{
			name:    "split on space",
			content: strings.Repeat("a", 1500) + " " + strings.Repeat("b", 600),
			maxLen:  2000,
			expected: []string{
				strings.Repeat("a", 1500) + " ",
				strings.Repeat("b", 600),
			},
		},
		{
			name:    "split on space small",
			content: "word1 word2",
			maxLen:  8,
			expected: []string{
				"word1 ",
				"word2",
			},
		},
		{
			name:     "hard cut",
			content:  strings.Repeat("a", 2500),
			maxLen:   2000,
			expected: []string{strings.Repeat("a", 2000), strings.Repeat("a", 500)},
		},
		{
			name:    "multiple chunks",
			content: strings.Repeat("a", 5000),
			maxLen:  2000,
			expected: []string{
				strings.Repeat("a", 2000),
				strings.Repeat("a", 2000),
				strings.Repeat("a", 1000),
			},
		},
		{
			name:     "empty message",
			content:  "",
			maxLen:   2000,
			expected: []string{""},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			chunks := SplitMessage(tc.content, tc.maxLen)
			require.Equal(s.T(), tc.expected, chunks)
		})
	}
}

// --- FindCutPoint ---

func (s *HelpersSuite) TestFindCutPoint() {
	tests := []struct {
		name    string
		content string
		maxLen  int
		want    int
	}{
		{"newline", "hello\nworld this is long", 10, 6},
		{"space", "hello world", 10, 6},
		{"hard cut", "abcdefghij", 5, 5},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, FindCutPoint(tc.content, tc.maxLen))
		})
	}
}
