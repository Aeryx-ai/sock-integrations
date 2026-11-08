# Contributing

sock-integrations is the client side of Sock for coding agents: a Claude Code plugin in `claude-code/`, a Codex plugin at the root (`skills/`, `hooks/`, `mcp.json`) with a manual route in `codex/`, an Agent SDK example in `agent-sdk/`, the `sock listen --exec` commands in `runner/` and the hosted connector package in `hosted/`. Everything shells out to [sock-cli](https://github.com/Aeryx-ai/sock-cli) and [sock-mcp](https://github.com/Aeryx-ai/sock-mcp); nothing here speaks to the hub directly except the tests' stub.

## Before you start

- Open an issue before anything beyond a small fix (a typo, a doc correction, an obvious bug). New hooks, settings, runner variables and message wording need agreement first: each is something an owner sees or a host trusts.
- Report vulnerabilities privately as [SECURITY.md](SECURITY.md) describes. Never in an issue, pull request or discussion.

## Dev setup

- Sibling checkouts of [sock-cli](https://github.com/Aeryx-ai/sock-cli) at `../cli` and [sock-mcp](https://github.com/Aeryx-ai/sock-mcp) at `../mcp`. `make deps` builds both; the tests run their binaries.
- Go 1.26 or newer (`test/go.mod`). CI reads the version from it.
- `shellcheck`, `jq` and golangci-lint v2.13.2, the version CI installs:

  ```sh
  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
  ```

- `claude` is optional: `make build` validates the plugin manifests with it when present and says so when not.

| Target | Does |
|---|---|
| `make` / `make build` | Builds the siblings, validates the manifests, `go vet` |
| `make test` | `go test -race` in `test/` |
| `make lint` | `shellcheck` on every script, golangci-lint on the tests |
| `make check` | `build`, `lint`, `test`. CI runs this target |
| `make install` | Installs the binaries and the plugin into Claude Code from this checkout |
| `make redeploy` | Updates the installed marketplace and plugin |
| `make smoke`, `make e2e-dev` | Real-server checks; not part of `check` |

`test/go.mod` pins [sock-contract](https://github.com/Aeryx-ai/sock-contract) by tag. To develop against a local checkout, use an untracked `go.work` in `test/`; never commit a `go.work` or a `replace` directive.

## Making a change

1. Fork, then branch from `main`.
2. Write the test with the code. A bug fix starts with a test that fails without the fix. The tests in `test/` drive the real scripts and binaries: hooks through a fake `sock` on `PATH`, the runner and bridge through fake agents, `sock-mcp` through a stub hub. Run a new or changed test alone before the whole suite.
3. A fact that lives in two places gets a guard. The README's paths, make targets and integration list are checked by `test/readme_test.go`; the tool names in the skills, `codex/install.md` and the SDK example by `test/drift_test.go`; the manifests by `test/manifest_test.go`. When one fails, fix the file it names in the same change.
4. Plugin changes bump the version in `claude-code/.claude-plugin/plugin.json` or `.codex-plugin/plugin.json`: patch for wording and internals, minor when a hook, setting or tool changes.
5. Run `make check`. It must pass.
6. Keep a PR to one change.

CI skips pull requests that touch only Markdown, `docs/` or `.beads/`.

## What the owner sees is a contract

Hooks and runner messages reach a person's phone. Treat their shape as an API:

- Hooks never fail the agent and never message outside an interactive session or more than once an hour per project (notify.sh rule 8 in `docs/specs/integrations.md`).
- Never send transcripts, file contents, tokens or keys. Logs carry one line per event and never the text.
- Secrets stay in the sock config file or the plugin's sensitive setting, never in a committed file, a log or an error message.
- One puppet per binding, one consumer per puppet. A change that lets two agents share a puppet's deliveries is a bug.
- Diffs that touch a token, key or trust boundary get a security review before merge.

## Design records

Decisions go in `docs/adr/` as append-only records (`NNNN-kebab-title.md`, with a `Status:` line). Design goes in `docs/specs/` and the ADR links to it. Supersede, never rewrite.

## Commit messages

- Imperative subject, short, no trailing period.
- A body that explains why, not what the diff already shows.
- One logical change per commit.
- Signed commits are encouraged: `git commit -S` or `git config commit.gpgsign true`.

## Pull requests

- Link the issue it resolves.
- Say what changed and why. Call out any change to a hook, a setting, a runner variable or message wording.
- Include the tests.
- CI must be green before review. A maintainer reviews every PR. Push review fixes as new commits; the maintainer may squash on merge.

## License

MIT, copyright Aeryx Industries LLC ([LICENSE](LICENSE)). Inbound = outbound: by contributing you agree your contribution is licensed under the same terms.
