# 6. One config file carries the machine's sock settings

Status: accepted (2026-11-10). Design: [plugin sign-in spec](../specs/plugin-sign-in.md). Bead integrations-9jk.

## Context

Zero-config plugins (integrations-hle) read every credential from the sock CLI config file. Two settings still lived in the Claude Code plugin's options: `sock_url`, which sock-mcp ignores whenever `sock_token` is empty (on the owner's machine it named a different server from the file and nothing noticed), and `message_owner`, which only the plugin UI could change and which the Codex plugin hard-coded on. Recovering a dead machine key took `sock machine-key create | sock machine-key use --force` and a plugin reload, and `sock login` refused to run at all while a dead session token sat in the file.

## Decision

- The config file is the one place for the machine's sock settings: `url` and the new `message_owner` join the credentials. The plugins carry only the `sock_token` override.
- `sock login` repairs instead of refusing: a valid session and key are kept, a rejected session is replaced by a fresh sign-in, a missing or rejected machine key is minted again. Running it twice is harmless.
- `sock logout` stays session only. The machine key, and the puppets it makes, keep working; the output says so and names the switch that stops owner messaging.
- A new `sock settings` reports sign-in and key health and changes `message_owner` and `url`. `url` changes only while the file holds no credential.
- `/sock:login`, `/sock:logout` and `/sock:settings` in the Claude Code plugin run those CLI commands and hold no logic of their own.
- sock-mcp re-reads the file for owner messaging on each send, and once after a machine key is rejected.

## Alternatives

- Keep `message_owner` a plugin option: `/sock:settings` could only point at `/plugin`, and Codex keeps a second copy.
- Logout also revokes the machine key: stops every session here messaging the owner, at the cost of a sign-in to get them back. The owner chose session only.
- Keep `sock_url` for `sock_token` users: one more option that is dead in the common case.

## Consequences

- A plugin user who set `message_owner` off loses that on upgrade until they run `/sock:settings message-owner off`. The default stays on.
- A `sock_token` user on a non-default server sets `SOCK_URL` in their shell.
- sock-client, sock-cli, sock-mcp and both plugins change, released in that order.
