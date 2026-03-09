package container

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type mockContainerLister struct {
	ids []string
	err error
}

func (m *mockContainerLister) ContainerList(_ context.Context, _, _ string) ([]string, error) {
	return m.ids, m.err
}

type mockShellCreator struct {
	containerID string
	err         error
}

func (m *mockShellCreator) CreateShellContainer(_ context.Context, _, _ string) (string, error) {
	return m.containerID, m.err
}

type EnsurerSuite struct {
	suite.Suite
}

func TestEnsurerSuite(t *testing.T) {
	suite.Run(t, new(EnsurerSuite))
}

func (s *EnsurerSuite) TestFindsExistingContainer() {
	e := NewChannelContainerEnsurer(
		&mockContainerLister{ids: []string{"existing-123"}},
		&mockShellCreator{containerID: "should-not-be-used"},
	)

	id, err := e.FindContainerByChannel(context.Background(), "ch-1", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing-123", id)
}

func (s *EnsurerSuite) TestCreatesWhenNotFound() {
	e := NewChannelContainerEnsurer(
		&mockContainerLister{ids: []string{}},
		&mockShellCreator{containerID: "new-456"},
	)

	id, err := e.FindContainerByChannel(context.Background(), "ch-1", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-456", id)
}

func (s *EnsurerSuite) TestListError() {
	e := NewChannelContainerEnsurer(
		&mockContainerLister{err: errors.New("docker error")},
		&mockShellCreator{},
	)

	_, err := e.FindContainerByChannel(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing containers")
}

func (s *EnsurerSuite) TestCreateError() {
	e := NewChannelContainerEnsurer(
		&mockContainerLister{ids: []string{}},
		&mockShellCreator{err: errors.New("create failed")},
	)

	_, err := e.FindContainerByChannel(context.Background(), "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "create failed")
}
