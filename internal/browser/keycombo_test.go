package browser

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type KeyComboSuite struct {
	suite.Suite
}

func TestKeyComboSuite(t *testing.T) {
	suite.Run(t, new(KeyComboSuite))
}

func (s *KeyComboSuite) TestParseKeyCombo() {
	tests := []struct {
		combo string
		key   string
		mods  int
	}{
		{"a", "a", 0},
		{"+", "+", 0},
		{"", "", 0},
		{"Enter", "Enter", 0},
		{"Return", "Enter", 0},
		{"return", "Enter", 0},
		{"KP_Enter", "Enter", 0},
		{"BackSpace", "Backspace", 0},
		{"Esc", "Escape", 0},
		{"space", " ", 0},
		{"Page_Down", "PageDown", 0},
		{"Up", "ArrowUp", 0},
		{"F5", "F5", 0},
		{"Control+a", "a", 2},
		{"ctrl+a", "a", 2},
		{"cmd+shift+t", "t", 4 | 8},
		{"Alt+Tab", "Tab", 1},
		{"ctrl+Return", "Enter", 2},
		{"ctrl + a", "a", 2},
		{"ctrl++", "+", 2},
		{"super+l", "l", 4},
		{"foo+a", "foo+a", 0},
	}
	for _, tt := range tests {
		s.Run(tt.combo, func() {
			key, mods := ParseKeyCombo(tt.combo)
			require.Equal(s.T(), tt.key, key)
			require.Equal(s.T(), tt.mods, mods)
		})
	}
}

func (s *KeyComboSuite) TestSpaceIsNamed() {
	nk, ok := resolveKey(" ")
	require.True(s.T(), ok)
	require.Equal(s.T(), namedKey{"Space", 32, " "}, nk)
}
