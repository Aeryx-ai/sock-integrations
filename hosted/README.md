# Hosted Sock submission

Status: staged sources, not ready to submit. Preparation authorized by Guy on 2026-10-17. Publisher requested: Aeryx. Verified portal identity remains unconfirmed. Tracking: integrations-6w8; operational dependency: integrations-ewi.

`sock/` is a separate portable hosted plugin, version 0.1.0. The desktop plugin at the repository root is unchanged. This directory contains no infrastructure changes, credentials, app bindings or lifecycle hooks.

## Review the draft

Listing copy, release notes and exactly five positive and three negative review cases live in `sock/plugin.json`. `sock/skills/sock/SKILL.md` describes the hosted tools and authenticated owner-reply events. Review materials and fixture setup are in `review.md`. `verification.json` records source revisions and observations, not an attestation or live MCP test pass.

Run local validation from this directory with a Python environment containing `jsonschema==4.26.0`:

```sh
python3 validate.py
python3 validate.py --submission
```

The first command checks both manifests against the pinned official Agent Plugins 1.0.0 schemas, listing field limits, review case shape/count, skill structure, file containment, PNG integrity and package inventory. The second also checks submission gates and is expected to fail while readiness is blocked. Neither command connects to Sock or changes services. Use `make check` for the structural checks. No validation target installs anything.

Schemas were downloaded from their declared URLs on 2026-10-17. `mcp.json` uses the portable transport fields accepted by that schema. OAuth configuration is deliberately left to live discovery and the portal: the current public Agent Plugins MCP schema rejects an `extensions` field on servers, although OpenAI's host-specific documentation describes one. Do not claim that the draft has a configured or tested OAuth connection. In the portal choose OAuth, scope `sock` and CIMD where supported. Issuer is `https://api.joinsock.com` and resource is `https://mcp.joinsock.com/mcp`, as specified in the hosted connector source. No bearer or client secret belongs in this package.

## Submission gates

- Hosted endpoint and authentication: source declares `https://mcp.joinsock.com/mcp`; DNS lookup failed during read-only verification. Production issuer discovery returned 404. Another person owns Render/metrics work. Leave infrastructure, DNS, OAuth settings and credentials to that workstream.
- Live MCP discovery, tool annotations, OAuth consent and all eight review cases: blocked, not run. The source exposes `sock_whoami`, `sock_list_conversations` and `sock_send`. The hosted policy refuses sends without a conversation id. There is no history/fetch tool.
- Owner reply delivery into a dot: implemented in the hosted service but not verified in this preparation. Requires a host-authorized `owner.reply` subscription, callback verification and a live owner message. Installation is not a subscription.
- Publisher: Aeryx is the requested public name. Confirm the owning OpenAI organization/project, Apps Management Write or owner access and the actual verified identity before upload. Do not claim Aeryx verification is complete.
- Countries and commerce: Guy has not chosen countries or declared commerce behavior. These fields are omitted, not set to empty values or inferred from Sock Pro. Confirm subscription/payment behavior and where purchases occur.
- Listing URLs: all four exact URLs in the manifest were fetched and their content inspected without login. Confirm that the existing privacy policy and terms cover the hosted connector's OAuth grants, encrypted puppet credentials, webhook subscription records, delivery of messages to the selected agent host and deletion/retention behavior. Reachability does not establish legal coverage. No policy text was changed.
- Reviewer access: needs a dedicated pre-provisioned sample account, fixtures and verified login instructions. Current Apple sign-in and invite-only onboarding are access issues to resolve, not permission to share Guy's account or change auth. Keep credentials in secure portal fields only.
- Demo: `review.md` provides a concrete rehearsal/recording script. No video has been recorded or hosted; `review.demo_recording_url` is omitted.
- Final portal checks, domain challenge, skills/tool scans, security findings and policy attestations: not performed. Have the authorized publisher complete legal/policy attestations. No upload, submission or public release is authorized by this preparation.

No distributable ZIP was generated. The submission skill's listing reference says: "If an endpoint is unavailable, stop packaging and report the missing integration." Staged source preserves the intended endpoint without presenting an unreachable service as ready. Finish the gates, revalidate and inspect the exact archive before any later authorized portal upload. Use the public Plugins portal, not private `create_plugin`, for directory submission.

## Assets and source

`assets/sock.png` was rendered at 512 x 512 from the existing website vector `website/public/brand/sock-app-icon-light.svg` using macOS Quick Look. It is reused for listing and composer icons. No new logo was generated. Optional dark assets and brand colors are omitted and do not block submission. Provenance and hashes are in `verification.json`.

References: [package format](https://developers.openai.com/plugins/build/plugins), [public submission](https://developers.openai.com/plugins/deploy/submission), [MCP Events](https://developers.openai.com/plugins/build/mcp-events), [desktop-only route](https://developers.openai.com/plugins/deploy/desktop-only). The remote package is separate from the partner-allowlisted desktop submission route.
