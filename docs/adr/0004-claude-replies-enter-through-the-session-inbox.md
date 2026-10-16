# 4. Claude Code replies enter through the session inbox

Status: accepted (2026-10-16). Design: [session puppets spec](../specs/session-puppets.md), Claude Code section.

## Context

The Claude Code plugin delivered the owner's replies as a channel (`sock-mcp --channel`, `notifications/claude/channel`). Claude Code 2.1.301 registers a channel only for plugins on Anthropic's approved list or an org's `allowedChannelPlugins`; any other plugin needs `--dangerously-load-development-channels` and a dialog accepted on every launch. A plain `claude` session logs `not in --channels list for this session` and drops every push, so in practice no session heard the owner.

Claude Code hands each MCP server its session's inbox: a Unix socket (`CLAUDE_CODE_MESSAGING_SOCKET`) and an auth token (`CLAUDE_CODE_MESSAGING_TOKEN`). A connection that writes `{"type":"auth","token":...}` and then `{"type":"user","message":{"role":"user","content":...}}` queues that content in the session. Claude Code's own debug log documents this line format. A probe on 2026-10-16 reached a plain session, shown as a message from another Claude session.

## Decision

- The plugin's server runs `sock-mcp --outbound-only --claude-inbound`. With a machine key it is the session's own puppet (ADR 3); it subscribes once that puppet exists, made by its first tool call or by a hook's send, and writes each owner reply into the inbox as a `<sock-message from="owner">` prompt, the same one Codex inbound uses.
- A reply is acknowledged once the inbox took it. A redelivery after a failed ack arrives as a repeat with the same id.
- One server per session listens, under a run lock beside the puppet cache. Its exit archives the puppet; a starting server archives the puppets of sessions whose server died.
- The plugin declares no channel. `sock-mcp --channel` stays for anyone who launches with the flag outside the plugin.

## Alternatives

- Keep the channel and wrap `claude` to pass the development flag: a dialog on every launch, and nothing for a session started any other way.
- A `Stop` hook that blocks with the owner's reply: only reaches a session that is ending a turn, never an idle one.
- The headless runner only: answers from a separate session, not the one the owner is talking to.

## Consequences

- Inbound works in every interactive Claude Code session with the plugin, with no flag.
- The inbox is not a documented interface. A Claude Code release can change or remove it; the owner's replies then wait at the hub, and the tools keep working.
- Claude Code frames the reply as a peer session's message, so the model treats it as a teammate's request, not the user's own input. The skill names it as the owner's reply.
- Sessions no longer share the project puppet, so integrations-bof and integrations-fdh do not arise with a machine key. A puppet token still makes one puppet for every session.
