# Platform Support

Loop supports three chat platforms: **Discord**, **Slack**, and **Local** (Desktop/Electron). Each platform implements the `orchestrator.Bot` interface, which provides a uniform set of operations for messaging, threading, typing indicators, and lifecycle management. The `BotRouter` layer sits above the platform bots and dispatches calls to the correct implementation based on the channel's platform stored in the database.

## Platform Overview

| Feature | Discord | Slack | Local |
|---|---|---|---|
| Max message length | 2000 chars | 4000 chars | Unlimited (no external API) |
| Typing indicator | `ChannelTyping` API, refreshed every 8s | Emoji reaction (eyes) on trigger message, removed on completion | No-op |
| Thread model | Guild public threads (`ThreadStart`) | Composite IDs (`channelID:threadTS`) via reply threading | DB-backed threads with random hex IDs |
| Stop button | Interactive button component (`DangerButton`) | Block Kit action button (`StyleDanger`) | No-op (UI handles stop via API) |
| Slash commands | Application commands registered via Discord API | Slash command via Socket Mode (`/loop`) | HTTP API `POST /api/commands` |
| Bot mention detection | `m.Mentions` slice + `<@BOT_ID>` content check | `<@BOT_ID>` in message text | `@LoopBot` text mention |
| Reply-to-bot detection | `MessageReference` author check + thread owner check | Thread parent message author/botID check | Always treated as DM (all messages trigger) |
| Prefix detection | `!loop` (case-insensitive) | `!loop` (case-insensitive) | `!loop` (case-insensitive) |
| DM detection | `GuildID == ""` | `ChannelType == "im"` | Always `IsDM: true` |
| Role-based permissions | Discord role IDs via `GuildMember` | Not supported (user-only permissions) | Auto-granted Owner role |
| Channel join events | Not dispatched (bot sees all guild channels) | `MemberJoinedChannelEvent` | Not applicable |

## Discord

### Setup Requirements

Discord requires the following credentials in the config file (`~/.loop/config.json`):

- `discord_token` -- Bot token from the Discord Developer Portal
- `discord_app_id` -- Application ID from the Developer Portal
- `discord_guild_id` -- (Optional) Guild ID for guild-specific operations

### Bot Permissions

The bot needs the following OAuth2 scopes:

- `bot` -- Required for message handling
- `applications.commands` -- Required for slash commands

The required permission integer is **395137059856**, which includes:

- View Channels
- Send Messages
- Manage Channels
- Manage Threads (separate from Manage Channels, needed for thread deletion via API)
- Read Message History
- Send Messages in Threads / Create Public Threads

### Invite URL Format

```
https://discord.com/oauth2/authorize?client_id=APP_ID&scope=bot%20applications.commands&permissions=395137059856
```

Replace `APP_ID` with the application ID from the Developer Portal.

### Intents

The bot uses `discordgo.New()` which defaults to `IntentsAllWithoutPrivileged`. The `IntentMessageContent` privileged intent must be added using `|=` (bitwise OR) -- do not replace all intents, or the bot will lose default intents.

### Message Handling

When a Discord message arrives, the bot checks four trigger conditions:

1. **Bot mention** -- The bot's user ID appears in the message's `Mentions` slice, or `<@BOT_ID>` appears in the raw content (fallback for self-mentions via `PostMessage` where Discord does not populate `Mentions`).
2. **Command prefix** -- The message starts with `!loop` (case-insensitive).
3. **Reply to bot** -- The message is an explicit reply to a bot message (`MessageReference` + `ReferencedMessage.Author.ID`), or the message is in a thread owned by the bot.
4. **DM** -- The message has an empty `GuildID`.

If none of these conditions are met, the message is silently ignored (returns `nil`).

Bot-authored messages are also ignored unless they contain an explicit `<@BOT_ID>` self-mention in the content. This prevents infinite recursion: when the bot replies to its own message, Discord auto-populates `m.Mentions` with the bot, which would otherwise re-trigger the handler.

The mention or prefix is stripped from the content before forwarding to the orchestrator. When a mention is detected, `StripMention` removes both `<@BOT_ID>` and `<@!BOT_ID>` forms. When a prefix is detected, `StripPrefix` removes the `!loop` prefix.

### Threading

Discord threads are first-class channels with their own IDs. The bot joins every new thread automatically via `handleThreadCreate`. Thread channel IDs are used directly as `ChannelID` in messages.

When a thread is deleted, the bot receives a `ThreadDelete` event and dispatches it to `channelDeleteHandlers` with `isThread: true`.

### Slash Command Interactions

Discord slash commands must be acknowledged within 3 seconds. The bot immediately sends a `DeferredChannelMessageWithSource` response via `InteractionRespond`, then stores the interaction in `pendingInteractions` keyed by channel ID. When the orchestrator sends the command response via `SendMessage`, the bot detects the pending interaction and resolves it using `InteractionResponseEdit` (for the first chunk) and `FollowupMessageCreate` (for subsequent chunks), rather than sending a regular message.

Subcommand parsing: for subcommands, `Options[0]` is the subcommand itself and its `Options` hold the parameters. For subcommand groups (e.g., `/loop template add`), `Options[0]` is the group and `Options[0].Options[0]` is the subcommand. The command name is constructed by joining group and subcommand names with a hyphen (e.g., `template-add`).

### Stop Button

The stop button is sent as a message with a `DangerButton` component whose `CustomID` is `stop:<channelID>`. When clicked, Discord sends a `InteractionMessageComponent` event. The bot acknowledges it with `DeferredMessageUpdate` (no visible response) and dispatches a `stop` interaction to the orchestrator with the target channel ID.

## Slack

### Setup Requirements

Slack requires:

- `slack_bot_token` -- Bot token (starts with `xoxb-`)
- `slack_app_token` -- App-level token for Socket Mode (starts with `xapp-`)

Commands are registered via the Slack app manifest, not programmatically. `RegisterCommands` and `RemoveCommands` are no-ops.

### Connection

Slack uses **Socket Mode** for real-time events. The bot authenticates with `AuthTest`, sets presence to `auto`, then starts an event loop that processes events from the Socket Mode client's `Events` channel.

### Message Handling

The Slack bot processes three event types:

- `EventTypeEventsAPI` -- Contains `MessageEvent`, `MemberJoinedChannelEvent`, `ChannelDeletedEvent`, and `GroupDeletedEvent`.
- `EventTypeSlashCommand` -- Slash commands from `/loop`.
- `EventTypeInteractive` -- Block Kit interactions (stop button clicks).

Message subtypes (edits, deletions, etc.) are ignored. The same four trigger conditions apply (mention, prefix, reply-to-bot, DM).

### Thread Model

Slack threads are represented as **composite IDs** in the format `channelID:threadTimestamp`. The `parseCompositeID` function splits this into channel and thread TS, and `compositeID` constructs it.

When a message arrives in a thread (`ThreadTimeStamp != "" && ThreadTimeStamp != TimeStamp`), the channel ID becomes the composite `channelID:threadTS`. For self-mention messages that create threads, the message's own timestamp is used as the thread TS.

`CreateThread` posts an initial message to the channel (which becomes the thread parent), then returns the composite ID. `CreateSimpleThread` similarly posts the thread "title" as the parent message and the initial content as the first reply.

`DeleteThread` fetches all replies via `GetConversationReplies` and deletes each message individually. `RenameThread` updates the parent message text via `UpdateMessage`.

### Typing Indicator

Instead of a typing API, Slack uses an emoji reaction approach. The bot adds an "eyes" emoji reaction to the last received message in the channel when processing starts, and removes it when the context is cancelled (processing complete). The `lastMessageRef` sync.Map tracks the most recent message per channel for this purpose.

### Slash Commands

Slack slash commands arrive as plain text from `/loop <text>`. The `parseSlashCommand` function splits the text into subcommand and arguments. The format varies by command:

- `schedule <schedule_expr> <type> <prompt>` -- Type keyword (cron/interval/once) acts as a delimiter between the schedule expression and the prompt.
- `edit <task_id> [--schedule X] [--type Y] [--prompt Z]` -- Uses flag-style arguments; `--prompt` consumes all remaining text.
- `allow user <@U...> [owner|member]` -- Extracts user ID from Slack mention format.
- `deny role` and `allow role` are explicitly unsupported on Slack with a descriptive error.

Invalid commands return help text via a regular `PostMessage` (not an ephemeral reply).

### Stop Button

The stop button uses Slack Block Kit. An action block with a danger-styled button is posted to the channel. The button's `ActionID` is `stop:<runID>`. When clicked, the `InteractionTypeBlockActions` callback fires and the bot dispatches a `stop` interaction.

## Local (Desktop/Electron)

### Overview

The local platform is designed for the Electron desktop app. It requires no external credentials -- the user is inherently trusted, running on their own machine.

### Bot Implementation

`local.Bot` implements `orchestrator.Bot` with:

- **DB-backed channel/thread operations** -- `CreateThread`, `CreateSimpleThread`, `DeleteThread`, `RenameThread`, `GetChannelParentID`, and `GetChannelName` all operate on the database directly.
- **No-op messaging** -- `SendMessage`, `SendTyping`, `PostMessage`, `SendStopButton`, and `RemoveStopButton` are all no-ops. The orchestrator handles DB persistence and EventsHub broadcasting, which the Electron app consumes via WebSocket SSE.
- **No-op lifecycle** -- `Start`, `Stop`, `RegisterCommands`, `RemoveCommands` are no-ops or log-only.

### IDs

Thread and channel IDs on the local platform are random 12-character hex strings generated by `randutil.HexID(6)`. Channel IDs created via `CreateChannel` use two concatenated hex IDs (24 characters).

### Message Handling

`HandleIncomingMessage` implements `api.IncomingMessageHandler`. When the Electron app sends a message via `POST /api/channels/{id}/messages`:

1. If no `authorID` is provided, it defaults to `"local-user"`.
2. The content is checked for `@LoopBot` text mention and `!loop` command prefix.
3. Mentions and prefixes are stripped from the content.
4. A `bot.IncomingMessage` is constructed with `Platform: PlatformLocal`, `IsDM: true` (all local messages are treated as direct), and dispatched to the registered message handler.

`HandleThreadCreated` is called when `POST /api/threads` creates a new thread. It prepends `@LoopBot` to the message to trigger the agent, then delegates to `HandleIncomingMessage`.

### Permission Bypass

The local platform automatically grants `RoleOwner` to all users. In `HandleInteraction`, if the resolved role is empty and the platform is `PlatformLocal`, the role is upgraded to `RoleOwner`. In `HandleMessage`, the permission check is skipped entirely when `msg.Platform == types.PlatformLocal`.

## BotRouter

The `BotRouter` sits between the orchestrator and the platform-specific bots. It holds a `map[types.Platform]Bot` of registered bots and a `ChannelStore` for looking up which platform a channel belongs to.

### Routing Behavior

- **Lifecycle operations** (`Start`, `Stop`, `RegisterCommands`, `RemoveCommands`) fan out to **all** registered bots.
- **Handler registration** (`OnMessage`, `OnInteraction`, `OnChannelDelete`, `OnChannelJoin`) registers on **all** bots, so the orchestrator receives events from every platform.
- **Channel-specific operations** (`SendMessage`, `SendTyping`, `SendStopButton`, `RemoveStopButton`, `SetChannelTopic`, `DeleteThread`, `RenameThread`, `PostMessage`, `GetChannelParentID`, `GetChannelName`, `CreateThread`, `CreateSimpleThread`, `InviteUserToChannel`) look up the channel in the database via `GetChannel`, determine its platform, and dispatch to the corresponding bot. If no bot is found, a warning is logged and an error is returned.
- **API message routing** (`HandleIncomingMessage`, `HandleThreadCreated`) similarly resolve the channel's platform bot and delegate to it.
- **Cross-bot operations** -- `IsBotUser` checks against **all** registered bots, returning true if any bot matches the user ID.

### Configuration

The `platforms` array in `config.json` determines which bots are instantiated:

```json
{
  "platforms": ["discord", "slack", "local"]
}
```

At least one platform must be configured. Each platform requires its specific credentials (Discord needs tokens, Slack needs bot + app tokens, Local needs nothing).

## Message Splitting

All platforms use `bot.SplitMessage` to split outgoing messages that exceed the platform's maximum length. The function attempts to break at newlines first, then spaces, and falls back to a hard cut at the limit. This ensures that long agent responses are delivered as multiple messages without breaking mid-word when possible.

## Cross-Platform Behavior

| Behavior | Description |
|---|---|
| Message storage | All platforms store messages in the same SQLite database via `store.InsertMessage` |
| Event broadcasting | The orchestrator broadcasts events (message created, agent status, tool use) via `EventsHub`, consumed by the Electron app's WebSocket connection |
| Session continuity | All platforms share the same `SessionID` mechanism for Claude Code session resume/fork |
| Thread inheritance | Threads on all platforms inherit `DirPath`, `SessionID`, `GuildID`, and `Permissions` from their parent channel |
| MCP config | Per-channel MCP config files (`mcp-{channelID}.json`) are used across all platforms to avoid race conditions between parent/thread sharing the same workDir |

## Related Documentation

- [Orchestrator & Message Processing](orchestrator.md) -- Full message flow from receive to deliver
- [Slash Commands & Interactions](commands.md) -- Command definitions and parsing per platform
- [Permission & RBAC System](permissions.md) -- How roles are resolved across platforms
