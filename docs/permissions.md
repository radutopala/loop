---
title: Permission & RBAC System
---

# Permission & RBAC System

Loop uses a role-based access control (RBAC) system to control who can interact with the bot and manage its configuration. The system is intentionally simple -- two roles, two sources of truth, and a bootstrap mechanism for first-time setup.

## Role Hierarchy

There are exactly two roles:

| Role | Level | Capabilities |
|---|---|---|
| **Owner** | Highest | All commands including permission management (`allow_user`, `allow_role`, `deny_user`, `deny_role`) |
| **Member** | Standard | All commands except permission management |
| *(none)* | No access | Messages and commands are silently ignored |

There is no inheritance between roles. Owners and Members are granted independently. A user is either an Owner, a Member, or has no access.

## Permission Sources

Permissions come from two sources that are merged at evaluation time:

### 1. Config File

The global config file (`~/.loop/config.json`) defines a base set of permissions:

```json
{
  "permissions": {
    "owners": {
      "users": ["discord-user-id-1", "slack-user-id-2"],
      "roles": ["discord-role-id-admin"]
    },
    "members": {
      "users": ["discord-user-id-3"],
      "roles": ["discord-role-id-dev"]
    }
  }
}
```

Project-specific config files (`{workDir}/.loop/config.json`) can override the global permissions. When a project config has a `permissions` block, it **replaces** the global permissions entirely for channels using that work directory.

The `configPermissionsFor(dirPath)` method handles this: it loads the project config via `LoadProjectConfig` and returns its permissions if available, falling back to the global config on error.

### 2. Database

Each channel stores its own `Permissions` in the database. These are managed at runtime via the `allow_user`, `allow_role`, `deny_user`, and `deny_role` commands.

Database permissions are per-channel and stored as a JSON column on the `channels` table. They use the same `types.Permissions` structure as config permissions.

## Merge Logic

The `resolveRole` function merges permissions from both sources. The algorithm:

```go
func resolveRole(cfgPerms, dbPerms Permissions, authorID string, authorRoles []string) Role {
    // Bootstrap rule: if BOTH sources are completely empty, everyone is Owner
    if cfgPerms.IsEmpty() && dbPerms.IsEmpty() {
        return RoleOwner
    }

    // Evaluate each source independently
    cfgRole := cfgPerms.GetRole(authorID, authorRoles)
    dbRole := dbPerms.GetRole(authorID, authorRoles)

    // Higher role wins
    if cfgRole == RoleOwner || dbRole == RoleOwner {
        return RoleOwner
    }
    if cfgRole == RoleMember || dbRole == RoleMember {
        return RoleMember
    }

    // Neither source grants a role
    return ""
}
```

Key behaviors:

- **Higher role wins** -- If the config grants "member" but the database grants "owner", the user is an Owner. This works in both directions.
- **Additive** -- A user denied in config can still be granted access via database commands, and vice versa. There is no explicit "deny" that overrides a grant from the other source.
- **Independent evaluation** -- Each source is checked separately. A user only needs to appear in one source to have access.

## Bootstrap Rule

When **both** config and database permissions are completely empty (no users, no roles in either owners or members), the system enters bootstrap mode. In this mode, every user is treated as an Owner.

This serves two purposes:

1. **First-time setup** -- A freshly deployed bot has no permissions configured. Without bootstrap mode, nobody could interact with it. With bootstrap mode, any user can send messages and use commands, including `iamtheowner` to claim ownership.
2. **Migration** -- Existing deployments that never configured permissions continue to work with full access for all users.

The `IsEmpty()` check tests all four lists:

```go
func (p Permissions) IsEmpty() bool {
    return len(p.Owners.Users) == 0 && len(p.Owners.Roles) == 0 &&
        len(p.Members.Users) == 0 && len(p.Members.Roles) == 0
}
```

### Exiting Bootstrap Mode

Bootstrap mode ends as soon as any user or role is added to either source. Once `iamtheowner` is used (which adds the invoking user to DB permissions as an owner), or once permissions are added to the config file, bootstrap mode is no longer active and only explicitly granted users/roles have access.

The `iamtheowner` command validates bootstrap mode before executing:

1. Check that `cfgPerms.IsEmpty() && dbPerms.IsEmpty()`.
2. If either has any entries, reject with: `An owner is already configured. Use /loop allow_user to manage permissions.`
3. Add the invoking user as an owner in the database permissions.

## Role Evaluation

The `GetRole` function in `types.Permissions` evaluates a user's role by checking lists in priority order:

1. **Owner users** -- If `authorID` is in `Owners.Users`, return `RoleOwner`.
2. **Owner roles** -- If any of `authorRoles` (Discord role IDs) is in `Owners.Roles`, return `RoleOwner`.
3. **Member users** -- If `authorID` is in `Members.Users`, return `RoleMember`.
4. **Member roles** -- If any of `authorRoles` is in `Members.Roles`, return `RoleMember`.
5. **No match** -- Return `""` (no role).

The check is short-circuiting: once a match is found at a higher level, lower levels are not checked.

## Discord Role Support

Discord supports role-based permissions. When a message or interaction arrives, the bot fetches the user's guild member roles via `GuildMember` and populates the `AuthorRoles` field on the `IncomingMessage` or `Interaction`.

This allows granting access to entire Discord server roles:

```json
{
  "permissions": {
    "owners": {
      "roles": ["123456789"]
    },
    "members": {
      "roles": ["987654321"]
    }
  }
}
```

Commands `allow_role` and `deny_role` manage role-based grants in the database at runtime:

```
/loop allow_role @Developers member
/loop deny_role @Developers
```

Role mentions in the response use Discord's role mention format: `Role <@&ROLE_ID>`.

## Slack Limitations

Slack does not have a native role system comparable to Discord's server roles. Consequently:

- The `Roles` field in `RoleGrant` is effectively ignored for Slack users.
- `allow role` and `deny role` commands explicitly return an error: `"allow role is Discord-only (role-based permissions are not supported on Slack)"`.
- Permission management on Slack is limited to `allow user` and `deny user`.
- `GetMemberRoles` returns `nil` on the Slack bot.

## Local Platform Auto-Grant

The local platform (Electron desktop app) automatically grants Owner access to all users. This happens at two levels:

### Message Handling

In `HandleMessage`, the permission check is entirely skipped when `msg.Platform == types.PlatformLocal`:

```go
if !o.bot.IsBotUser(msg.AuthorID) && msg.Platform != types.PlatformLocal {
    // ... permission check ...
}
```

### Interaction Handling

In `HandleInteraction`, if the resolved role is empty and the platform is Local, the role is upgraded to Owner:

```go
if inter.Platform == types.PlatformLocal && role == "" {
    role = types.RoleOwner
}
```

This ensures the desktop user always has full access, regardless of what the config file or database permissions say.

## Permission Propagation to Threads

When a thread is created (either via user action or scheduled task), it inherits the parent channel's permissions:

```go
store.UpsertChannel(ctx, &db.Channel{
    ChannelID:   threadID,
    Permissions: parent.Permissions,  // inherited
    // ... other inherited fields ...
})
```

This applies to:

- **Thread resolution** (`resolveThread`) -- When a message arrives in an unregistered thread with an active parent.
- **Task thread creation** (`ExecuteTask`) -- When a scheduled task creates a thread for its output.
- **Simple thread creation** (`CreateSimpleThread` on local bot) -- When the Electron app creates a new thread.

Inherited permissions are database permissions from the parent. Config-file permissions are resolved independently based on the `DirPath`, which is also inherited from the parent.

After a thread is created, its permissions are independent of the parent. Changes to the parent's permissions do not retroactively affect existing threads. However, since config-file permissions are resolved fresh on each request based on `DirPath`, config changes do take effect on threads sharing the same work directory.

## allow/deny Command Behavior

The `handlePermissionUpdate` function implements all four permission commands (`allow_user`, `allow_role`, `deny_user`, `deny_role`):

### Allow (Grant)

1. Remove the target from **both** owners and members lists (clean slate).
2. Add the target to the specified role's list (default: member).
3. Save the updated permissions to the database.

This ensures a user/role has exactly one database-granted role. Upgrading from member to owner works by removing from members and adding to owners.

### Deny (Revoke)

1. Remove the target from **both** owners and members lists.
2. Save the updated permissions to the database.

Note that `deny` only affects database permissions. If the user or role is also granted access in the config file, they will retain that access. To fully revoke access, the user/role must be removed from both the config file and the database.

## Permission Check Points

Permissions are checked at two points in the system:

### 1. Message Processing

In `HandleMessage`, after a message is triggered (mention, reply, prefix, DM), the orchestrator checks:

- Is the author the bot itself? If yes, bypass (self-mentions from MCP tools should work).
- Is the platform Local? If yes, bypass.
- Does the author have a role (Owner or Member)? If no, silently ignore the message (log entry only, no error message to user).

### 2. Interaction Processing

In `HandleInteraction`, before dispatching to the command handler:

- Permission commands (`allow_user`, `allow_role`, `deny_user`, `deny_role`) require `RoleOwner`. Non-owners see: `Only owners can manage permissions.`
- `iamtheowner` bypasses normal checks (validated internally for bootstrap mode).
- All other commands require any role (Owner or Member). No role sees: `You don't have permission to use this command.`

## Configuration Examples

### Minimal Setup (Bootstrap Mode)

No permissions block in config -- all users are treated as owners:

```json
{
  "platforms": ["discord"]
}
```

### Single Owner

Only one user has full control:

```json
{
  "permissions": {
    "owners": {
      "users": ["123456789"]
    }
  }
}
```

All other users have no access and cannot use the bot.

### Owner + Members

An owner manages the bot; team members can use it:

```json
{
  "permissions": {
    "owners": {
      "users": ["123456789"]
    },
    "members": {
      "users": ["111111111", "222222222"]
    }
  }
}
```

### Discord Role-Based

Access by Discord server roles:

```json
{
  "permissions": {
    "owners": {
      "roles": ["admin-role-id"]
    },
    "members": {
      "roles": ["developer-role-id", "qa-role-id"]
    }
  }
}
```

### Mixed Users and Roles

```json
{
  "permissions": {
    "owners": {
      "users": ["specific-admin-user"],
      "roles": ["admin-role"]
    },
    "members": {
      "users": ["contractor-user"],
      "roles": ["team-role"]
    }
  }
}
```

### Project-Specific Override

In `{workDir}/.loop/config.json`, permissions replace the global config for channels using this directory:

```json
{
  "permissions": {
    "owners": {
      "users": ["project-lead"]
    },
    "members": {
      "users": ["project-dev-1", "project-dev-2"]
    }
  }
}
```

## Related Documentation

- [Platform Support](platforms.md) -- Platform-specific role handling and auto-grant behavior
- [Slash Commands & Interactions](commands.md) -- Per-command permission requirements
- [Orchestrator & Message Processing](orchestrator.md) -- Where permission checks occur in the message flow
