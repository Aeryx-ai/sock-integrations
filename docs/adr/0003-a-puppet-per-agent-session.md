# 3. A puppet per agent session

Status: proposed (2026-10-15). Follows [sock-contract ADR 27](https://github.com/aeryx-ai/sock-contract/blob/main/docs/adr/0027-a-puppet-per-agent-session-archived-when-it-ends.md). Supersedes this repo's use of project puppets (contract ADR 10) and, in [ADR 2](0002-the-plugin-server-listens-for-codex.md), the session ask and the per-project subscription. Design: [session-puppets.md](../specs/session-puppets.md).

## Context

Project puppets made every session in one repository share a delivery queue: a Claude Code session and a Codex session in one repository competed for the owner's replies (integrations-fdh), and Codex inbound needed session asks and project matching to guess which session a reply was for. The owner's ruling: start any number of agents anywhere; each is its own contact.

## Decision

- Each agent session (a Claude Code session, a Codex thread) gets its own puppet, made the first time the session sends, not when it starts: most sessions never message the owner.
- The puppet carries the session's name and follows its renames.
- Archived means the session is not running. Integrations archives and unarchives with the session; it never erases a session puppet, since erasing loses the conversation and refuses a reply to a session the owner resumes.
- A reply to a session puppet goes to that session only. Session asks and project matching are dropped: they guessed, and two platforms in one repository still competed.

## Consequences

- The puppet is the route: no session asks, no project matching, no per-project subscription.
- Each session the owner hears from is a contact, archived while it is not running.
- Existing project puppets are no longer used or swept; the owner erases them from the app.
- A session that stops without the stop being seen stays unarchived until the next reconciliation.
