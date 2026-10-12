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
	p := types.Permissions{}
	require.True(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestIsEmptyFalseOwnerUser() {
	p := types.Permissions{Owners: types.RoleGrant{Users: []string{"U1"}}}
	require.False(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestIsEmptyFalseOwnerRole() {
	p := types.Permissions{Owners: types.RoleGrant{Roles: []string{"admin"}}}
	require.False(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestIsEmptyFalseMemberUser() {
	p := types.Permissions{Members: types.RoleGrant{Users: []string{"U2"}}}
	require.False(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestIsEmptyFalseMemberRole() {
	p := types.Permissions{Members: types.RoleGrant{Roles: []string{"member-role"}}}
	require.False(s.T(), p.IsEmpty())
}

func (s *ModelsSuite) TestGetRoleOwnerByUser() {
	p := types.Permissions{
		Owners: types.RoleGrant{Users: []string{"U1"}},
	}
	require.Equal(s.T(), types.RoleOwner, p.GetRole("U1", nil))
}

func (s *ModelsSuite) TestGetRoleOwnerByRole() {
	p := types.Permissions{
		Owners: types.RoleGrant{Roles: []string{"admin"}},
	}
	require.Equal(s.T(), types.RoleOwner, p.GetRole("U1", []string{"admin"}))
}

func (s *ModelsSuite) TestGetRoleMemberByUser() {
	p := types.Permissions{
		Members: types.RoleGrant{Users: []string{"U2"}},
	}
	require.Equal(s.T(), types.RoleMember, p.GetRole("U2", nil))
}

func (s *ModelsSuite) TestGetRoleMemberByRole() {
	p := types.Permissions{
		Members: types.RoleGrant{Roles: []string{"member-role"}},
	}
	require.Equal(s.T(), types.RoleMember, p.GetRole("U2", []string{"member-role"}))
}

func (s *ModelsSuite) TestGetRoleNone() {
	p := types.Permissions{
		Owners: types.RoleGrant{Users: []string{"U1"}},
	}
	require.Equal(s.T(), types.Role(""), p.GetRole("U2", nil))
}

func (s *ModelsSuite) TestChannelLearnEnabled() {
	tests := []struct {
		override string
		def      bool
		want     bool
	}{
		{LearnOn, false, true},
		{LearnOff, true, false},
		{"", true, true},
		{"", false, false},
	}
	for _, tc := range tests {
		ch := &Channel{LearnOverride: tc.override}
		require.Equal(s.T(), tc.want, ch.LearnEnabled(tc.def), "override %q default %v", tc.override, tc.def)
		ch = &Channel{ExplainOverride: tc.override, LearnOverride: LearnOff}
		require.Equal(s.T(), tc.want, ch.ExplainEnabled(tc.def), "explain override %q default %v", tc.override, tc.def)
	}
}

func (s *ModelsSuite) TestIsHiddenKind() {
	for _, tc := range []struct {
		kind string
		want bool
	}{
		{ChannelKindLearn, true},
		{ChannelKindExplain, true},
		{PluginKind("cogtest"), true},
		{ChannelKindPluginPrefix, false},
		{"plugin", false},
		{"", false},
	} {
		require.Equal(s.T(), tc.want, IsHiddenKind(tc.kind), "kind %q", tc.kind)
	}
}

func (s *ModelsSuite) TestPluginOfKind() {
	for _, tc := range []struct {
		kind   string
		wantID string
		wantOK bool
	}{
		{"plugin:cogtest", "cogtest", true},
		{PluginKind("echo"), "echo", true},
		{"plugin:", "", false},
		{ChannelKindLearn, "", false},
		{"", "", false},
	} {
		id, ok := PluginOfKind(tc.kind)
		require.Equal(s.T(), tc.wantOK, ok, "kind %q", tc.kind)
		require.Equal(s.T(), tc.wantID, id, "kind %q", tc.kind)
	}
}
