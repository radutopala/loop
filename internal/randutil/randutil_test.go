package randutil

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RandUtilSuite struct {
	suite.Suite
}

func TestRandUtilSuite(t *testing.T) {
	suite.Run(t, new(RandUtilSuite))
}

func (s *RandUtilSuite) TestHexIDLength() {
	require.Len(s.T(), HexID(2), 4)
	require.Len(s.T(), HexID(6), 12)
	require.Len(s.T(), HexID(16), 32)
}

func (s *RandUtilSuite) TestHexIDUnique() {
	a := HexID(6)
	b := HexID(6)
	require.NotEqual(s.T(), a, b)
}
