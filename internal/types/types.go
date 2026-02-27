package types

import (
	"slices"
	"strings"
)

// TruncateString replaces newlines with spaces and truncates s to maxLen
// characters, appending "..." if truncated.
func TruncateString(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Platform represents the chat platform type.
type Platform string

const (
	PlatformDiscord Platform = "discord"
	PlatformSlack   Platform = "slack"
)

// Role represents the RBAC role for channel access control.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// RoleGrant lists the users and platform role IDs that are granted a specific RBAC role.
type RoleGrant struct {
	Users []string `json:"users"` // platform user IDs
	Roles []string `json:"roles"` // Discord role IDs (ignored on Slack)
}

// Permissions configures RBAC with owner and member roles.
// An empty config (all slices nil/empty) allows all users as owners (bootstrap mode).
type Permissions struct {
	Owners  RoleGrant `json:"owners"`
	Members RoleGrant `json:"members"`
}

// IsEmpty returns true when no role grants are configured.
func (p Permissions) IsEmpty() bool {
	return len(p.Owners.Users) == 0 && len(p.Owners.Roles) == 0 &&
		len(p.Members.Users) == 0 && len(p.Members.Roles) == 0
}

// GetRole returns the role for the given author based on grants.
// Returns "" when the author is not granted any role.
func (p Permissions) GetRole(authorID string, authorRoles []string) Role {
	if slices.Contains(p.Owners.Users, authorID) {
		return RoleOwner
	}
	for _, r := range authorRoles {
		if slices.Contains(p.Owners.Roles, r) {
			return RoleOwner
		}
	}
	if slices.Contains(p.Members.Users, authorID) {
		return RoleMember
	}
	for _, r := range authorRoles {
		if slices.Contains(p.Members.Roles, r) {
			return RoleMember
		}
	}
	return ""
}
