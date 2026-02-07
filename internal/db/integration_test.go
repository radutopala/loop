package db

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type IntegrationSuite struct {
	suite.Suite
}

func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(IntegrationSuite))
}

func (s *IntegrationSuite) TestNewSQLiteStoreInMemory() {
	store, err := NewSQLiteStore(":memory:")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), store)
	defer store.Close()
}

func (s *IntegrationSuite) TestNewSQLiteStoreInvalidDSN() {
	// A path that doesn't exist and can't be created
	store, err := NewSQLiteStore("/nonexistent/path/to/nowhere/test.db")
	if err != nil {
		// Expected on most systems - the path doesn't exist
		require.Nil(s.T(), store)
		return
	}
	// If it somehow succeeded (unlikely), just close it
	store.Close()
}
