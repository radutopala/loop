package types

import "strings"

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
