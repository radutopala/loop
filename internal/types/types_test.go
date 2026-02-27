package types

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TypesSuite struct {
	suite.Suite
}

func TestTypesSuite(t *testing.T) {
	suite.Run(t, new(TypesSuite))
}

func (s *TypesSuite) TestTruncateString() {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{"short", "hello", 80, "hello"},
		{"exact", "abcde", 5, "abcde"},
		{"truncated", "abcdef", 5, "abcde..."},
		{"newlines", "line1\nline2\nline3", 80, "line1 line2 line3"},
		{"newlines and truncated", "line1\nline2\nline3", 11, "line1 line2..."},
		{"empty", "", 80, ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expected, TruncateString(tc.input, tc.maxLen))
		})
	}
}
