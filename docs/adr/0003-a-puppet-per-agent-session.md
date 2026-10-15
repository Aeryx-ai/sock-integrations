# 3. A puppet per agent session

Status: proposed (2026-10-15). Follows [sock-contract ADR 27](https://github.com/aeryx-ai/sock-contract/blob/main/docs/adr/0027-a-puppet-per-agent-session-archived-when-it-ends.md). Supersedes this repo's use of project puppets (contract ADR 10) and, in [ADR 2](0002-the-plugin-server-listens-for-codex.md), the session ask and the per-project subscription.

## Context

Project puppets made every session in one repository share a delivery queue: a Claude Code session and a Codex session in one repository competed for the owner's replies (integrations-fdh), and Codex inbound needed session asks and project matching to guess which session a reply was for. The owner's ruling: start any number of agents anywhere; each is its own contact.

## Decision

- A session puppet per agent session: a Claude Code session (`CLAUDE_CODE_SESSION_ID`) or a Codex thread. It is minted with the machine key the first time the session sends, named `<session name> on <host>`, and renamed with the session through its own token (`UpdateDisplayName`).
- Archived means the session is not running. Integrations archives it when the session stops and unarchives it when the session runs again, with the creating machine key. It never erases one.
- A reply to a session puppet goes to that session only. Claude Code: the session's own `sock-mcp --channel` subscribes as its puppet. Codex: the listener subscribes as each loaded thread's puppet and injects into that thread.
- The ensure-or-read routine for session puppets lives once, in sock-client, as the project routine did; the hooks reach it through `sock send --as-puppet --session`.

## Alternatives

- Keep project puppets and route by session asks: guesses, and two platforms in one repository still compete.
- Mint at session start: a contact for every session, most of which never message the owner.
- Erase on session end: loses the conversation and refuses a reply to a session the owner resumes.

## Consequences

- No session asks, no project matching, no per-project subscription: the puppet is the route.
- Each session the owner hears from is a contact, archived while it is not running.
- Existing project puppets are no longer used or swept; the owner erases them from the app.
- A session that stops without the stop being seen stays unarchived until the next reconciliation (spec, "Archiving").
