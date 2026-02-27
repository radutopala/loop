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

func (s *TypesSuite) TestPermissionsIsEmpty() {
	tests := []struct {
		name   string
		perms  Permissions
		expect bool
	}{
		{"zero value", Permissions{}, true},
		{"owners users only", Permissions{Owners: RoleGrant{Users: []string{"U1"}}}, false},
		{"owners roles only", Permissions{Owners: RoleGrant{Roles: []string{"R1"}}}, false},
		{"members users only", Permissions{Members: RoleGrant{Users: []string{"U1"}}}, false},
		{"members roles only", Permissions{Members: RoleGrant{Roles: []string{"R1"}}}, false},
		{"all set", Permissions{
			Owners:  RoleGrant{Users: []string{"U1"}, Roles: []string{"R1"}},
			Members: RoleGrant{Users: []string{"U2"}},
		}, false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expect, tc.perms.IsEmpty())
		})
	}
}

func (s *TypesSuite) TestPermissionsGetRole() {
	tests := []struct {
		name        string
		perms       Permissions
		authorID    string
		authorRoles []string
		expect      Role
	}{
		{"empty config returns empty role", Permissions{}, "any-user", nil, ""},
		{"owner by user", Permissions{Owners: RoleGrant{Users: []string{"U1", "U2"}}}, "U1", nil, RoleOwner},
		{"owner by role", Permissions{Owners: RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R1"}, RoleOwner},
		{"member by user", Permissions{Members: RoleGrant{Users: []string{"U1"}}}, "U1", nil, RoleMember},
		{"member by role", Permissions{Members: RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R1"}, RoleMember},
		{"owner beats member", Permissions{
			Owners:  RoleGrant{Users: []string{"U1"}},
			Members: RoleGrant{Users: []string{"U1"}},
		}, "U1", nil, RoleOwner},
		{"user not in any list", Permissions{Owners: RoleGrant{Users: []string{"U1"}}}, "U2", nil, ""},
		{"role match as owner", Permissions{Owners: RoleGrant{Roles: []string{"R1"}}}, "U2", []string{"R1", "R2"}, RoleOwner},
		{"role match as member", Permissions{Members: RoleGrant{Roles: []string{"R2"}}}, "U2", []string{"R1", "R2"}, RoleMember},
		{"no role match", Permissions{Owners: RoleGrant{Roles: []string{"R1"}}}, "U3", []string{"R2", "R3"}, ""},
		{"nil author roles with role restriction", Permissions{Owners: RoleGrant{Roles: []string{"R1"}}}, "U1", nil, ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.expect, tc.perms.GetRole(tc.authorID, tc.authorRoles))
		})
	}
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
