package procsource

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SourceForSuite struct {
	suite.Suite
}

func TestSourceForSuite(t *testing.T) {
	suite.Run(t, new(SourceForSuite))
}

// --- sourceForWith (testable core) ---

func (s *SourceForSuite) TestSourceForWithZeroPIDIsChat() {
	require.Equal(s.T(), "chat", sourceForWith(0, func(int) string { return "terminal:nope" }))
}

func (s *SourceForSuite) TestSourceForWithNegativePIDIsChat() {
	require.Equal(s.T(), "chat", sourceForWith(-1, func(int) string { return "terminal:nope" }))
}

func (s *SourceForSuite) TestSourceForWithEmptyLookupIsChat() {
	require.Equal(s.T(), "chat", sourceForWith(42, func(int) string { return "" }))
}

func (s *SourceForSuite) TestSourceForWithLookupResultPassesThrough() {
	got := sourceForWith(42, func(pid int) string {
		require.Equal(s.T(), 42, pid)
		return "terminal:leaf-7"
	})
	require.Equal(s.T(), "terminal:leaf-7", got)
}

// --- SourceFor (public wrapper) ---

func (s *SourceForSuite) TestSourceForZeroPIDPublic() {
	// Public-API smoke: zero PID always short-circuits to chat without
	// touching /proc.
	require.Equal(s.T(), "chat", SourceFor(0))
}

func (s *SourceForSuite) TestSourceForPublicReturnsWellKnownShape() {
	// Public SourceFor walks the real /proc tree at runtime; the result
	// depends on whether the test runner itself has LOOP_TERMINAL_LEAF in
	// its env. Both shapes are valid — we just assert the wrapper doesn't
	// invent a new form.
	got := SourceFor(1)
	require.True(s.T(), got == "chat" || strings.HasPrefix(got, "terminal:"),
		"unexpected SourceFor result: %q", got)
}

// --- Lookup (public wrapper of the OS-specific stub) ---

func (s *SourceForSuite) TestLookupOfUnknownPIDIsEmpty() {
	// Lookup contract: missing marker / unknown PID → "". A PID this large
	// is overwhelmingly unlikely to exist.
	require.Equal(s.T(), "", Lookup(999_999_999))
}
