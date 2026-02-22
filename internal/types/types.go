package types

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
