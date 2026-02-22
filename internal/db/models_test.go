package db

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type ModelsSuite struct {
	suite.Suite
}

func TestModelsSuite(t *testing.T) {
	suite.Run(t, new(ModelsSuite))
}

func (s *ModelsSuite) TestIsEmptyTrue() {
	p := ChannelPermissions{}
	require.True(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestIsEmptyFalseOwnerUser() {
	p := ChannelPermissions{Owners: ChannelRoleGrant{Users: []string{"U1"}}}
	require.False(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestIsEmptyFalseOwnerRole() {
	p := ChannelPermissions{Owners: ChannelRoleGrant{Roles: []string{"admin"}}}
	require.False(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestIsEmptyFalseMemberUser() {
	p := ChannelPermissions{Members: ChannelRoleGrant{Users: []string{"U2"}}}
	require.False(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestIsEmptyFalseMemberRole() {
	p := ChannelPermissions{Members: ChannelRoleGrant{Roles: []string{"member-role"}}}
	require.False(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestGetRoleOwnerByUser() {
	p := ChannelPermissions{
		Owners: ChannelRoleGrant{Users: []string{"U1"}},
	}
	require.Equal(s.T(), types.RoleOwner, p.GetRole("U1", nil))
}

func (s *ModelsSuite) TestGetRoleOwnerByRole() {
	p := ChannelPermissions{
		Owners: ChannelRoleGrant{Roles: []string{"admin"}},
	}
	require.Equal(s.T(), types.RoleOwner, p.GetRole("U1", []string{"admin"}))
}

func (s *ModelsSuite) TestGetRoleMemberByUser() {
	p := ChannelPermissions{
		Members: ChannelRoleGrant{Users: []string{"U2"}},
	}
	require.Equal(s.T(), types.RoleMember, p.GetRole("U2", nil))
}

func (s *ModelsSuite) TestGetRoleMemberByRole() {
	p := ChannelPermissions{
		Members: ChannelRoleGrant{Roles: []string{"member-role"}},
	}
	require.Equal(s.T(), types.RoleMember, p.GetRole("U2", []string{"member-role"}))
}

func (s *ModelsSuite) TestGetRoleNone() {
	p := ChannelPermissions{
		Owners: ChannelRoleGrant{Users: []string{"U1"}},
	}
	require.Equal(s.T(), types.Role(""), p.GetRole("U2", nil))
}
