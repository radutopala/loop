package terminal

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RingBufferSuite struct {
	suite.Suite
}

func TestRingBufferSuite(t *testing.T) {
	suite.Run(t, new(RingBufferSuite))
}

func (s *RingBufferSuite) TestNewRingBuffer() {
	rb := NewRingBuffer(16)
	require.Equal(s.T(), 0, rb.Len())
	require.Empty(s.T(), rb.Bytes())
}

func (s *RingBufferSuite) TestWriteAndRead() {
	rb := NewRingBuffer(16)

	n, err := rb.Write([]byte("hello"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), 5, n)
	require.Equal(s.T(), 5, rb.Len())
	require.Equal(s.T(), []byte("hello"), rb.Bytes())
}

func (s *RingBufferSuite) TestWriteEmpty() {
	rb := NewRingBuffer(16)
	n, err := rb.Write(nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, n)
	require.Equal(s.T(), 0, rb.Len())
}

func (s *RingBufferSuite) TestWriteMultiple() {
	rb := NewRingBuffer(16)

	_, _ = rb.Write([]byte("abc"))
	_, _ = rb.Write([]byte("def"))

	require.Equal(s.T(), 6, rb.Len())
	require.Equal(s.T(), []byte("abcdef"), rb.Bytes())
}

func (s *RingBufferSuite) TestWriteWrap() {
	rb := NewRingBuffer(8)

	_, _ = rb.Write([]byte("abcde")) // w=5, [abcde...]
	_, _ = rb.Write([]byte("fghij")) // wraps: [ijcdefgh] -> w=2, full=true
	// chronological: fghij, but buffer is 8 bytes, so keeps "cdefghij"

	require.Equal(s.T(), 8, rb.Len())
	require.Equal(s.T(), []byte("cdefghij"), rb.Bytes())
}

func (s *RingBufferSuite) TestWriteExactSize() {
	rb := NewRingBuffer(4)

	n, err := rb.Write([]byte("abcd"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), 4, n)
	require.Equal(s.T(), 4, rb.Len())
	require.Equal(s.T(), []byte("abcd"), rb.Bytes())
}

func (s *RingBufferSuite) TestWriteLargerThanBuffer() {
	rb := NewRingBuffer(4)

	n, err := rb.Write([]byte("abcdefgh"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), 8, n)
	require.Equal(s.T(), 4, rb.Len())
	require.Equal(s.T(), []byte("efgh"), rb.Bytes())
}

func (s *RingBufferSuite) TestWriteExactlyDoubleSize() {
	rb := NewRingBuffer(4)

	_, _ = rb.Write([]byte("ab"))       // w=2
	_, _ = rb.Write([]byte("cdefghij")) // >= size, keeps last 4

	require.Equal(s.T(), 4, rb.Len())
	require.Equal(s.T(), []byte("ghij"), rb.Bytes())
}

func (s *RingBufferSuite) TestReset() {
	rb := NewRingBuffer(8)

	_, _ = rb.Write([]byte("hello"))
	require.Equal(s.T(), 5, rb.Len())

	rb.Reset()
	require.Equal(s.T(), 0, rb.Len())
	require.Empty(s.T(), rb.Bytes())
}

func (s *RingBufferSuite) TestWrapThenRead() {
	rb := NewRingBuffer(4)

	_, _ = rb.Write([]byte("ab")) // w=2, [ab..]
	_, _ = rb.Write([]byte("cd")) // w=0, full, [abcd]
	require.Equal(s.T(), []byte("abcd"), rb.Bytes())

	_, _ = rb.Write([]byte("ef")) // w=2, full, [efcd]
	require.Equal(s.T(), []byte("cdef"), rb.Bytes())
}

func (s *RingBufferSuite) TestFillExactlyNoWrap() {
	rb := NewRingBuffer(4)

	_, _ = rb.Write([]byte("ab"))
	_, _ = rb.Write([]byte("cd"))

	require.Equal(s.T(), 4, rb.Len())
	require.Equal(s.T(), []byte("abcd"), rb.Bytes())
}
