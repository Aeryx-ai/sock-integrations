---
description: Sign in to sock so this machine's sessions can message you and hear your replies
allowed-tools: Bash(sock login:*), Bash(sock login), Bash(sock settings), Bash(sock --version)
---

Sign the owner in to sock with the sock CLI. `sock login` is safe to run again: it keeps a session and a machine key that work and replaces only what the server rejects.

1. Run `sock login` with the Bash tool in the background (`run_in_background`), so its wait for the browser does not time out. If `sock` is not found, tell the owner to install the sock CLI (`brew install aeryx-ai/tap/sock aeryx-ai/tap/sock-mcp`) and stop.
2. Read its output as it arrives. When it prints a sign-in page and a code, show the owner both, exactly as printed, and say to open the page on any device and confirm the code there. Say nothing else until it finishes.
3. When it exits, report its last lines as printed. Never print or ask for a token.
   - `registration needs ...`: this address has no sock account yet. Ask the owner for a handle (lowercase a-z, 0-9 and _, 3 to 20 characters), a display name, and whether they accept the terms version the error names. Then rerun `sock login --handle <handle> --name <name> --accept-terms <version>` the same way, adding `--invite <code>` only if it asks for one.
   - `already signed in as @...` with `machine key works`: nothing changed; say so in one line.
   - A new sign-in, or a line saying a machine key was kept or replaced: end with "run /reload-plugins so this session picks it up".
   - Anything else: show the error and stop.
