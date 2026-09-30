package apiauth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
)

type mockTokenStore struct {
	mock.Mock
}

func (m *mockTokenStore) InsertAPIToken(ctx context.Context, t *db.APIToken) error {
	return m.Called(ctx, t).Error(0)
}

func (m *mockTokenStore) DeleteAPITokens(ctx context.Context, containerID string) error {
	return m.Called(ctx, containerID).Error(0)
}

func (m *mockTokenStore) ListAPITokens(ctx context.Context) ([]*db.APIToken, error) {
	args := m.Called(ctx)
	v, _ := args.Get(0).([]*db.APIToken)
	return v, args.Error(1)
}

type RegistrySuite struct {
	suite.Suite
	store *mockTokenStore
	reg   *Registry
}

func TestRegistrySuite(t *testing.T) {
	suite.Run(t, new(RegistrySuite))
}

func (s *RegistrySuite) SetupTest() {
	s.store = new(mockTokenStore)
	s.reg = NewRegistry(s.store)
}

func (s *RegistrySuite) TearDownTest() {
	s.store.AssertExpectations(s.T())
}

func (s *RegistrySuite) TestIssueLookupRevoke() {
	var stored *db.APIToken
	s.store.On("InsertAPIToken", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		stored = args.Get(1).(*db.APIToken)
	}).Return(nil).Twice()
	s.store.On("DeleteAPITokens", mock.Anything, "c1").Return(nil).Once()

	tok, err := s.reg.Issue("c1", "ch1", "/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), hashToken(tok), stored.Hash)
	require.NotContains(s.T(), stored.Hash, tok, "only the hash is stored")
	other, err := s.reg.Issue("c2", "ch2", "")
	require.NoError(s.T(), err)

	p, ok := s.reg.Lookup(tok)
	require.True(s.T(), ok)
	require.Equal(s.T(), Principal{Kind: KindAgent, ContainerID: "c1", ChannelID: "ch1", DirPath: "/work"}, p)
	_, ok = s.reg.Lookup("nope")
	require.False(s.T(), ok)

	require.NoError(s.T(), s.reg.Revoke("c1"))
	_, ok = s.reg.Lookup(tok)
	require.False(s.T(), ok)
	_, ok = s.reg.Lookup(other)
	require.True(s.T(), ok)
}

func (s *RegistrySuite) TestIssueErrors() {
	boom := errors.New("boom")
	s.Run("rand", func() {
		s.reg.readRand = func([]byte) (int, error) { return 0, boom }
		_, err := s.reg.Issue("c1", "ch1", "")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("store", func() {
		s.SetupTest()
		s.store.On("InsertAPIToken", mock.Anything, mock.Anything).Return(boom).Once()
		_, err := s.reg.Issue("c1", "ch1", "")
		require.ErrorIs(s.T(), err, boom)
		require.Empty(s.T(), s.reg.byHash, "an unstored token isn't usable")
	})
}

func (s *RegistrySuite) TestLoad() {
	s.store.On("ListAPITokens", mock.Anything).Return([]*db.APIToken{
		{Hash: "h1", ContainerID: "live", ChannelID: "ch1", DirPath: "/a"},
		{Hash: "h2", ContainerID: "dead", ChannelID: "ch2"},
		{Hash: "h3", ContainerID: "dead", ChannelID: "ch2"},
	}, nil).Once()
	s.store.On("DeleteAPITokens", mock.Anything, "dead").Return(nil).Once()

	require.NoError(s.T(), s.reg.Load(context.Background(), func(id string) bool { return id == "live" }))
	require.Equal(s.T(), map[string]Principal{"h1": {Kind: KindAgent, ContainerID: "live", ChannelID: "ch1", DirPath: "/a"}}, s.reg.byHash)
}

func (s *RegistrySuite) TestLoadErrors() {
	boom := errors.New("boom")
	s.Run("list", func() {
		s.store.On("ListAPITokens", mock.Anything).Return(nil, boom).Once()
		require.ErrorIs(s.T(), s.reg.Load(context.Background(), nil), boom)
	})
	s.Run("delete", func() {
		s.store.On("ListAPITokens", mock.Anything).Return([]*db.APIToken{{Hash: "h", ContainerID: "dead"}}, nil).Once()
		s.store.On("DeleteAPITokens", mock.Anything, "dead").Return(boom).Once()
		require.ErrorIs(s.T(), s.reg.Load(context.Background(), func(string) bool { return false }), boom)
	})
}

func (s *RegistrySuite) TestWithoutStore() {
	reg := NewRegistry(nil)
	require.NoError(s.T(), reg.Load(context.Background(), nil))
	tok, err := reg.Issue("c1", "ch1", "")
	require.NoError(s.T(), err)
	_, ok := reg.Lookup(tok)
	require.True(s.T(), ok)
	require.NoError(s.T(), reg.Revoke("c1"))
	_, ok = reg.Lookup(tok)
	require.False(s.T(), ok)
}
