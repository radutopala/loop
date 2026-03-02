package bot

import (
	"errors"
	"io"
	"log/slog"
	"os"
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

// --- RemoveMCPConfig ---

func (s *HelpersSuite) TestRemoveMCPConfig() {
	origRemove := osRemove
	s.T().Cleanup(func() { osRemove = origRemove })

	var removedPath string
	osRemove = func(name string) error {
		removedPath = name
		return nil
	}

	err := removeMCPConfig("/work", "chan-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/work/.loop/mcp-chan-1.json", removedPath)
}

func (s *HelpersSuite) TestRemoveMCPConfigNotExist() {
	origRemove := osRemove
	s.T().Cleanup(func() { osRemove = origRemove })

	osRemove = func(string) error { return os.ErrNotExist }

	err := removeMCPConfig("/work", "chan-1")
	require.NoError(s.T(), err)
}

func (s *HelpersSuite) TestRemoveMCPConfigError() {
	origRemove := osRemove
	s.T().Cleanup(func() { osRemove = origRemove })

	osRemove = func(string) error { return errors.New("permission denied") }

	err := removeMCPConfig("/work", "chan-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing MCP config")
}

func (s *HelpersSuite) TestRemoveMCPConfigEmptyDirPath() {
	err := removeMCPConfig("", "chan-1")
	require.NoError(s.T(), err)
}

func (s *HelpersSuite) TestRemoveMCPConfigVar() {
	// Verify the exported var points to the real implementation.
	require.NotNil(s.T(), RemoveMCPConfig)
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

// --- FormatThreadMessage ---

func (s *HelpersSuite) TestFormatThreadMessage() {
	tests := []struct {
		name          string
		botID         string
		botUsername   string
		mentionUserID string
		message       string
		want          string
	}{
		{
			name:  "message strips bot mention",
			botID: "B1", botUsername: "LoopBot",
			message: "<@B1> do the thing",
			want:    "<@B1> do the thing",
		},
		{
			name:  "message strips text mention",
			botID: "B1", botUsername: "LoopBot",
			message: "@LoopBot do the thing",
			want:    "<@B1> do the thing",
		},
		{
			name:  "message with user mention",
			botID: "B1", botUsername: "LoopBot",
			mentionUserID: "U42",
			message:       "do the thing",
			want:          "<@B1> do the thing <@U42>",
		},
		{
			name:    "message with empty bot username",
			botID:   "B1",
			message: "do the thing",
			want:    "<@B1> do the thing",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := FormatThreadMessage(tc.botID, tc.botUsername, tc.mentionUserID, tc.message)
			require.Equal(s.T(), tc.want, got)
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

// --- SweepOrphanedMCPConfigs ---

func (s *HelpersSuite) TestSweepOrphanedMCPConfigs() {
	origGlob := filepathGlob
	origRemove := osRemove
	s.T().Cleanup(func() { filepathGlob = origGlob; osRemove = origRemove })

	filepathGlob = func(string) ([]string, error) {
		return []string{
			"/work/.loop/mcp-known1.json",
			"/work/.loop/mcp-orphan1.json",
			"/work/.loop/mcp-orphan2.json",
		}, nil
	}

	var removed []string
	osRemove = func(name string) error {
		removed = append(removed, name)
		return nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	knownIDs := map[string]struct{}{"known1": {}}
	count := SweepOrphanedMCPConfigs([]string{"/work"}, knownIDs, logger)

	require.Equal(s.T(), 2, count)
	require.Equal(s.T(), []string{
		"/work/.loop/mcp-orphan1.json",
		"/work/.loop/mcp-orphan2.json",
	}, removed)
}

func (s *HelpersSuite) TestSweepOrphanedMCPConfigsNoOrphans() {
	origGlob := filepathGlob
	origRemove := osRemove
	s.T().Cleanup(func() { filepathGlob = origGlob; osRemove = origRemove })

	filepathGlob = func(string) ([]string, error) {
		return []string{"/work/.loop/mcp-known1.json"}, nil
	}

	osRemove = func(string) error {
		s.T().Fatal("osRemove should not be called")
		return nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	knownIDs := map[string]struct{}{"known1": {}}
	count := SweepOrphanedMCPConfigs([]string{"/work"}, knownIDs, logger)
	require.Equal(s.T(), 0, count)
}

func (s *HelpersSuite) TestSweepOrphanedMCPConfigsGlobError() {
	origGlob := filepathGlob
	s.T().Cleanup(func() { filepathGlob = origGlob })

	filepathGlob = func(string) ([]string, error) {
		return nil, errors.New("bad pattern")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	count := SweepOrphanedMCPConfigs([]string{"/work"}, nil, logger)
	require.Equal(s.T(), 0, count)
}

func (s *HelpersSuite) TestSweepOrphanedMCPConfigsRemoveError() {
	origGlob := filepathGlob
	origRemove := osRemove
	s.T().Cleanup(func() { filepathGlob = origGlob; osRemove = origRemove })

	filepathGlob = func(string) ([]string, error) {
		return []string{"/work/.loop/mcp-orphan1.json"}, nil
	}
	osRemove = func(string) error { return errors.New("permission denied") }

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	count := SweepOrphanedMCPConfigs([]string{"/work"}, nil, logger)
	require.Equal(s.T(), 0, count)
}

func (s *HelpersSuite) TestSweepOrphanedMCPConfigsEmptyDirPaths() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	count := SweepOrphanedMCPConfigs(nil, nil, logger)
	require.Equal(s.T(), 0, count)
}
