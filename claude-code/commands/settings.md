---
description: Show sock sign-in status and settings, or change message-owner or the server URL
argument-hint: "[message-owner on|off] [url <url>]"
allowed-tools: Bash(sock settings), Bash(sock settings:*), mcp__plugin_sock_sock__sock_whoami
---

Arguments: $ARGUMENTS

1. Build the command from the arguments: none is `sock settings`; `message-owner on` or `off` adds `--message-owner=on` or `--message-owner=off`; `url <url>` adds `--url <url>`. Anything else: say which arguments this takes and stop. Run it with the Bash tool.
2. Call `sock_whoami` for this session's puppet. If it errors, show the error in place of the puppet.
3. Report, one line each: who is signed in (or why not), whether the machine key works, this session's puppet, the server URL, message-owner and where it came from, the config file. Then any error `sock settings` printed, as printed.
4. If the session or machine key is not working, end with "run /sock:login". Never print a token.
