# 2. The plugin's server listens for Codex

Status: accepted (2026-10-15). Supersedes the delivery half of [ADR 1](0001-codex-replies-enter-through-the-app-server.md): replies still enter through the app-server, but the plugin's own `sock-mcp` receives them, not a `sock listen --exec` service.

## Context

ADR 1 put the listener in a per-project service (`runner/sock-codex` under `sock-runner-service`). Installing the plugin then did not make replies work; the owner had to run an installer per project. The owner's requirement: installing the plugin is the whole setup.

The Codex daemon starts the plugin's `sock-mcp` once per loaded session and keeps it while the session is loaded. Codex gives a server no session id or directory at startup: `sandboxCwd` arrives only on tool calls. The daemon does list every loaded session with its `cwd`.

## Decision

- The Codex plugin's `sock-mcp` (flag `--codex-inbound`) listens. Of all such servers on the machine, the one holding an exclusive lock on `${XDG_STATE_HOME}/sock-integrations/codex-inbound.lock` is the listener; the rest wait on the lock and take over when the holder's session ends.
- The listener names puppets from what Codex reports: it lists the daemon's loaded interactive sessions, takes each one's project (sock-client `Project` of its `cwd`) and resolves that project's puppet with the machine key from the config file, as the tool calls do. It subscribes once per project puppet, with the `channel` package's stream, reconnect, owner allowlist and push bound.
- Each owner reply is injected as ADR 1 says (the session that asked, else the newest loaded session of the project, duplicate check by `clientUserMessageId`) and acknowledged once `turn/start` is accepted.
- No session loaded, no listener: replies wait at the hub.

## Alternatives

- A service per project (ADR 1): a setup step outside the plugin.
- Every server listens: N subscribers on one puppet race for each reply, and a server cannot tell which session is its own.
- A `SessionStart` hook that installs a service: writes a LaunchAgent the owner did not ask for.

## Consequences

- `runner/sock-codex`, the `codex-live` service option and the separate `sock-codex-inject` binary are removed; the app-server client moves into `sock-mcp` as a package.
- Sessions in the ChatGPT desktop app run under the app's own app-server, reachable only over the app's stdio, and receive nothing.
- A Claude Code session with the sock channel on in the same repository still competes for the project puppet's deliveries (integrations-fdh).
