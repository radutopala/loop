package bot

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// HandlersSuite consolidates tests for the generic handler helpers.
type HandlersSuite struct {
	suite.Suite
}

func TestHandlersSuite(t *testing.T) {
	suite.Run(t, new(HandlersSuite))
}

func (s *HandlersSuite) TestRegisterHandler_AppendsToSlice() {
	var mu sync.RWMutex
	var handlers []func()

	called := false
	RegisterHandler(&mu, &handlers, func() { called = true })

	require.Len(s.T(), handlers, 1)
	handlers[0]()
	require.True(s.T(), called)
}

func (s *HandlersSuite) TestRegisterHandler_MultipleHandlers() {
	var mu sync.RWMutex
	var handlers []string

	RegisterHandler(&mu, &handlers, "a")
	RegisterHandler(&mu, &handlers, "b")
	RegisterHandler(&mu, &handlers, "c")

	require.Equal(s.T(), []string{"a", "b", "c"}, handlers)
}

func (s *HandlersSuite) TestCopyHandlers_ReturnsSnapshot() {
	var mu sync.RWMutex
	handlers := []string{"a", "b"}

	cp := CopyHandlers(&mu, handlers)

	require.Equal(s.T(), []string{"a", "b"}, cp)

	// Modifying the copy must not affect the original.
	cp[0] = "x"
	require.Equal(s.T(), "a", handlers[0])
}

func (s *HandlersSuite) TestCopyHandlers_EmptySlice() {
	var mu sync.RWMutex
	var handlers []int

	cp := CopyHandlers(&mu, handlers)

	require.NotNil(s.T(), cp)
	require.Empty(s.T(), cp)
}
