# Plan: Discord connector (the user's own bot)

Status: design settled (2026-11-05), D1 in progress. Tracking issue: #917 (#2828 before the 2026-11-08 repository rebuild); #1106, #1426, #1730 and #2057 below refer to the history before that rebuild.
Discord is a **connector** (a data source: knowledge base, Inbox, assistant tools), not a channel (an entry point for talking to Persona, `docs/extensions/channels.md`);
making the same bot a channel too is a separate issue. The first version is **read-only**. The framework is in `docs/extensions/connectors.md` (capability list, Inbox, knowledge base, gates) and `docs/protocol/connector-protocol.md` (protocol v2);
extension manifest, permissions and sandbox are in `docs/extensions/extensions.md`. This doc only covers what is specific to Discord. The code lives in the public extension repository `nwverse/persona-extensions` (`extensions/discord/`), like Telegram (#2057).
When done, the design is merged into `docs/extensions/connectors.md` and this file moves to `docs/archive/`.

## What we can see: Discord only allows your own bot

Of the ways to read Discord messages, only the last two are allowed by the terms and usable by ordinary people:

| Route | What it sees | Usable? |
| --- | --- | --- |
| User token / self-bot (using your own account, like Telegram) | Everything you can see, including DMs | **No**: violates the Terms of Service and Community Guidelines (automated user accounts); gets the account banned |
| OAuth message reading (`messages.read`, `dm_channels.read`) | The user's DMs and group DMs | **No**: only for partners Discord approves |
| Your own bot (bot token) | Channels it has permission for in servers it was added to; DMs others send the bot | Yes: adding a bot to a server needs the "Manage Server" permission, so it's your own server or one whose admin agreed |
| A user-installed app (User Install) message command "Send to Persona" | When the user right-clicks a message **anywhere** (DMs, group DMs, servers the bot isn't in) and picks it, the full text of that one message | Yes: the command interaction carries the full message (`data.resolved.messages`), no Message Content intent needed; interactions come over the Gateway, no public address needed |
| Data package (Settings › Data & Privacy › Request Data) | Only messages you sent; can take up to 30 days | Yes, but it's only half of each conversation; later |

So Persona's Discord = **server channels your own bot can see** + **messages you click "Send to Persona" on**. DMs between someone else and the user are invisible unless the user clicks; that's Discord's boundary and we don't work around it.

## Shape: the `discord` extension

- `extensions/discord/` in the extension repository, written in Go, using `disgoorg/disgo` as the Discord client (supports API v10, user-installed apps, and message search; not 1.0 yet, so the version is pinned in `go.mod`).
- A process extension on the hub (`runtime`) contributing one connector, called by the hub over the plugin protocol (`server/internal/connector/proc`), like Telegram.
  A Go program is still how a new extension ships (the extension repository's development guide, "Add a program extension"). A TypeScript source profile for the isolated runtime
  (`docs/archive/extensions/source-extension-migration-2026-11-07.md`) waits for controlled outbound WebSocket support (#1166), because the Gateway is a WebSocket; polling REST instead of the Gateway is not a substitute.
- Uses only HTTPS (REST, CDN) and WSS (Gateway), so it **can run in the sandbox** (Telegram's MTProto is TCP and can't): `permissions.network` lists Discord's domains, and outbound traffic goes through the hub's egress proxy.
- Doesn't run on a node: programs on a node can't call hub methods (`docs/extensions/extensions.md` ("Programs that run on a computer")), so they can't be connectors.

What the manifest looks like:

```json
"connectors": [{
  "id": "discord", "name": "Discord",
  "capabilities": ["status", "tools", "events", "inbox", "archive", "login", "threads", "fallback"],
  "kinds": ["mention", "saved"],
  "kind_descriptions": {
    "mention": "Messages in a Discord server that @ the user or reply to the user",
    "saved": "A message the user sent to Persona from Discord (\"Send to Persona\")"
  }
}],
"configuration": {"triage_rules": "… (required, #1730)", "history_days": "How many days to backfill the first time, default 30"},
"permissions": {"network": ["discord.com", "*.discord.com", "*.discord.gg", "cdn.discordapp.com", "media.discordapp.net"]}
```

`events` means: when a new message arrives on the Gateway, call the hub's `changed`; the hub then fetches `archive.changes` and `inbox.collect` (`docs/protocol/connector-protocol.md`).

## Login and credentials

The user creates their own Discord application; both the bot and the app belong to the user (the developer on Discord's side is the user):

1. Create an application in the Discord Developer Portal: on the Bot page turn on the **Message Content intent** and turn off **Public Bot** (only you can invite it); on the Installation page check **User Install**.
   A personal bot is tiny, so the Message Content intent needs no review (only large apps do).
2. Enter the bot token under **Settings › Connectors › Discord**: `login.step` has a single step, a field to fill in (the first of the three step types in `docs/extensions/connectors.md` ("Login supports 'open a web page to authorize'")), with instructions for the Developer Portal.
3. The extension validates:
   - Call `GET /users/@me` with the token; 401 means the token is wrong.
   - Connect to the Gateway: close code 4014 means the intent is off; the status `guide` says "In the Developer Portal › Bot, turn on the Message Content intent".
   - Use `GET /oauth2/applications/@me` to get the app's owner (`owner`; for a team-owned app, the team's `owner_user_id`). The owner is "me", used to tell which messages I sent and who @'d me.
4. The status `guide` gives two links:
   - Invite the bot to a server: `scope=bot applications.commands`, permissions only "View Channels" + "Read Message History".
   - Install the app to my account (`integration_type=1`): after that, any message's context menu › Apps › Send to Persona.
5. The token is encrypted via `connector.secrets.*` into the hub's database (`connector_secrets`); the App only shows the bot's name; it's deleted on sign-out. Tokens never appear in logs.

Gateway intents needed: `GUILDS`, `GUILD_MESSAGES`, `DIRECT_MESSAGES`, `MESSAGE_CONTENT`, `GUILD_MESSAGE_REACTIONS` (for deciding "done", see "Inbox"). Not `GUILD_MEMBERS` or `GUILD_PRESENCES`.

## Knowledge base: what to read

- **Conversations**: one conversation per channel / thread / forum post, `discord-<channel id>`, named "Server #channel" or "Server #channel › thread", `kind: group`.
  The bot's DMs with "me" are not collected (they only contain tests), nor are DMs others send the bot.
- **Which channels**: every text channel, announcement channel, thread and forum post the bot can see. To exclude a channel, don't give the bot "View Channels" on it in Discord. Persona has no separate channel picker.
- **First time**: backfill `history_days` days (default 30) per channel, at most 500 messages per channel; `GET /channels/{id}/messages?before=` returns 100 per page.
  Threads are backfilled only if active in that window (ongoing + recently archived).
- **After that**: real-time over the Gateway, with `events` nudging the hub to fetch. After an extension restart or disconnect, it catches up with `after=` from each channel's last message id in the cursor.
  The cursor (`archive.changes`' `cursor`, stored by the hub) is a table of "channel → last message id".
- **Messages**: sender display name, body (mentions written as `@name`, `#channel`, custom emoji as `:name:`), the quoted reply, embed titles and links; messages "I" sent are marked `mine`.
  Edits and deletions are not synced in the first version (item 3 of "Decided by the user").
- **No people records** (item 2 of "Decided by the user"; Discord's developer policy forbids profiling users): messages carry only the sender's name, **no `sender_id` and no `sender_handles`**.
  The knowledge base only creates people and records identity hints when a message has `sender_id` (`notePerson` in `server/internal/kb/archive.go`), and person notes during compaction only cover people already in the conversation
  (`notePersonNote` in `server/internal/kb/compress.go`), so the hub needs no change; the extension's tests guard that no message carries either field.
- **Attachments**: Discord attachment links are signed and expire, so they're fetched at ingest (`archive.media`, size limit per `ArchiveMediaMost`); if expired, refetch the message to get a fresh link.
- **"Send to Persona" messages** go into the knowledge base too: into the conversation of their channel, `discord-<channel id>` (also for DMs and channels the bot can't see; such conversations contain only the messages the user clicked),
  named from the channel info in the interaction. Until the hub fetches them, they're kept in the extension's own data directory (`PERSONA_EXTENSION_DATA`), so an extension restart loses nothing.

## Inbox: what counts as "needs me"

| kind | What | Tier |
| --- | --- | --- |
| `mention` | In a server: @ me, a reply to me, @ a role I'm in | Action (action) |
| `mention` | @everyone / @here | FYI (fyi) |
| `saved` | A message I clicked "Send to Persona" on | Action (action) |

- **Done**: I later posted in the same channel / thread, reacted to the message, or handled it in Persona. Discord exposes no read state to bots, so read state isn't used.
- Anything older than 8 days no longer enters the Inbox (like Telegram). Messages sent by bots don't enter the Inbox.
- `triage_rules` is required configuration (#1730); with the `fallback` capability, every item carries `extra.rule_tier`. `thread` is the channel (thread) id; several items from the same channel merge into one row.
- When "Send to Persona" is clicked, the extension replies within 3 seconds with a message only I can see (ephemeral): "Sent to Persona".

## Assistant read-only tools

| Tool | What it does |
| --- | --- |
| `discord_channels` | Servers and channels the bot can see, most recently active first |
| `discord_messages` | Messages in a channel / thread, paging back in time |
| `discord_search` | Search messages in a server (`GET /guilds/{id}/messages/search`); while Discord is still indexing (returns 202), falls back to knowledge-base search and says so |

Tool descriptions and parameter docs live in the extension's own `internal/prompts/mcp/` (the rules in `docs/assistant/prompts.md`). Message content is treated as untrusted data (prompt injection); tool results carry no tokens and no user ids.

## Conversation page

`threads`: Discord conversations in the Inbox open as a conversation page in the App (`thread.page`, one page fetched live over REST), drawn as group-chat bubbles (`kind: group`), with avatars and images via `thread.media`.
The first version has no input box, only "Reply in Discord", which opens `https://discord.com/channels/<server id>/<channel id>/<message id>` (for DMs the server id is `@me`).

## Network

Some networks can't reach Discord directly (if the hub is on one, both REST and the Gateway fail):

- **Not sandboxed** (extensions from a trusted market run without the sandbox by default): the extension's secrets can include `proxy` (`http://`, `https://`, `socks5://`, optionally with username and password, which is why it's a secret rather than config); REST, Gateway and CDN all go through it.
- **Sandboxed**: the extension can only go out through the hub's egress proxy, which in turn uses the hub process's own `HTTPS_PROXY`. Per-extension upstreams are a later hub change (#1106); until then the whole hub has to go through the proxy.
- It doesn't run on a node (see "Shape"), so you can't "pick a computer that can reach Discord" to run it. For that, run a proxy on that computer and send the hub through it.

## Discord's rules

The parts of Discord's Developer Policy and Developer Terms that concern us:

- **No user profiling** (Policy §16: don't use API data to profile Discord users, their identities, or their relationships): so no people are created from Discord senders and no identity hints are recorded (the "Knowledge base" section).
- **No training models on message content** (Policy §21): Persona only gives messages to models for summarizing, triage and answering (like every other source), never for training.
- **Delete on request**: the user can "Delete stored knowledge base content" under **Settings › Connectors › Discord**; signing out stops collection and deletes the token.
- **Encryption at rest**: the token is encrypted in the database; the knowledge base is files on the hub machine (same for all sources) and relies on the hub machine's disk encryption, as stated in `docs/extensions/connectors.md`.
- **Rate limits**: 50 requests per second globally, per-route buckets from response headers (handled by disgo). More than 10,000 invalid requests (401, 403, 429) in 10 minutes gets the IP temporarily banned,
  so on 401 it stops and waits for the user to sign in again, and channels that return 403 are remembered and not fetched again.

## Testing

- Fake Discord: `httptest` serves REST and the Gateway (WebSocket) with synthetic servers, channels, messages and interactions; no real account needed.
- The extension SDK's conformance tests (`exttest`): calling over the protocol gives the same results as calling directly; `archive.changes` cursors resume correctly, and catching up after a disconnect has no duplicates or gaps.
- Guarded: messages have no `sender_id` / `sender_handles`; `@everyone` is FYI; items I later posted after or reacted to count as done; tokens never appear in logs, status or tool results;
  in the sandbox, outbound traffic only goes to domains in the manifest.
- The real-account path (create the app, invite, Gateway, "Send to Persona" getting the full text in a DM) is checked by hand when work starts, and the PR states truthfully what was checked.

## Steps

One PR per step; PRs in the extension repository write `Refs nwverse/persona#917`:

| Step | What |
| --- | --- |
| D1 | Login, status, knowledge base (first backfill, real-time, catch-up after disconnect), read-only tools, `proxy`, tests |
| D2 | Inbox (`mention`, deciding "done", `triage_rules`) |
| D3 | "Send to Persona" message command (`saved`): first verify with a real account that the interaction in a DM carries the full message text |
| D4 | Conversation page, attachments |

This repository: this plan (the first PR); one row in the table in `docs/extensions/connectors.md` after D1 merges; the Discord brand mark (all three platforms, or wait for #1426, data-driven brands).

## Not doing / later

- **Not doing**: self-bot / user token, OAuth message reading ("What we can see").
- **Later**: sending messages as the bot (as an action, through the gate with `confirm_always`, at most 2000 characters per message); data package import; the same bot doubling as a channel (DMing the bot is talking to the main chat, like the Lark bot);
  syncing edits and deletions. Each gets its own issue.

## Decided by the user (2026-11-05)

1. Scope: only "your own bot + Send to Persona", no self-bot.
2. People in the knowledge base: archive chats only, don't create people from Discord senders (Developer Policy §16). The extension just doesn't send `sender_id` / `sender_handles`; no toggle is added to the knowledge base.
3. Edits and deletions: not synced in the first version; rely on the existing "Delete stored knowledge base content"; syncing deletions is for later.
4. The first version is read-only; sending as the bot is decided later.
5. Where: the plan and tracking issue live in this repository (#917), the code in `nwverse/persona-extensions`.
