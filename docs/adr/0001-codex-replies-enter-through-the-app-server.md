# 1. Codex replies enter through the app-server

Status: proposed (2026-10-15)

## Context

The Codex plugin is outbound only. When the owner answers in sock, nothing reaches the Codex session. `sock-runner` runs `codex exec resume` per message, which needs a session id fixed in configuration and corrupts a session someone has open, because two writers interleave one transcript.

A live probe (Codex CLI 0.162.1, daemon 0.163.0) found that the TUI is a client of a per-user app-server daemon on `~/.codex/app-server-control/app-server-control.sock` (JSON-RPC over WebSocket). A second client can list the loaded threads with their `cwd`, and `turn/start` on one of them:
- shows the input in an open TUI as a user turn, which Codex answers;
- adds the input to a turn already running, which Codex answers before finishing its work;
- runs a full turn after the TUI has quit, because the thread stays loaded in the daemon.

## Decision

A new `sock listen --exec` command, `runner/sock-codex`, injects each owner reply into a Codex session through the daemon. It reuses `runner/lib.sh` for the owner gate, input hygiene, the tagged prompt and the `done.ids` ledger, and a small Go helper is the anti-corruption layer for the app-server protocol. The reply goes to the session that last posted to the conversation (recorded locally by sock-mcp and the hooks), else the newest interactive session in the project. The message is acknowledged once `turn/start` is accepted, and `clientUserMessageId` carries the sock message id so a redelivery is recognized from the session's history.

## Alternatives

- `codex exec resume` (sock-runner): two writers on an open session, and one fixed session per runner.
- A `Stop` hook that fetches: never reaches an idle session (already rejected in the integration spec).
- Wait for the turn to finish and check that a reply was posted, as sock-runner does: holds the owner's message while the owner may be the one at the keyboard, and the model has no duty to answer through sock when the owner is present.
- A daemon of this repo's: `sock listen --exec` already owns reconnect, backoff and at-least-once delivery.

## Consequences

- One writer per session: the daemon, open TUI or not.
- Depends on an experimental, self-updating protocol. A change shows as a refused injection and the reply stays pending, not lost.
- A Go helper joins a repo that so far ships only shell, because no shell tool here speaks WebSocket over a Unix socket.
- The owner's text becomes user input to a Codex session that can run tools. It arrives inside the `<sock-message>` tag as data, gated to the owner, and the session's own sandbox and approval policy apply.
